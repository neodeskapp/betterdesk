package signal

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/codec"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/crypto"
	"github.com/unitronix/betterdesk-server/peer"
	pb "github.com/unitronix/betterdesk-server/proto"
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
		RequestRelay: &pb.RequestRelay{Id: target, Uuid: "panel-proof-" + target},
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
			srv.peers.Get("BANNEDLOOP1").Banned = true
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
