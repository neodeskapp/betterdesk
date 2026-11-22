package bdrelay

import (
	"crypto/subtle"
	"sync"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// serveRelay is one side of a relay session. Before the handshake: the
// origin check (403). After it, each refusal is a close: a missing
// parameter or a role other than initiator and target 4400 "Missing or
// invalid parameters", the address's socket limit 4429 "Too many
// connections", an unknown session 4404 "Session not found", a token that
// is not the role's (also one of another length) 4403 "Invalid token", a
// session still waiting after SessionTTL 4408 "Session expired" (and it
// is gone), the role's place taken 4409 "Slot already taken". A panic
// while the socket joins closes it with 1011 "Internal error"; the
// service goes on.
//
// When both sides are there, each gets {"type":"paired","session":id}
// and from then on every message goes to the other side as it came, text
// or binary; messages before that are dropped. A side that leaves ends
// the session: the other is closed with 1000 "Peer disconnected" (1011
// "WebSocket error" when the side broke the protocol). A side that waits
// longer than SessionTTL is closed with 4408 "Timeout waiting for peer",
// a pair that is silent for IdleTimeout with 4408 "Idle timeout".
func (s *Service) serveRelay(u *ws.Upgrade) {
	if !u.CheckOrigin("bd-relay /ws/bd-relay") {
		return
	}
	conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxFrameBytes})
	if !ok {
		return
	}
	defer conn.Wait()
	var waitMu sync.Mutex
	j, ok := s.joinRelay(u, conn, &waitMu)
	if !ok {
		return
	}

	var err error
	for {
		var binary bool
		var data []byte
		binary, data, err = conn.Read()
		if err != nil {
			break
		}
		s.relayMessage(j.sess, conn, binary, data)
	}
	waitMu.Lock()
	j.waiting.Stop()
	waitMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.perIP.Done(j.ip)
	s.stopIdleLocked(j.sess)
	if ws.ProtocolError(err) {
		// The other side hears of the error
		s.teardownLocked(j.id, 1011, "WebSocket error")
	}
	s.teardownLocked(j.id, 1000, "Peer disconnected")
}

// relayJoin is a socket that took its place in a session.
type relayJoin struct {
	sess    *session
	id, ip  string
	waiting Timer
}

// joinRelay checks a relay socket and puts it into its session: the
// steps of handleRelayConnection up to the timer that waits for the other
// side, which takes waitMu before it ends the session. A socket that is
// refused has been closed when ok is false.
//
// A panic on the way, where the old handler threw, is logged and closes
// the socket with 1011 "Internal error". What the steps before it did
// stays, as in Node: a socket that already holds its slot keeps it until
// the session ends, and its address stays counted.
func (s *Service) joinRelay(u *ws.Upgrade, conn *ws.Conn, waitMu *sync.Mutex) (j relayJoin, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			logging.Logf("console", "ERROR: [bd-relay] connection handler failed: %v", r)
			conn.Close(1011, "Internal error")
			_ = conn.Discard()
			j, ok = relayJoin{}, false
		}
	}()
	ip := u.ClientIP()
	id, _ := u.SearchParam("session")
	token, _ := u.SearchParam("token")
	role, _ := u.SearchParam("role")
	if id == "" || token == "" || role == "" || (role != "initiator" && role != "target") {
		conn.Close(4400, "Missing or invalid parameters")
		_ = conn.Discard()
		return relayJoin{}, false
	}
	sess, code, reason := s.attachRelay(conn, id, token, role, ip)
	if code != 0 {
		conn.Close(code, reason)
		if code == 4409 {
			s.mu.Lock()
			s.perIP.Done(ip)
			s.mu.Unlock()
		}
		_ = conn.Discard()
		return relayJoin{}, false
	}
	waiting := s.clock.AfterFunc(SessionTTL, func() {
		waitMu.Lock()
		defer waitMu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		if sess.status == "pending" {
			s.teardownLocked(id, 4408, "Timeout waiting for peer")
		}
	})
	return relayJoin{sess: sess, id: id, ip: ip, waiting: waiting}, true
}

// attachRelay runs the checks of a relay socket that need the sessions
// and puts the socket into its slot, pairing the session when both sides
// are there. code is the close code of a refusal, 0 when the socket took
// its place. A refused 4409 has been counted for its address.
func (s *Service) attachRelay(conn *ws.Conn, id, token, role, ip string) (sess *session, code int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP.Count(ip) >= MaxRelayPerIP {
		return nil, 4429, "Too many connections"
	}
	sess, found := s.sessions.get(id)
	if !found {
		return nil, 4404, "Session not found"
	}
	expected := sess.initiatorToken
	if role == "target" {
		expected = sess.targetToken
	}
	if !tokensMatch(token, expected) {
		return nil, 4403, "Invalid token"
	}
	if s.nowMillis()-sess.createdAt > SessionTTL.Milliseconds() && sess.status == "pending" {
		s.sessions.delete(id)
		return nil, 4408, "Session expired"
	}
	s.perIP.Add(ip)
	slot := &sess.initiator
	if role == "target" {
		slot = &sess.target
	}
	if *slot != nil {
		return nil, 4409, "Slot already taken"
	}
	*slot = conn
	if sess.initiator != nil && sess.target != nil {
		s.activateLocked(sess)
	}
	return sess, 0, ""
}

// tokensMatch compares the token a relay socket brought with the one of
// its role in constant time. A token of another length does not match
// (every token is 64 hex digits, so the length is no secret), and neither
// does an empty one, also against an empty token.
func tokensMatch(provided, expected string) bool {
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// activateLocked pairs the two sides of a session.
func (s *Service) activateLocked(sess *session) {
	sess.status = "active"
	text := `{"type":"paired","session":` + quote(sess.id) + `}`
	for _, c := range []*ws.Conn{sess.initiator, sess.target} {
		c.SendText(text)
	}
	s.resetIdleLocked(sess)
}

// resetIdleLocked starts the idle timer of a paired session again.
func (s *Service) resetIdleLocked(sess *session) {
	if sess.idle != nil {
		sess.idle.Stop()
	}
	var timer Timer
	timer = s.clock.AfterFunc(IdleTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if sess.idle != timer {
			return
		}
		s.teardownLocked(sess.id, 4408, "Idle timeout")
	})
	sess.idle = timer
}

// stopIdleLocked stops the idle timer when a side of a pair leaves.
func (s *Service) stopIdleLocked(sess *session) {
	if sess.idle != nil {
		sess.idle.Stop()
	}
}

// relayMessage passes a message of a paired side to the other side and
// starts the idle time again. Before the pairing the other side's slot is
// empty (the second side's join pairs them under the same lock), so the
// message is dropped, as in Node, where nothing listened for it yet.
func (s *Service) relayMessage(sess *session, from *ws.Conn, binary bool, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.status == "active" {
		s.resetIdleLocked(sess)
	}
	to := sess.target
	if from == sess.target {
		to = sess.initiator
	}
	if to != nil {
		to.Send(binary, data)
	}
}
