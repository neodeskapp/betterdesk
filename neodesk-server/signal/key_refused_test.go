package signal

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// TestAPeerWithARefusedKeyIsNotOnline: a computer that came back with a
// new key keeps sending heartbeats, which the server refuses to take as
// a sign of life: it takes no session, so the list must not say Online
// while the viewer says offline. It counts again once it registers with
// the stored key (L335).
func TestAPeerWithARefusedKeyIsNotOnline(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	uuid := []byte("machine-reset-01")
	storedPK := bytes.Repeat([]byte{0x7A}, 32)
	if err := database.UpsertPeer(&db.Peer{ID: "RESETPC01", UUID: hex.EncodeToString(uuid), PK: storedPK, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	recv, from := attachTestUDPPair(t, srv)

	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "RESETPC01", Serial: 1}, from)
	_ = readUDPRendezvous(t, recv)
	if !srv.peers.IsOnline("RESETPC01", config.RegTimeout) {
		t.Fatal("the peer is not online after its heartbeat")
	}

	// The reset computer: same machine, a new key, from its own address
	newPK := bytes.Repeat([]byte{0x5A}, 32)
	if got := registerPkResult(srv.processRegisterPk(&pb.RegisterPk{Id: "RESETPC01", Uuid: uuid, Pk: newPK}, from.String())); got != pb.RegisterPkResponse_NOT_SUPPORT {
		t.Fatalf("new key = %v, want NOT_SUPPORT", got)
	}
	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "RESETPC01", Serial: 2}, from)
	if resp := readUDPRendezvous(t, recv).GetRegisterPeerResponse(); resp == nil || !resp.GetRequestPk() {
		t.Errorf("the heartbeat of a refused key is not asked for the key again: %v", resp)
	}
	if srv.peers.IsOnline("RESETPC01", config.RegTimeout) {
		t.Error("a peer whose key is refused is online")
	}

	// Another address cannot knock a peer offline with a made-up key
	if got := registerPkResult(srv.processRegisterPk(&pb.RegisterPk{Id: "RESETPC01", Uuid: uuid, Pk: storedPK}, from.String())); got != pb.RegisterPkResponse_OK {
		t.Fatalf("stored key = %v, want OK", got)
	}
	if !srv.peers.IsOnline("RESETPC01", config.RegTimeout) {
		t.Error("the peer is not online again after registering with its key")
	}
	srv.processRegisterPk(&pb.RegisterPk{Id: "RESETPC01", Uuid: uuid, Pk: newPK}, "198.51.100.9:40000")
	if !srv.peers.IsOnline("RESETPC01", config.RegTimeout) {
		t.Error("a RegisterPk from another address took the peer offline")
	}
}

// TestAStoredPeerIsAskedForItsKeyWhenItRegistersAgain: a computer the
// server knows registers again after the server restarted or its entry
// expired (a reboot), from its stored address. The server takes the stored
// key for signing at once but still asks the computer for its key, so a
// computer that came back with another key is refused and shows offline
// instead of staying Online while every session fails its handshake (L438).
func TestAStoredPeerIsAskedForItsKeyWhenItRegistersAgain(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, from := attachTestUDPPair(t, srv)
	uuid := []byte("rebooted-machine")
	storedPK := bytes.Repeat([]byte{0x12}, 32)
	if err := database.UpsertPeer(&db.Peer{ID: "REBOOT001", UUID: hex.EncodeToString(uuid), PK: storedPK, IP: from.IP.String(), Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}

	srv.handleRegisterPeer(&pb.RegisterPeer{Id: "REBOOT001", Serial: 1}, from)
	if resp := readUDPRendezvous(t, recv).GetRegisterPeerResponse(); resp == nil || !resp.GetRequestPk() {
		t.Fatalf("a stored peer registering again is not asked for its key: %v", resp)
	}
	if entry := srv.peers.Get("REBOOT001"); entry == nil || !bytes.Equal(entry.PK, storedPK) {
		t.Fatalf("the stored key is not live for signing: %+v", entry)
	}

	// The computer answers with the key it has now, a new one
	newPK := bytes.Repeat([]byte{0x47}, 32)
	if got := registerPkResult(srv.processRegisterPk(&pb.RegisterPk{Id: "REBOOT001", Uuid: uuid, Pk: newPK}, from.String())); got != pb.RegisterPkResponse_NOT_SUPPORT {
		t.Fatalf("new key = %v, want NOT_SUPPORT", got)
	}
	if srv.peers.IsOnline("REBOOT001", config.RegTimeout) {
		t.Error("a peer that came back with another key is online")
	}
}
