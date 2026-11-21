package signal

import (
	"bytes"
	"net"
	"testing"
	"time"

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
