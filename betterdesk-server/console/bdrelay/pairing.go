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
// connections", an unknown session 4404 "Session not found", a wrong
// token 4403 "Invalid token", a session still waiting after SessionTTL
// 4408 "Session expired" (and it is gone), the role's place taken 4409
// "Slot already taken".
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
	ip := u.ClientAddr()
	id, _ := u.SearchParam("session")
	token, _ := u.SearchParam("token")
	role, _ := u.SearchParam("role")

	refuse := func(code int, reason string) {
		conn.Close(code, reason)
		_ = conn.Discard()
	}
	if id == "" || token == "" || role == "" || (role != "initiator" && role != "target") {
		refuse(4400, "Missing or invalid parameters")
		return
	}

	s.mu.Lock()
	if s.perIP.Count(ip) >= MaxRelayPerIP {
		s.mu.Unlock()
		refuse(4429, "Too many connections")
		return
	}
	sess, found := s.sessions.get(id)
	if !found {
		s.mu.Unlock()
		refuse(4404, "Session not found")
		return
	}
	expected := sess.initiatorToken
	if role == "target" {
		expected = sess.targetToken
	}
	if len(token) != len(expected) {
		// Node's compare threw on tokens of another length and the process
		// ended; this server drops the connection and goes on.
		s.mu.Unlock()
		logging.Logf("console", "WARN: [bd-relay] token of the wrong length for session %s, connection dropped", id)
		conn.CloseNow()
		_ = conn.Discard()
		return
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		s.mu.Unlock()
		refuse(4403, "Invalid token")
		return
	}
	if s.nowMillis()-sess.createdAt > SessionTTL.Milliseconds() && sess.status == "pending" {
		s.sessions.delete(id)
		s.mu.Unlock()
		refuse(4408, "Session expired")
		return
	}
	s.perIP.Add(ip)
	slot := &sess.initiator
	if role == "target" {
		slot = &sess.target
	}
	if *slot != nil {
		s.mu.Unlock()
		conn.Close(4409, "Slot already taken")
		s.mu.Lock()
		s.perIP.Done(ip)
		s.mu.Unlock()
		_ = conn.Discard()
		return
	}
	*slot = conn
	if sess.initiator != nil && sess.target != nil {
		s.activateLocked(sess)
	}
	s.mu.Unlock()

	// Waiting for the other side.
	var waitMu sync.Mutex
	waiting := s.clock.AfterFunc(SessionTTL, func() {
		waitMu.Lock()
		defer waitMu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		if sess.status == "pending" {
			s.teardownLocked(id, 4408, "Timeout waiting for peer")
		}
	})

	var err error
	for {
		var binary bool
		var data []byte
		binary, data, err = conn.Read()
		if err != nil {
			break
		}
		s.relayMessage(sess, conn, binary, data)
	}
	waitMu.Lock()
	waiting.Stop()
	waitMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if ws.ProtocolError(err) {
		// "error" and then "close": counted down twice, and the other
		// side hears of the error first.
		s.perIP.Done(ip)
		s.stopIdleLocked(sess)
		s.teardownLocked(id, 1011, "WebSocket error")
	}
	s.perIP.Done(ip)
	s.stopIdleLocked(sess)
	s.teardownLocked(id, 1000, "Peer disconnected")
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
