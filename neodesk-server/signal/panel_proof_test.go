package signal

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/codec"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/crypto"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// serveTCPOnLoopback runs srv's TCP connection handling on a loopback
// listener and returns its address.
func serveTCPOnLoopback(t *testing.T, srv *Server) string {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv.kp = kp
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleTCPConn(conn)
		}
	}()
	return ln.Addr().String()
}

// relayRequestOverTCP connects to addr from loopback as a plain client:
// it reads the server's KeyExchange, writes first as it is (nothing when
// nil), sends a RequestRelay for target and returns the answer, or the
// read's error when the server closed the connection.
func relayRequestOverTCP(t *testing.T, addr string, first []byte, target string) (*pb.RelayResponse, error) {
	t.Helper()
	return relayRequestOverTCPWithUUID(t, addr, first, target, "panel-proof-"+target)
}

// relayRequestOverTCPWithUUID is relayRequestOverTCP with the request's
// relay uuid: a ticket is bound to the account that asked for it first,
// so another account's session needs a uuid of its own, as the browser
// makes one per session.
func relayRequestOverTCPWithUUID(t *testing.T, addr string, first []byte, target, uuid string) (*pb.RelayResponse, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if ke, err := codec.ReadRawProto(conn, 5*time.Second); err != nil || ke.GetKeyExchange() == nil {
		t.Fatalf("server KeyExchange: %v %v", ke, err)
	}
	if first != nil {
		if _, err := conn.Write(first); err != nil {
			t.Fatalf("write first frame: %v", err)
		}
	}
	if err := codec.WriteRawProto(conn, &pb.RendezvousMessage{Union: &pb.RendezvousMessage_RequestRelay{
		RequestRelay: &pb.RequestRelay{Id: target, Uuid: uuid},
	}}); err != nil {
		t.Fatalf("write RequestRelay: %v", err)
	}
	resp, err := codec.ReadRawProto(conn, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if resp.GetRelayResponse() == nil {
		t.Fatalf("answer = %v, want a RelayResponse", resp)
	}
	return resp.GetRelayResponse(), nil
}

// A client on a loopback address is any client unless its connection
// starts with the panel proof: enrollment, the login-only rule and bans
// apply to it. With the proof the same request is the panel's web client.
func TestLoopbackClientNeedsThePanelProof(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		setup func(srv *Server)
	}{
		{"managed enrollment", config.EnrollmentModeManaged, func(*Server) {}},
		{"logged-in-only initiator", config.EnrollmentModeOpen, func(srv *Server) { srv.cfg.LoggedInOnlyInitiator = true }},
		{"operator-only outbound", config.EnrollmentModeOpen, func(srv *Server) { srv.cfg.OperatorOnlyOutbound = true }},
		{"banned peer on the address", config.EnrollmentModeOpen, func(srv *Server) {
			putOnlinePeer(srv, "BANNEDLOOP1", "127.0.0.1", 40001, peer.ConnTCP)
			srv.peers.SetBanned("BANNEDLOOP1", true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newTestSignalServer(t, tc.mode)
			putOnlinePeer(srv, "TGTPROOF1", "203.0.113.70", 52000, peer.ConnTCP)
			tc.setup(srv)
			addr := serveTCPOnLoopback(t, srv)

			rr, err := relayRequestOverTCP(t, addr, nil, "TGTPROOF1")
			if err != nil {
				t.Fatalf("without the proof: %v", err)
			}
			if rr.RefuseReason != refuseInitiatorNotAuthorized {
				t.Fatalf("without the proof: refuse reason %q, want %q", rr.RefuseReason, refuseInitiatorNotAuthorized)
			}

			rr, err = relayRequestOverTCP(t, addr, srv.PanelProof(), "TGTPROOF1")
			if err != nil {
				t.Fatalf("with the proof: %v", err)
			}
			if rr.RefuseReason != "" {
				t.Fatalf("with the proof: refused with %q", rr.RefuseReason)
			}
		})
	}
}

// A proof with another secret is a message with an unknown field: the
// server ends the connection. A proof from an address outside
// PANEL_SIGNAL_PROXY_CIDRS makes the connection any client.
func TestPanelProofMustMatchAndComeFromTheAllowedAddresses(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeManaged)
	putOnlinePeer(srv, "TGTPROOF2", "203.0.113.71", 52000, peer.ConnTCP)
	addr := serveTCPOnLoopback(t, srv)

	forged := srv.PanelProof()
	forged[len(forged)-1] ^= 0xff
	if rr, err := relayRequestOverTCP(t, addr, forged, "TGTPROOF2"); err == nil {
		t.Fatalf("a wrong secret got an answer: %+v", rr)
	}

	other := New(config.DefaultConfig(), nil, nil)
	if bytes.Equal(other.PanelProof(), srv.PanelProof()) {
		t.Fatal("two servers made the same secret")
	}

	_, nets, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.PanelSignalProxyCIDRs = []*net.IPNet{nets}
	rr, err := relayRequestOverTCP(t, addr, srv.PanelProof(), "TGTPROOF2")
	if err != nil {
		t.Fatalf("proof outside the addresses: %v", err)
	}
	if rr.RefuseReason != refuseInitiatorNotAuthorized {
		t.Fatalf("proof outside the addresses: refuse reason %q, want %q", rr.RefuseReason, refuseInitiatorNotAuthorized)
	}
}

// TestTheRelayTicketNamesTheConsoleSession: the web client's proof may
// name the console session of its account (A239: an account may be signed
// in on several browsers); the relay ticket of its request names it too,
// so a retry from another browser's session of the same account is not
// the same ticket, and saved sign-in hands its one use to the browser
// whose viewer opened the session. The proof with the session is longer
// than a one-byte frame header carries.
func TestTheRelayTicketNamesTheConsoleSession(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeManaged)
	_, targetAddr := attachTestUDPPair(t, srv)
	putOnlinePeer(srv, "WEBSESS01", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
	addr := serveTCPOnLoopback(t, srv)
	first, second := strings.Repeat("a1", 16), strings.Repeat("b2", 16)
	uuid := "panel-proof-console-session-WEBSESS01"
	if rr, err := relayRequestOverTCPWithUUID(t, addr, srv.PanelProofForSession(9, first), "WEBSESS01", uuid); err != nil || rr.RefuseReason != "" {
		t.Fatalf("web client with a console session: %+v, %v", rr, err)
	}
	if relay.AuthorizeRelayPairForSession(uuid, "panel-web-remote", "WEBSESS01", 9, second) {
		t.Error("the ticket took another console session of the account")
	}
	if !relay.AuthorizeRelayPairForSession(uuid, "panel-web-remote", "WEBSESS01", 9, first) {
		t.Error("the ticket does not name the console session that asked")
	}
}
