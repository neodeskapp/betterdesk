package bdrelay

import (
	"regexp"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ws"
)

// deviceIDPattern is a device id the signal socket takes.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// Register adds /ws/bd-relay and /ws/bd-signal.
func (s *Service) Register(reg *ws.Registry) {
	reg.Handle("/ws/bd-relay", s.serveRelay)
	reg.Handle("/ws/bd-signal", s.serveSignal)
}

// serveSignal is an agent's signal socket. Before the handshake: the
// origin check (403). After it, each refusal is a close: no device_id or
// token 4400 "Missing device_id or token", a device id that is not 3 to
// 64 letters, digits, _ and - 4400 "Invalid device_id", a token the device
// may not use 4403 "Invalid token" (4500 "Auth error" when the check
// fails), the address's socket limit 4429 "Too many connections". A
// device that connects again replaces its open socket, which is closed
// with 4409 "Replaced by new connection".
//
// Messages the agent sends before the socket is accepted are dropped, as
// they were when no message listener was attached yet.
func (s *Service) serveSignal(u *ws.Upgrade) {
	if !u.CheckOrigin("bd-signal /ws/bd-signal") {
		return
	}
	conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: SignalMaxPayload})
	if !ok {
		return
	}
	defer conn.Wait()
	ip := u.ClientAddr()
	deviceID, _ := u.SearchParam("device_id")
	token, _ := u.SearchParam("token")

	var attached atomic.Bool
	ended := make(chan error, 1)
	go func() {
		for {
			binary, data, err := conn.Read()
			if err != nil {
				ended <- err
				return
			}
			if !attached.Load() {
				continue
			}
			if reply := s.signalMessage(deviceID, binary, data); reply != "" {
				conn.SendText(reply)
			}
		}
	}()
	refuse := func(code int, reason string) {
		conn.Close(code, reason)
		<-ended
	}

	if deviceID == "" || token == "" {
		refuse(4400, "Missing device_id or token")
		return
	}
	if !deviceIDPattern.MatchString(deviceID) {
		refuse(4400, "Invalid device_id")
		return
	}
	authed, err := s.opts.VerifyDevice(conn.Context(), deviceID, token)
	if err != nil {
		refuse(4500, "Auth error")
		return
	}
	if !authed {
		refuse(4403, "Invalid token")
		return
	}

	s.mu.Lock()
	var endedEarly error
	select {
	case endedEarly = <-ended:
	default:
	}
	if s.perIP.Count(ip) >= MaxRelayPerIP {
		s.mu.Unlock()
		if endedEarly == nil {
			refuse(4429, "Too many connections")
		}
		return
	}
	s.perIP.Add(ip)
	if existing, ok := s.online.get(deviceID); ok {
		existing.Close(4409, "Replaced by new connection")
	}
	s.online.set(deviceID, conn)
	s.mu.Unlock()
	if endedEarly != nil {
		// The socket ended while its token was checked, before anything
		// listened for its end: the old relay counted it for good and
		// listed it until the sweep.
		return
	}
	attached.Store(true)
	conn.KeepAlive(s.opts.PingInterval)
	conn.SendText(`{"type":"welcome","device_id":` + quote(deviceID) + `}`)

	err = <-ended
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws.ProtocolError(err) {
		// "error" and then "close": the address is counted down twice.
		s.perIP.Done(ip)
	}
	s.perIP.Done(ip)
	if current, ok := s.online.get(deviceID); ok && current == conn {
		s.online.delete(deviceID)
	}
}

// signalMessage handles one message of an agent: a heartbeat, the answer
// to a relay request, the answer to RequestFromDevice. It returns what
// goes back to the agent, "" for nothing. Anything that is not a JSON
// object of a known type is ignored.
func (s *Service) signalMessage(deviceID string, binary bool, data []byte) string {
	text := string(data)
	if binary && !utf8.Valid(data) {
		text = decodeBuffer(data)
	}
	v, err := goapi.Parse([]byte(text))
	if err != nil {
		return ""
	}
	msg, ok := v.(*goapi.Object)
	if !ok || msg == nil {
		return ""
	}
	typ, _ := msg.Get("type").(string)
	switch typ {
	case "heartbeat":
		reply, _ := stringify(goapi.NewObject("type", "heartbeat_ack", "ts", float64(s.nowMillis())))
		return reply
	case "relay_accept":
		id, ok := msg.Get("session_id").(string)
		if !ok {
			return ""
		}
		s.mu.Lock()
		sess, found := s.sessions.get(id)
		var token string
		ready := found && sess.targetID == deviceID && sess.status == "pending"
		if ready {
			token = sess.targetToken
		}
		s.mu.Unlock()
		if ready {
			reply, _ := stringify(goapi.NewObject("type", "relay_ready", "session_id", id, "token", token))
			return reply
		}
	case "relay_reject":
		id, ok := msg.Get("session_id").(string)
		if !ok {
			return ""
		}
		s.mu.Lock()
		if sess, found := s.sessions.get(id); found && sess.targetID == deviceID {
			s.teardownLocked(id, 4403, "Connection rejected by target")
		}
		s.mu.Unlock()
	case "command_response":
		id, ok := msg.Get("request_id").(string)
		if !ok {
			return ""
		}
		s.answer(id, msg)
	}
	return ""
}

// quote is a JSON string.
func quote(s string) string {
	text, _ := stringify(s)
	return text
}

// decodeBuffer is buffer.toString() for bytes that are not UTF-8: each
// maximal part that is not a character becomes U+FFFD.
func decodeBuffer(b []byte) string {
	var out strings.Builder
	for i := 0; i < len(b); {
		c := b[i]
		need, lower, upper := 0, byte(0x80), byte(0xBF)
		switch {
		case c < 0x80:
			out.WriteByte(c)
			i++
			continue
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			} else if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			} else if c == 0xF4 {
				upper = 0x8F
			}
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lower || b[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !ok {
			out.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		out.Write(b[i:j])
		i = j
	}
	return out.String()
}
