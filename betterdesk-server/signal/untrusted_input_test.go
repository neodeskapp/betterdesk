package signal

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/audit"
	"github.com/unitronix/betterdesk-server/config"
	cryptopkg "github.com/unitronix/betterdesk-server/crypto"
	"github.com/unitronix/betterdesk-server/peer"
	pb "github.com/unitronix/betterdesk-server/proto"
)

// A relay uuid is client text of any length; a short one must be answered,
// not crash the process.
func TestRequestRelayWithShortUUIDDoesNotPanic(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, initAddr := attachTestUDPPair(t, srv)
	putOnlinePeer(srv, "SHORTID01", initAddr.IP.String(), initAddr.Port, peer.ConnUDP)
	putOnlinePeer(srv, "SHORTTG01", "203.0.113.70", 52000, peer.ConnUDP)

	srv.handleRequestRelay(&pb.RequestRelay{Id: "SHORTTG01", Uuid: "x"}, initAddr)
	if rr := readUDPRendezvous(t, recv).GetRelayResponse(); rr == nil || rr.Uuid != "x" {
		t.Fatalf("UDP RequestRelay with uuid x: got %+v, want a RelayResponse carrying it", rr)
	}

	resp := srv.handleRequestRelayTCP(&pb.RequestRelay{Id: "SHORTTG01", Uuid: "y"}, initAddr, peer.ConnTCP)
	if rr := resp.GetRelayResponse(); rr == nil || rr.Uuid != "y" {
		t.Fatalf("TCP RequestRelay with uuid y: got %+v", resp)
	}
}

func TestRecoverMessagePanicKeepsServing(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	ran := false
	srv.recoverMessagePanic("UDP", "198.51.100.72:1", func() { panic("boom") })
	srv.recoverMessagePanic("UDP", "198.51.100.72:1", func() { ran = true })
	if !ran {
		t.Fatal("the handler after a recovered panic did not run")
	}
}

func expectNoUDP(t *testing.T, conn *net.UDPConn, what string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 65535)
	if n, _, err := conn.ReadFromUDP(buf); err == nil {
		t.Fatalf("%s: the address named in the packet got %d bytes", what, n)
	}
}

// putPunchTarget registers a target with a key, and gives the server a key
// pair to sign it with.
func putPunchTarget(t *testing.T, srv *Server, id string, addr *net.UDPAddr) {
	t.Helper()
	kp, err := cryptopkg.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	srv.kp = kp
	srv.peers.Put(&peer.Entry{
		ID:         id,
		PK:         bytes.Repeat([]byte{0x33}, 32),
		IP:         addr.String(),
		UDPAddr:    addr,
		ConnType:   peer.ConnUDP,
		LastReg:    time.Now(),
		StatusTier: peer.StatusOnline,
	})
}

func TestPunchHoleSentFromStrangerIsDropped(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, named := attachTestUDPPair(t, srv)
	putPunchTarget(t, srv, "PHSTGT01", udpAddr("203.0.113.90", 40000))
	srv.storePendingUUID("PHSTGT01", named, "", "PHSINIT1")

	srv.handlePunchHoleSent(&pb.PunchHoleSent{
		Id:          "PHSTGT01",
		SocketAddr:  cryptopkg.EncodeAddr(named),
		RelayServer: "evil.example:21117",
	}, udpAddr("198.51.100.9", 5000), true)

	expectNoUDP(t, recv, "PunchHoleSent from a stranger")
}

func TestPunchHoleSentFromStrangerDoesNotStoreRelay(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	putPunchTarget(t, srv, "PHSTGT04", udpAddr("203.0.113.93", 40000))
	srv.storePendingUUID("PHSTGT04", udpAddr("198.51.100.12", 41000), "", "PHSINIT4")

	srv.handlePunchHoleSent(&pb.PunchHoleSent{
		Id:          "PHSTGT04",
		SocketAddr:  cryptopkg.EncodeAddr(udpAddr("198.51.100.12", 41000)),
		RelayServer: "evil.example:21117",
	}, udpAddr("198.51.100.9", 5000), true)

	if relay := srv.peers.Get("PHSTGT04").RelayServer; relay != "" {
		t.Fatalf("a stranger's PunchHoleSent stored the relay %q for the target", relay)
	}
}

func TestPunchHoleSentWithoutPendingPunchIsDropped(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, named := attachTestUDPPair(t, srv)
	putPunchTarget(t, srv, "PHSTGT02", udpAddr("203.0.113.91", 40000))

	srv.handlePunchHoleSent(&pb.PunchHoleSent{
		Id:         "PHSTGT02",
		SocketAddr: cryptopkg.EncodeAddr(named),
	}, udpAddr("203.0.113.91", 40001), true)

	expectNoUDP(t, recv, "PunchHoleSent without a punch")
}

func TestPunchHoleSentFromTargetForPendingPunchIsForwarded(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, initiator := attachTestUDPPair(t, srv)
	putPunchTarget(t, srv, "PHSTGT03", udpAddr("203.0.113.92", 40000))
	srv.storePendingUUID("PHSTGT03", initiator, "", "PHSINIT3")

	// Stock clients send PunchHoleSent from a new socket: same IP, new port.
	srv.handlePunchHoleSent(&pb.PunchHoleSent{
		Id:          "PHSTGT03",
		SocketAddr:  cryptopkg.EncodeAddr(initiator),
		RelayServer: "relay.example:21117",
	}, udpAddr("203.0.113.92", 40001), true)

	if phr := readUDPRendezvous(t, recv).GetPunchHoleResponse(); phr == nil || len(phr.Pk) == 0 {
		t.Fatalf("the initiator got %+v, want a signed PunchHoleResponse", phr)
	}
	if relay := srv.peers.Get("PHSTGT03").RelayServer; relay != "relay.example:21117" {
		t.Fatalf("target's relay = %q", relay)
	}
}

func TestClientFetchLocalAddrIsDropped(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, named := attachTestUDPPair(t, srv)

	srv.handleUDPMessage(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_FetchLocalAddr{
		FetchLocalAddr: &pb.FetchLocalAddr{SocketAddr: cryptopkg.EncodeAddr(named), RelayServer: "text of the sender"},
	}}, udpAddr("198.51.100.10", 5000))

	expectNoUDP(t, recv, "FetchLocalAddr from a client")
}

func TestRegisterPkRefusesOversizedKeyAndUUID(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)

	for _, msg := range []*pb.RegisterPk{
		{Id: "BIGPK0001", Uuid: []byte("machine-uid"), Pk: bytes.Repeat([]byte{1}, 60000)},
		{Id: "BIGPK0002", Uuid: bytes.Repeat([]byte{2}, 1000), Pk: bytes.Repeat([]byte{2}, 32)},
	} {
		if got := registerPkResult(srv.processRegisterPk(msg, "198.51.100.11:5000")); got == pb.RegisterPkResponse_OK {
			t.Fatalf("RegisterPk with a %d byte pk and a %d byte uuid was accepted", len(msg.Pk), len(msg.Uuid))
		}
		if stored, _ := database.GetPeer(msg.Id); stored != nil {
			t.Fatalf("%s was stored", msg.Id)
		}
	}
}

// A target id that no peer can have is answered as unknown before anything
// is logged or audited: the id is client text and may hold a line break.
func TestInvalidTargetIDIsRefusedBeforeAudit(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	logger := audit.NewLogger("")
	srv.SetAuditLogger(logger)
	forged := "VICTIM\n2026/11/21 [audit] peer_banned"

	resp := srv.handlePunchHoleRequestTCP(&pb.PunchHoleRequest{Id: forged}, udpAddr("198.51.100.20", 5000))
	if phr := resp.GetPunchHoleResponse(); phr == nil || phr.Failure != pb.PunchHoleResponse_ID_NOT_EXIST {
		t.Fatalf("PunchHole for an invalid id: got %+v, want ID_NOT_EXIST", resp)
	}
	resp = srv.handleRequestRelayTCP(&pb.RequestRelay{Id: forged, Uuid: "u"}, udpAddr("198.51.100.20", 5000), peer.ConnTCP)
	if rr := resp.GetRelayResponse(); rr == nil || rr.RefuseReason == "" {
		t.Fatalf("RequestRelay for an invalid id: got %+v, want a refusal", resp)
	}
	if n := logger.Total(); n != 0 {
		t.Fatalf("%d audit events for requests naming an invalid id", n)
	}
}

// Refusals of unauthenticated requests are counted per address: a flood
// writes one audit event, not one per packet, and does not push earlier
// events out of the ring.
func TestRefusalsAreAuditedOncePerAddress(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	logger := audit.NewLogger("")
	srv.SetAuditLogger(logger)
	putOnlinePeer(srv, "FLOODTGT1", "203.0.113.94", 52000, peer.ConnUDP)

	for i := 0; i < 50; i++ {
		srv.handlePunchHoleRequestTCP(&pb.PunchHoleRequest{Id: "FLOODTGT1"}, udpAddr("198.51.100.21", 5000+i))
	}
	if got := len(logger.RecentByAction(audit.ActionConnectionDenied, 100)); got != 1 {
		t.Fatalf("50 refused requests from one address wrote %d audit events, want 1", got)
	}
	srv.handlePunchHoleRequestTCP(&pb.PunchHoleRequest{Id: "FLOODTGT1"}, udpAddr("198.51.100.22", 5000))
	if got := len(logger.RecentByAction(audit.ActionConnectionDenied, 100)); got != 2 {
		t.Fatalf("a refusal from another address: %d audit events, want 2", got)
	}
}

func TestRefusalAuditWindowCarriesSuppressedCount(t *testing.T) {
	c := newRefusalCounter(time.Minute, 100)
	start := time.Unix(1000, 0)
	if record, _ := c.note("198.51.100.23", start); !record {
		t.Fatal("the first refusal from an address must be recorded")
	}
	for i := 0; i < 9; i++ {
		if record, _ := c.note("198.51.100.23", start.Add(time.Second)); record {
			t.Fatal("a refusal inside the window was recorded")
		}
	}
	record, suppressed := c.note("198.51.100.23", start.Add(61*time.Second))
	if !record || suppressed != 9 {
		t.Fatalf("after the window: record=%v suppressed=%d, want true and 9", record, suppressed)
	}
}

func TestRefusalAuditHasAGlobalBudget(t *testing.T) {
	c := newRefusalCounter(time.Minute, 3)
	now := time.Unix(2000, 0)
	recorded := 0
	for i := 0; i < 10; i++ {
		if record, _ := c.note(fmt.Sprintf("198.51.100.%d", 100+i), now); record {
			recorded++
		}
	}
	if recorded != 3 {
		t.Fatalf("refusals from 10 forged addresses wrote %d events in one window, want the budget of 3", recorded)
	}
}

func iceCandidateTo(id string) *pb.RendezvousMessage {
	return &pb.RendezvousMessage{Union: &pb.RendezvousMessage_IceCandidate{IceCandidate: &pb.IceCandidate{
		Id: id, SessionKey: "sess", Candidate: "candidate:1 1 UDP 2130706431 203.0.113.95 50000 typ host",
	}}}
}

// IceCandidate is forwarded only between the two ends of a punch the
// server signalled, not from anyone to any online peer.
func TestIceCandidateFromStrangerIsNotForwarded(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, targetAddr := attachTestUDPPair(t, srv)
	putOnlinePeer(srv, "ICETGT001", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)

	srv.handleMessage(iceCandidateTo("ICETGT001"), &net.TCPAddr{IP: net.ParseIP("198.51.100.24"), Port: 5000})

	expectNoUDP(t, recv, "IceCandidate from a stranger")
}

func TestIceCandidateBetweenPunchEndsIsForwarded(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	recv, peerAddr := attachTestUDPPair(t, srv)
	initiator := &net.TCPAddr{IP: net.ParseIP("198.51.100.25"), Port: 5000}
	putOnlinePeer(srv, "ICETGT002", peerAddr.IP.String(), peerAddr.Port, peer.ConnUDP)
	srv.storePendingUUID("ICETGT002", udpAddr("198.51.100.25", 5000), "", "ICEINI002")

	srv.handleMessage(iceCandidateTo("ICETGT002"), initiator)
	if readUDPRendezvous(t, recv).GetIceCandidate() == nil {
		t.Fatal("the initiator's candidate did not reach the target")
	}

	// The target answers with its own candidates to the initiator's id.
	putOnlinePeer(srv, "ICEINI002", peerAddr.IP.String(), peerAddr.Port+1, peer.ConnUDP)
	srv.peers.Get("ICEINI002").UDPAddr = peerAddr
	srv.peers.Get("ICETGT002").UDPAddr = udpAddr("203.0.113.96", 40000)
	srv.peers.Get("ICETGT002").IP = "203.0.113.96:40000"
	srv.handleMessage(iceCandidateTo("ICEINI002"), &net.TCPAddr{IP: net.ParseIP("203.0.113.96"), Port: 40000})
	if readUDPRendezvous(t, recv).GetIceCandidate() == nil {
		t.Fatal("the target's candidate did not reach the initiator")
	}
}
