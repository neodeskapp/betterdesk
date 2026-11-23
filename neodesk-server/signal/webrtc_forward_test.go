package signal

import (
	"net"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	cryptopkg "github.com/neodeskapp/neodesk/neodesk-server/crypto"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// Recent RustDesk clients negotiate a WebRTC transport. The rendezvous server
// must carry the SDP offer/answer and trickle-ICE candidates between peers.
// A server whose proto drops those fields still lets a new client reach an old
// one (which never sends them) but breaks new<->new sessions.
func TestHandlePunchHoleRequestTCPForwardsWebRTCSDPOffer(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.cfg.P2PFirst = false
	srv.localIP.Store("198.51.100.20")

	recv, targetAddr := attachTestUDPPair(t, srv)
	putOnlinePeer(srv, "WSRTCTG01", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
	putOnlinePeer(srv, "WSRTCINIT", "203.0.113.61", 51000, peer.ConnTCP)

	const offer = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n"

	srv.handlePunchHoleRequestTCP(&pb.PunchHoleRequest{
		Id:             "WSRTCTG01",
		WebrtcSdpOffer: offer,
		SwitchCode:     "SW1",
	}, udpAddr("203.0.113.61", 51000))

	msg := readUDPRendezvous(t, recv)
	ph := msg.GetPunchHole()
	if ph == nil {
		t.Fatalf("expected PunchHole at target, got %+v", msg)
	}
	if ph.GetWebrtcSdpOffer() != offer {
		t.Fatalf("webrtc_sdp_offer = %q, want the offer forwarded verbatim", ph.GetWebrtcSdpOffer())
	}
}

func TestHandlePunchHoleSentForwardsWebRTCSDPAnswer(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.localIP.Store("198.51.100.20")

	// Real loopback socket standing in for the initiator, so the response can
	// actually be delivered and inspected.
	initiator, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen initiator: %v", err)
	}
	t.Cleanup(func() { initiator.Close() })
	initiatorAddr := initiator.LocalAddr().(*net.UDPAddr)

	send, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen send: %v", err)
	}
	t.Cleanup(func() { send.Close() })
	srv.udpConn = send

	putOnlinePeer(srv, "WSRTCINI01", initiatorAddr.IP.String(), initiatorAddr.Port, peer.ConnUDP)
	putOnlinePeer(srv, "WSRTCTGT01", "203.0.113.80", 52100, peer.ConnUDP)
	srv.storePendingUUID("WSRTCTGT01", initiatorAddr, "", "WSRTCINI01")

	const answer = "v=0\r\no=- 2 2 IN IP4 127.0.0.1\r\na=fingerprint:sha-256 AB:CD\r\n"

	srv.handlePunchHoleSent(&pb.PunchHoleSent{
		Id:              "WSRTCTGT01",
		SocketAddr:      cryptopkg.EncodeAddr(initiatorAddr),
		WebrtcSdpAnswer: answer,
	}, udpAddr("203.0.113.80", 52100), true)

	msg := readUDPRendezvous(t, initiator)
	phr := msg.GetPunchHoleResponse()
	if phr == nil {
		t.Fatalf("expected PunchHoleResponse at initiator, got %+v", msg)
	}
	if phr.GetWebrtcSdpAnswer() != answer {
		t.Fatalf("webrtc_sdp_answer = %q, want the answer forwarded verbatim", phr.GetWebrtcSdpAnswer())
	}
}

func TestHandleIceCandidateForwardsToTargetPeer(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)

	recv, targetAddr := attachTestUDPPair(t, srv)
	putOnlinePeer(srv, "ICETARGET1", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
	initiator := udpAddr("198.51.100.90", 50001)
	srv.storePendingUUID("ICETARGET1", initiator, "", "ICEINIT1")

	srv.handleIceCandidate(&pb.IceCandidate{
		Id:         "ICETARGET1",
		SessionKey: "sess-1",
		Candidate:  "candidate:1 1 UDP 2130706431 203.0.113.90 50000 typ host",
	}, initiator)

	msg := readUDPRendezvous(t, recv)
	ice := msg.GetIceCandidate()
	if ice == nil {
		t.Fatalf("expected IceCandidate at target, got %+v", msg)
	}
	if ice.GetCandidate() == "" || ice.GetSessionKey() != "sess-1" {
		t.Fatalf("ice candidate not forwarded verbatim: %+v", ice)
	}
}

func TestHandleIceCandidateIgnoresUnknownTarget(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
	// Must not panic or allocate pending state for a missing peer.
	srv.handleIceCandidate(&pb.IceCandidate{Id: "NOSUCHPEER", Candidate: "candidate:x"}, udpAddr("198.51.100.91", 1))
	srv.handleIceCandidate(&pb.IceCandidate{}, nil)
}
