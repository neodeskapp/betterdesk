package signal

import (
	"testing"

	"github.com/unitronix/betterdesk-server/config"
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
