package remoterelay

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
)

// deviceIDPattern is a device id the agent's socket takes.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// Register adds /ws/remote-agent/{id} and /ws/remote-viewer/{id}.
func (s *Service) Register(reg *ws.Registry) {
	reg.Handle("/ws/remote-agent/{id}", s.serveAgent)
	reg.Handle("/ws/remote-viewer/{id}", s.serveViewer)
}

// serveAgent is an agent's socket. Before the handshake, each refusal a
// bare status line: the origin check (403), a device id that is not 3 to
// 64 letters, digits, _ and - after percent decoding, or whose escapes
// spell no text (400), a token that is neither the device's pending
// single-use token nor a device token the device may use (403; 500 when
// the device token check fails).
func (s *Service) serveAgent(u *ws.Upgrade) {
	if !u.CheckOrigin("remote-agent " + u.Path) {
		return
	}
	deviceID, ok := decodeURIComponent(u.Param("id"))
	if !ok {
		u.Refuse(http.StatusBadRequest)
		return
	}
	token, _ := u.SearchParam("token")
	if !deviceIDPattern.MatchString(deviceID) {
		u.Refuse(http.StatusBadRequest)
		return
	}
	allowed := false
	if token != "" {
		allowed = s.consumeToken(deviceID, token)
		if !allowed {
			var err error
			allowed, err = s.opts.VerifyDevice(u.R.Context(), deviceID, token)
			if err != nil {
				u.Refuse(http.StatusInternalServerError)
				return
			}
		}
	}
	if !allowed {
		u.Refuse(http.StatusForbidden)
		return
	}
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()

	s.mu.Lock()
	sess := s.sessionLocked(deviceID)
	if sess.agent != nil {
		sess.agent.Close(1001, "New agent connected")
	}
	sess.streaming, sess.paused = false, nil
	sess.agent = conn
	logf("[RemoteRelay] Agent connected: %s", deviceID)
	sess.broadcastText(`{"type":"agent-ready","device_id":` + string(appendQuoted(nil, deviceID)) + `}`)
	if len(sess.viewers) > 0 {
		s.startStreamLocked(sess, deviceID)
	} else {
		s.scheduleIdleCloseLocked(sess, deviceID)
	}
	s.mu.Unlock()
	conn.KeepAlive(s.opts.PingInterval)

	for {
		binary, data, err := conn.Read()
		if err != nil {
			break
		}
		if binary {
			// A screen frame goes to every viewer that watches, unless it
			// is too big
			if len(data) <= MaxBinaryFrame {
				s.mu.Lock()
				sess.sendFrameLocked(data)
				s.mu.Unlock()
			}
			continue
		}
		v, text := agentMessage(string(data))
		if v == drop {
			conn.CloseNow()
			break
		}
		if v == forward {
			s.mu.Lock()
			sess.broadcastText(text)
			s.mu.Unlock()
		}
	}

	s.mu.Lock()
	if sess.agent == conn {
		sess.agent = nil
		sess.streaming, sess.paused = false, nil
		if sess.idle != nil {
			sess.idle.Stop()
		}
		logf("[RemoteRelay] Agent disconnected: %s", deviceID)
		sess.broadcastText(`{"type":"agent-disconnected"}`)
		s.dropIfEmptyLocked(sess, deviceID)
	}
	s.mu.Unlock()
}

// serveViewer is a viewer's socket. Before the handshake, each refusal a
// bare status line: the origin check (403), nobody signed in (401), a
// role without device.connect (403, also when the lookup fails), a device
// that does not exist or that the account's device scope leaves out
// (both 404, so a scope miss looks like no device), a failed lookup or
// scope check (403). The id is the path segment as sent, not decoded.
// After the handshake, a sixth viewer of a device is closed with 1008
// "Max viewers reached", and the others are checked again every
// SessionCheck (watchViewer). A viewer whose account may not use remote
// control (canControl) watches: its input never reaches the agent.
func (s *Service) serveViewer(u *ws.Upgrade) {
	if !u.CheckOrigin("remote-viewer " + u.Path) {
		return
	}
	sn := u.Session()
	if !sn.SignedIn() {
		u.Refuse(http.StatusUnauthorized)
		return
	}
	if allowed, err := u.RoleHasPermission(sn.UserRole(), "device.connect"); err != nil || !allowed {
		u.Refuse(http.StatusForbidden)
		return
	}
	deviceID := u.Param("id")
	device, err := s.opts.Devices(u.R.Context(), deviceID)
	if err != nil {
		logf("WARN: [RemoteRelay] remote-viewer scope check failed for %s: %v", deviceID, err)
		u.Refuse(http.StatusForbidden)
		return
	}
	if device == nil {
		u.Refuse(http.StatusNotFound)
		return
	}
	visible, err := s.opts.CanAccess(u.R.Context(), sn.User(), device)
	if err != nil {
		logf("WARN: [RemoteRelay] remote-viewer scope check failed for %s: %v", deviceID, err)
		u.Refuse(http.StatusForbidden)
		return
	}
	if !visible {
		u.Refuse(http.StatusNotFound)
		return
	}
	control := s.canControl(u.R.Context(), sn.UserID(), deviceID)
	conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxViewerMessage})
	if !ok {
		return
	}
	defer conn.Wait()

	s.mu.Lock()
	sess := s.sessionLocked(deviceID)
	if len(sess.viewers) >= MaxViewers {
		s.mu.Unlock()
		conn.Close(1008, "Max viewers reached")
		_ = conn.Discard()
		return
	}
	sess.viewers = append(sess.viewers, conn)
	agentReady := sess.agent != nil && sess.agent.Open()
	conn.SendText(`{"type":"session-info","agent_ready":` + boolText(agentReady) + `,"streaming":` + boolText(sess.streaming) + `}`)
	if agentReady && !sess.streaming {
		s.startStreamLocked(sess, deviceID)
	}
	s.mu.Unlock()
	conn.KeepAlive(s.opts.PingInterval)
	stopWatch := s.watchViewer(u, conn, sn.UserID(), deviceID)
	defer stopWatch()

read:
	for {
		binary, data, err := conn.Read()
		if err != nil {
			break
		}
		if binary {
			continue
		}
		v, event := viewerMessage(string(data))
		switch v {
		case drop:
			conn.CloseNow()
			break read
		case stop:
			s.mu.Lock()
			s.pauseViewerLocked(sess, deviceID, conn)
			s.mu.Unlock()
		case forward, tooDeep:
			if !control {
				continue
			}
			s.mu.Lock()
			agent := sess.agent
			s.mu.Unlock()
			if agent == nil || !agent.Open() {
				continue
			}
			if v == tooDeep {
				// JSON.stringify ran out of stack: Node ended
				conn.CloseNow()
				break read
			}
			agent.SendText(event)
		}
	}

	s.mu.Lock()
	for i, v := range sess.viewers {
		if v == conn {
			sess.viewers = append(sess.viewers[:i:i], sess.viewers[i+1:]...)
			break
		}
	}
	delete(sess.paused, conn)
	if len(sess.viewers) == 0 {
		s.stopStreamLocked(sess, deviceID, "last viewer left")
		s.scheduleIdleCloseLocked(sess, deviceID)
		s.dropIfEmptyLocked(sess, deviceID)
	} else {
		s.stopIfNobodyWatchesLocked(sess, deviceID, "last watching viewer left")
	}
	s.mu.Unlock()
}

// watchViewer looks at a viewer's session every SessionCheck while its
// socket is open, and closes the socket with 1008 "Session ended" once
// the session has ended or belongs to another account, its role no longer
// has device.connect, or the device is gone or outside the account's
// scope. A failed lookup counts as no, as at the upgrade. The returned
// function stops the watch and waits for it.
func (s *Service) watchViewer(u *ws.Upgrade, conn *ws.Conn, userID int64, deviceID string) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(s.opts.SessionCheck)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-conn.Context().Done():
				return
			case <-tick.C:
				if !s.viewerStillAllowed(u, conn.Context(), userID, deviceID) {
					conn.Close(1008, "Session ended")
					return
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// viewerStillAllowed reads a viewer's session again and runs the checks of
// the upgrade on it.
func (s *Service) viewerStillAllowed(u *ws.Upgrade, parent context.Context, userID int64, deviceID string) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	check := *u
	check.R = u.R.WithContext(ctx)
	sn := check.Session()
	if !sn.SignedIn() || sn.UserID() != userID {
		return false
	}
	if allowed, err := check.RoleHasPermission(sn.UserRole(), "device.connect"); err != nil || !allowed {
		return false
	}
	device, err := s.opts.Devices(ctx, deviceID)
	if err != nil || device == nil {
		return false
	}
	visible, err := s.opts.CanAccess(ctx, sn.User(), device)
	return err == nil && visible
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// or is v || fallback.
func (v jsValue) or(fallback jsValue) jsValue {
	if v.truthy() {
		return v
	}
	return fallback
}

// decodeURIComponent works like the JavaScript function: each %XX run
// must spell out whole UTF-8 characters in their shortest form, and no
// surrogate, else ok is false (URIError).
func decodeURIComponent(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := hexByte(s, i)
		if !ok {
			return "", false
		}
		i += 3
		if first < 0x80 {
			b.WriteByte(first)
			continue
		}
		size := 0
		switch {
		case first&0xE0 == 0xC0:
			size = 2
		case first&0xF0 == 0xE0:
			size = 3
		case first&0xF8 == 0xF0:
			size = 4
		default:
			return "", false
		}
		seq := []byte{first}
		for k := 1; k < size; k++ {
			next, ok := hexByte(s, i)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			seq = append(seq, next)
			i += 3
		}
		if !utf8.Valid(seq) {
			return "", false
		}
		b.Write(seq)
	}
	return b.String(), true
}

// hexByte reads the %XX at s[i:].
func hexByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' || !isHex(s[i+1]) || !isHex(s[i+2]) {
		return 0, false
	}
	return unhex(s[i+1])<<4 | unhex(s[i+2]), true
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// canControl reports whether the account may use remote control on the
// device: whether its client features leave remote_control on. A lookup
// that fails counts as no.
func (s *Service) canControl(ctx context.Context, userID int64, deviceID string) bool {
	if s.opts.FeaturesOff == nil {
		return true
	}
	off, err := s.opts.FeaturesOff(ctx, userID)
	if err != nil {
		logf("WARN: [RemoteRelay] client features of user %d failed for %s: %v; the viewer only watches", userID, deviceID, err)
		return false
	}
	for _, key := range off {
		if key == "remote_control" {
			return false
		}
	}
	return true
}
