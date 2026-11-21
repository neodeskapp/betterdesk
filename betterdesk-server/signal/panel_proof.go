package signal

import (
	"crypto/rand"
	"crypto/subtle"
	"net"

	pb "github.com/unitronix/betterdesk-server/proto"
	"google.golang.org/protobuf/encoding/protowire"
)

// The console's bridge for the browser's web client (console/wsrelay) runs
// in this process and opens a plain TCP connection to the signal port. Its
// first frame is the panel proof: a rendezvous message that holds only
// field panelProofField with a secret the server makes at start. The
// secret stays in memory; it is never logged or written anywhere. A
// connection whose first frame is the proof, from an address in
// PANEL_SIGNAL_PROXY_CIDRS, is the panel's web client ("panel-web-remote"):
// its PunchHole and RequestRelay need no registered peer and no client
// login, because the console checked the account or the guest link at the
// websocket upgrade and checks every target. Every other connection, also
// one from a loopback address, is any client. Stock clients and devices
// never send the field; a frame that holds it with another value is a
// message with an unknown field, which ends the connection as before.

// panelProofField is the field number of the proof, far above the fields
// of RendezvousMessage.
const panelProofField protowire.Number = 1000

// panelSecretSize is the length of the secret in bytes.
const panelSecretSize = 32

// newPanelProof returns the encoded field of a fresh secret: the unknown
// bytes a proof message holds.
func newPanelProof() []byte {
	secret := make([]byte, panelSecretSize)
	// crypto/rand.Read does not fail; it ends the program when the system
	// has no randomness.
	_, _ = rand.Read(secret)
	proof := protowire.AppendTag(nil, panelProofField, protowire.BytesType)
	return protowire.AppendBytes(proof, secret)
}

// PanelProof returns the frame the console's bridge writes first on its
// TCP connection to the signal port, with the BytesCodec header. Each call
// returns a new copy.
func (s *Server) PanelProof() []byte {
	// The proof is 35 bytes, so the header is the single byte length*4.
	frame := make([]byte, 0, 1+len(s.panelProof))
	frame = append(frame, byte(len(s.panelProof)<<2))
	return append(frame, s.panelProof...)
}

// isPanelProof reports whether msg is the proof message: no message kind
// and exactly the proof's bytes, compared in constant time.
func (s *Server) isPanelProof(msg *pb.RendezvousMessage) bool {
	if msg == nil || msg.Union != nil || len(s.panelProof) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(msg.ProtoReflect().GetUnknown(), s.panelProof) == 1
}

// panelSourceAllowed reports whether a connection that sent the proof
// comes from an address of PANEL_SIGNAL_PROXY_CIDRS (default loopback).
func (s *Server) panelSourceAllowed(remote net.Addr) bool {
	host, _, err := net.SplitHostPort(remote.String())
	if err != nil {
		return false
	}
	return s.cfg.IPIsPanelSignalProxy(net.ParseIP(host))
}

// isPanelConnAddr reports whether addr is the address of an open TCP
// connection that proved itself the panel's web client.
func (s *Server) isPanelConnAddr(addr *net.UDPAddr) bool {
	if addr == nil {
		return false
	}
	_, ok := s.panelConns.Load(normalizeAddrKey(addr.String()))
	return ok
}
