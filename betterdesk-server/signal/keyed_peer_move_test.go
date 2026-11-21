package signal

import (
	"bytes"
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/crypto"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
	pb "github.com/unitronix/betterdesk-server/proto"
	"google.golang.org/protobuf/proto"
)

var (
	victimUUID = []byte("victim-machine-uid")
	victimPK   = bytes.Repeat([]byte{0x56}, 32)
)

// putKeyedVictim stores a peer with a key in the database and online over
// UDP at addr.
func putKeyedVictim(t *testing.T, srv *Server, database db.Database, id string, addr *net.UDPAddr) {
	t.Helper()
	if err := database.UpsertPeer(&db.Peer{
		ID:     id,
		UUID:   hex.EncodeToString(victimUUID),
		PK:     victimPK,
		IP:     addr.IP.String(),
		Status: "ONLINE",
	}); err != nil {
		t.Fatal(err)
	}
	srv.peers.Put(&peer.Entry{
		ID:         id,
		UUID:       victimUUID,
		PK:         victimPK,
		IP:         addr.String(),
		UDPAddr:    addr,
		ConnType:   peer.ConnUDP,
		LastReg:    time.Now(),
		StatusTier: peer.StatusOnline,
	})
}

func registerPeerAsksForKey(t *testing.T, msg *pb.RendezvousMessage) bool {
	t.Helper()
	rpr := msg.GetRegisterPeerResponse()
	if rpr == nil {
		t.Fatalf("expected RegisterPeerResponse, got %+v", msg)
	}
	return rpr.GetRequestPk()
}

func TestHeartbeatFromAnotherIPDoesNotMoveKeyedPeer(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeLocked)
	recv, attacker := attachTestUDPPair(t, srv)
	victimAddr := udpAddr("203.0.113.80", 40000)
	putKeyedVictim(t, srv, database, "VICTIM01", victimAddr)

	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "VICTIM01", Serial: 1}, attacker)

	if !registerPeerAsksForKey(t, readUDPRendezvous(t, recv)) {
		t.Fatal("a heartbeat from another IP must be answered with request_pk")
	}
	if ip, _, _ := srv.peers.Endpoint("VICTIM01"); !ip.Equal(victimAddr.IP) {
		t.Fatalf("victim moved to %s on a heartbeat alone", ip)
	}
	if found := srv.peers.FindByAddr(attacker); found != nil {
		t.Fatalf("the sender's address resolves to %s", found.ID)
	}
	if id, ok := srv.requireAuthorizedInitiator(attacker, "OTHER001", "", 0); ok && id == "VICTIM01" {
		t.Fatal("the sender was authorized as the victim")
	}
}

func TestHeartbeatFromNewPortOfSameIPMovesKeyedPeer(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, newAddr := attachTestUDPPair(t, srv)
	putKeyedVictim(t, srv, database, "ROAMING01", udpAddr(newAddr.IP.String(), 1))

	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "ROAMING01", Serial: 1}, newAddr)

	if registerPeerAsksForKey(t, readUDPRendezvous(t, recv)) {
		t.Fatal("a NAT port change must not ask for the key again")
	}
	if entry := srv.peers.Get("ROAMING01"); entry.UDPAddr.Port != newAddr.Port {
		t.Fatalf("UDP address = %s, want %s", entry.UDPAddr, newAddr)
	}
}

func TestMatchingRegisterPkOverUDPMovesKeyedPeer(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, newAddr := attachTestUDPPair(t, srv)
	putKeyedVictim(t, srv, database, "MOVED0001", udpAddr("203.0.113.81", 40000))

	srv.handleRegisterPk(&pb.RegisterPk{Id: "MOVED0001", Uuid: victimUUID, Pk: victimPK}, newAddr)

	if got := registerPkResult(readUDPRendezvous(t, recv)); got != pb.RegisterPkResponse_OK {
		t.Fatalf("matching RegisterPk = %v, want OK", got)
	}
	if found := srv.peers.FindByAddr(newAddr); found == nil || found.ID != "MOVED0001" {
		t.Fatalf("after a matching RegisterPk the new address resolves to %+v", found)
	}
	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "MOVED0001", Serial: 1}, newAddr)
	if registerPeerAsksForKey(t, readUDPRendezvous(t, recv)) {
		t.Fatal("the heartbeat after the move must not ask for the key again")
	}
}

func TestHeartbeatAfterRestartFromAnotherIPDoesNotRegisterKeyedPeer(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, attacker := attachTestUDPPair(t, srv)
	putKeyedVictim(t, srv, database, "RESTART02", udpAddr("203.0.113.82", 40000))
	srv.peers.Remove("RESTART02")

	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "RESTART02", Serial: 1}, attacker)

	if !registerPeerAsksForKey(t, readUDPRendezvous(t, recv)) {
		t.Fatal("a heartbeat from another IP than the stored one must be answered with request_pk")
	}
	if entry := srv.peers.Get("RESTART02"); entry != nil {
		t.Fatalf("the heartbeat registered the peer at %s", entry.IP)
	}
}

func TestWSHeartbeatDoesNotMoveKeyedUDPPeer(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	victimAddr := udpAddr("203.0.113.83", 40000)
	putKeyedVictim(t, srv, database, "VICTIM03", victimAddr)

	resp, accepted := srv.handleRegisterPeerWS(&pb.RegisterPeer{Id: "VICTIM03", Serial: 1}, "198.51.100.7:51000")
	if accepted {
		t.Fatal("the WebSocket was accepted as the victim's transport")
	}

	if resp == nil || !registerPeerAsksForKey(t, resp) {
		t.Fatalf("a WebSocket heartbeat for a UDP peer must ask for the key, got %+v", resp)
	}
	if entry := srv.peers.Get("VICTIM03"); entry.ConnType != peer.ConnUDP || entry.IP != victimAddr.String() {
		t.Fatalf("victim moved to %s %s", entry.ConnType, entry.IP)
	}
}

func TestRegisterPkWithoutIdentityIsRefusedForKnownPeer(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeLocked)
	victimAddr := udpAddr("203.0.113.84", 40000)
	putKeyedVictim(t, srv, database, "VICTIM04", victimAddr)
	attacker := "198.51.100.8:47777"
	srv.tcpPunchConns.Store(normalizeAddrKey(attacker), &tcpPunchConn{createdAt: time.Now()})

	for _, msg := range []*pb.RegisterPk{
		{Id: "VICTIM04"},
		{Id: "VICTIM04", Uuid: victimUUID},
		{Id: "VICTIM04", Pk: victimPK},
	} {
		if got := registerPkResult(srv.processRegisterPk(msg, attacker)); got == pb.RegisterPkResponse_OK {
			t.Fatalf("RegisterPk uuid=%d pk=%d bytes was accepted", len(msg.Uuid), len(msg.Pk))
		}
	}
	if entry := srv.peers.Get("VICTIM04"); entry.IP != victimAddr.String() {
		t.Fatalf("victim's address became %s", entry.IP)
	}
	if bound := srv.peerIDBySessionAddr(attacker); bound != "" {
		t.Fatalf("the sender's session was bound to %q", bound)
	}
}

// No-register-device RegisterPk is a key-only exchange: it must not bind the
// WebSocket as the transport of the id it names.
func TestWSNoRegisterDeviceDoesNotBindPeer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SignalPort = 29210
	cfg.RelayPort = 29211
	dir := t.TempDir()
	cfg.DBPath = dir + "/test.db"
	cfg.KeyFile = dir + "/id_ed25519"
	database, err := db.OpenSQLite(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	database.Migrate()
	defer database.Close()
	kp, err := crypto.LoadOrGenerateKeyPair(cfg.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, kp, database)
	ctx := t.Context()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	victimAddr := udpAddr("203.0.113.85", 40000)
	putKeyedVictim(t, srv, database, "VICTIM05", victimAddr)
	time.Sleep(200 * time.Millisecond)

	ws, _, err := websocket.Dial(ctx, "ws://127.0.0.1:29212/", nil)
	if err != nil {
		t.Fatalf("WS dial: %v", err)
	}
	defer ws.CloseNow()
	data, _ := proto.Marshal(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_RegisterPk{
		RegisterPk: &pb.RegisterPk{Id: "VICTIM05", NoRegisterDevice: true},
	}})
	if err := ws.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatal(err)
	}
	readWSProtoSkippingKeepAlive(t, ctx, ws)

	if entry := srv.PeerMap().Get("VICTIM05"); entry.ConnType != peer.ConnUDP || entry.WSConn != nil {
		t.Fatalf("victim bound to the WebSocket: %s %v", entry.ConnType, entry.WSConn != nil)
	}
}
