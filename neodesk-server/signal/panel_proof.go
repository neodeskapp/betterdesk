package signal

import (
	"crypto/rand"
	"crypto/subtle"
	"math"
	"net"

	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
	"google.golang.org/protobuf/encoding/protowire"
)

// The console's bridge for the browser's web client (console/wsrelay) runs
// in this process and opens a plain TCP connection to the signal port. Its
// first frame is the panel proof: a rendezvous message that holds only
// field panelProofField with a secret the server makes at start, for a
// signed-in account field panelAccountField with the account's id, and
// after it field panelSessionField with the handle of the account's
// console session (A239: an account may be signed in on several
// browsers, and saved sign-in binds its use to the one that opened the
// relay session). The
// secret stays in memory; it is never logged or written anywhere. A
// connection whose first frame is the proof, from an address in
// PANEL_SIGNAL_PROXY_CIDRS, is the panel's web client ("panel-web-remote"):
// its PunchHole and RequestRelay need no registered peer and no client
// login, because the console checked the account or the guest link at the
// websocket upgrade and checks every target, and the account's client
// features apply to its sessions. Every other connection, also one from a
// loopback address, is any client. Stock clients and devices never send
// the fields; a frame that holds them with other values is a message with
// unknown fields, which ends the connection as before.

// panelProofField is the field number of the proof, far above the fields
// of RendezvousMessage; panelAccountField is the account's id after it.
const (
	panelProofField   protowire.Number = 1000
	panelAccountField protowire.Number = 1001
	panelSessionField protowire.Number = 1002
)

// panelSessionSize is the length of a console session's handle: 32 hex
// digits (session.Handle).
const panelSessionSize = 32

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

// PanelProof returns the frame the console's bridge of a guest writes
// first on its TCP connection to the signal port, with the BytesCodec
// header. Each call returns a new copy.
func (s *Server) PanelProof() []byte { return s.PanelProofFor(0) }

// PanelProofFor returns the frame the console's bridge writes first on
// its TCP connection to the signal port, with the BytesCodec header: the
// proof, and the account's id when userID is above 0. Each call returns a
// new copy.
func (s *Server) PanelProofFor(userID int64) []byte { return s.PanelProofForSession(userID, "") }

// PanelProofForSession is PanelProofFor that also names the account's
// console session (its handle); a guest (userID 0) names none, and a
// handle of another form is left out.
func (s *Server) PanelProofForSession(userID int64, session string) []byte {
	payload := append([]byte(nil), s.panelProof...)
	if userID > 0 {
		payload = protowire.AppendTag(payload, panelAccountField, protowire.VarintType)
		payload = protowire.AppendVarint(payload, uint64(userID))
		if isHandle(session) {
			payload = protowire.AppendTag(payload, panelSessionField, protowire.BytesType)
			payload = protowire.AppendBytes(payload, []byte(session))
		}
	}
	// A frame of up to 63 bytes has a one-byte header (length*4); the
	// proof with a session is longer and takes the two-byte one
	// (length*4+1, little-endian)
	if len(payload) < 64 {
		frame := make([]byte, 0, 1+len(payload))
		frame = append(frame, byte(len(payload)<<2))
		return append(frame, payload...)
	}
	header := uint16(len(payload)<<2 | 1)
	frame := make([]byte, 0, 2+len(payload))
	frame = append(frame, byte(header), byte(header>>8))
	return append(frame, payload...)
}

// isHandle reports whether s has the form of a console session's handle.
func isHandle(s string) bool {
	if len(s) != panelSessionSize {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// readPanelProof reports whether msg is the proof message: no message
// kind, the proof's bytes compared in constant time, then nothing or the
// account field, then nothing or the console session's handle. userID is
// the account's id, 0 for a guest; session is the handle, "" for none.
func (s *Server) readPanelProof(msg *pb.RendezvousMessage) (userID int64, session string, ok bool) {
	if msg == nil || msg.Union != nil || len(s.panelProof) == 0 {
		return 0, "", false
	}
	unknown := msg.ProtoReflect().GetUnknown()
	if len(unknown) < len(s.panelProof) ||
		subtle.ConstantTimeCompare(unknown[:len(s.panelProof)], s.panelProof) != 1 {
		return 0, "", false
	}
	rest := unknown[len(s.panelProof):]
	if len(rest) == 0 {
		return 0, "", true
	}
	num, typ, n := protowire.ConsumeTag(rest)
	if n < 0 || num != panelAccountField || typ != protowire.VarintType {
		return 0, "", false
	}
	id, m := protowire.ConsumeVarint(rest[n:])
	if m < 0 || id == 0 || id > math.MaxInt64 {
		return 0, "", false
	}
	rest = rest[n+m:]
	if len(rest) == 0 {
		return int64(id), "", true
	}
	num, typ, n = protowire.ConsumeTag(rest)
	if n < 0 || num != panelSessionField || typ != protowire.BytesType {
		return 0, "", false
	}
	handle, m := protowire.ConsumeBytes(rest[n:])
	if m < 0 || n+m != len(rest) || !isHandle(string(handle)) {
		return 0, "", false
	}
	return int64(id), string(handle), true
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

// panelConn is an open TCP connection that proved itself the panel's web
// client, with the account it named (0 for a guest).
type panelConn struct {
	conn    net.Conn
	userID  int64
	session string
}

// isPanelConnAddr reports whether addr is the address of an open TCP
// connection that proved itself the panel's web client.
func (s *Server) isPanelConnAddr(addr *net.UDPAddr) bool {
	_, ok := s.panelConnAt(addr)
	return ok
}

// panelConnAt returns the panel's connection at addr.
func (s *Server) panelConnAt(addr *net.UDPAddr) (*panelConn, bool) {
	if addr == nil {
		return nil, false
	}
	value, ok := s.panelConns.Load(normalizeAddrKey(addr.String()))
	if !ok {
		return nil, false
	}
	pc, ok := value.(*panelConn)
	return pc, ok && pc != nil
}

// panelSession is the console session the panel's connection at addr
// named: "" for a guest, none, or no panel connection.
func (s *Server) panelSession(addr *net.UDPAddr) string {
	if pc, ok := s.panelConnAt(addr); ok {
		return pc.session
	}
	return ""
}

// panelAccount is the account the panel's connection at addr named: 0 for
// a guest or for no panel connection.
func (s *Server) panelAccount(addr *net.UDPAddr) int64 {
	if pc, ok := s.panelConnAt(addr); ok {
		return pc.userID
	}
	return 0
}
