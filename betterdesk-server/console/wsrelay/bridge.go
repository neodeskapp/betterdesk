package wsrelay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ws"
)

// writeQueue is how many messages may wait for the TCP connection. The
// old service buffered without a limit; here the socket's reader waits.
const writeQueue = 64

// bridge is one browser socket and its TCP connection.
type bridge struct {
	s                *Service
	conn             *ws.Conn
	label            string
	target           string
	ip               string
	messageTransport bool
	guest            *guestGrant
	// background is the context of calls to the API server: they run to
	// their end like the old service's, also when the bridge ends first.
	background context.Context
	cancel     context.CancelFunc
	unregister func()

	// Used by the socket's reader only.
	guestDecoder   *frameDecoder
	relayBuffer    []byte
	relayValidated bool
	consumed       bool

	// Used by the TCP reader only.
	tcpDecoder *frameDecoder

	// dialed is closed when the dial has an answer, connected when the
	// connection is up, done when the bridge has ended.
	dialed    chan struct{}
	connected chan struct{}
	done      chan struct{}
	writes    chan []byte
	wg        sync.WaitGroup

	mu             sync.Mutex
	cleaned        bool
	openedReported bool
	openedAt       int64
	idle           Timer
	revalidation   Timer
	tcp            net.Conn
}

// bridge counts the socket against its address's limit and passes bytes
// between it and a new TCP connection to target until either side ends,
// nothing passes for IdleTimeout, or a guest check ends it. It returns
// when everything the bridge started has ended.
func (s *Service) bridge(conn *ws.Conn, ip, label, target string, messageTransport bool, guest *guestGrant) {
	s.mu.Lock()
	if s.perIP.Count(ip) >= MaxConnectionsPerIP {
		s.mu.Unlock()
		logf("WARN: WS proxy [%s]: Connection limit reached for %s", label, ip)
		conn.Close(1008, "Too many connections")
		_ = conn.Discard()
		return
	}
	s.perIP.Add(ip)
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(conn.Context())
	b := &bridge{
		s: s, conn: conn, label: label, target: target, ip: ip,
		messageTransport: messageTransport, guest: guest,
		background: context.WithoutCancel(conn.Context()), cancel: cancel,
		unregister: func() {},
		dialed:     make(chan struct{}), connected: make(chan struct{}), done: make(chan struct{}),
		writes: make(chan []byte, writeQueue),
	}
	if messageTransport {
		b.tcpDecoder = &frameDecoder{}
	}
	if guest != nil && label == "rendezvous" {
		b.guestDecoder = &frameDecoder{}
	}
	if guest != nil && guest.token != "" && guest.peerID != "" {
		b.unregister = s.registerGrant(guest, b)
	}
	b.wg.Add(2)
	go b.dialAndRead(ctx)
	go b.writeTCP()
	if guest != nil && guest.token != "" && guest.peerID != "" {
		// The first check goes out once the dial has an answer, as the old
		// service's did in practice: its connection to the server on the
		// same machine was up before the API server answered.
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			select {
			case <-b.dialed:
			case <-b.done:
				return
			}
			b.revalidate()
		}()
	}
	b.readSocket()
	cancel()
	b.wg.Wait()
}

// readSocket hands every message of the browser to onMessage until the
// socket ends.
func (b *bridge) readSocket() {
	for {
		_, data, err := b.conn.Read()
		if err != nil {
			if ws.ProtocolError(err) {
				logf("ERROR: WS proxy [%s]: WebSocket error: %s", b.label, err.Error())
				b.cleanup("ws-error", err.Error())
			} else {
				b.cleanup("ws-close", "")
			}
			return
		}
		b.onMessage(data)
	}
}

func (b *bridge) isCleaned() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cleaned
}

func (b *bridge) nowMillis() int64 { return b.s.clock.Now().UnixMilli() }

// onMessage passes one message of the browser to the server, after the
// guest checks: on /ws/rendezvous every frame of a guest must be a
// rendezvous message whose request names the link's device; on /ws/relay
// a guest's first bytes must decode to a relay request for that device.
// The first such request uses up one use of the link.
func (b *bridge) onMessage(data []byte) {
	b.resetIdle()
	if b.isCleaned() {
		return
	}
	opening := false
	if g := b.guest; g != nil {
		if b.label == "rendezvous" {
			frames, err := b.guestDecoder.feed(data)
			if err != nil {
				logf("ERROR: WS proxy [%s]: invalid relay WS frame: %s", b.label, err.Error())
				b.cleanup("invalid-ws-frame", err.Error())
				return
			}
			for _, payload := range frames {
				msg, err := decodeRendezvous(payload)
				if err != nil || !guestTargetMatches(msg, g.peerID) {
					logf("WARN: WS proxy [%s]: guest target mismatch for grant %s", b.label, grantLabel(g))
					b.cleanup("guest-target-mismatch", "")
					return
				}
				if msg.PunchHoleRequest != nil || msg.RequestRelay != nil {
					opening = true
				}
			}
		} else if !b.relayValidated {
			if len(b.relayBuffer)+len(data) > guestRelayBuffer {
				b.cleanup("guest-invalid-relay-frame", "")
				return
			}
			b.relayBuffer = append(b.relayBuffer, data...)
			msg, err := decodeRendezvous(b.relayBuffer)
			if err != nil {
				return
			}
			if msg.RequestRelay == nil || msg.RequestRelay.ID != g.peerID {
				logf("WARN: WS proxy [%s]: guest relay target mismatch for grant %s", b.label, grantLabel(g))
				b.cleanup("guest-target-mismatch", "")
				return
			}
			b.relayValidated = true
			opening = true
		}
	}
	if b.guest != nil && opening && !b.consumed {
		if err := b.s.consume(b.background, b.guest); err != nil {
			logf("WARN: WS proxy [%s]: guest session rejected: %s", b.label, err.Error())
			b.cleanup("guest-use-rejected", "")
			return
		}
		if b.isCleaned() {
			return
		}
		b.consumed = true
		now := b.nowMillis()
		b.mu.Lock()
		b.openedAt = now
		first := !b.openedReported
		b.openedReported = true
		b.mu.Unlock()
		if first {
			b.report("opened", "", now)
		}
	}
	payload := data
	if b.messageTransport {
		framed, err := EncodeRelayFrame(data)
		if err != nil {
			logf("ERROR: WS proxy [%s]: invalid relay WS frame: %s", b.label, err.Error())
			b.cleanup("invalid-ws-frame", err.Error())
			return
		}
		payload = framed
	}
	select {
	case b.writes <- payload:
	case <-b.done:
	}
}

// grantLabel names a link in a log line: its id, or "unknown".
func grantLabel(g *guestGrant) string {
	if v := g.id(); goapi.Truthy(v) {
		if text, ok := v.(string); ok {
			return text
		}
	}
	return "unknown"
}

// dialAndRead opens the TCP connection, then passes what the server sends
// to the browser: as it comes, or with ?transport=message one message per
// RustDesk frame without its header.
func (b *bridge) dialAndRead(ctx context.Context) {
	defer b.wg.Done()
	tcp, err := b.s.opts.Dial(ctx, "tcp", b.target)
	if err != nil {
		if !b.isCleaned() {
			logf("ERROR: WS proxy [%s]: TCP error (%s): %s", b.label, b.target, err.Error())
			b.cleanup("tcp-error", err.Error())
		}
		close(b.dialed)
		return
	}
	b.mu.Lock()
	if b.cleaned {
		b.mu.Unlock()
		_ = tcp.Close()
		close(b.dialed)
		return
	}
	b.tcp = tcp
	b.resetIdleLocked()
	close(b.connected)
	b.mu.Unlock()
	close(b.dialed)

	buf := make([]byte, 64<<10)
	for {
		n, err := tcp.Read(buf)
		if n > 0 && !b.fromServer(append([]byte{}, buf[:n]...)) {
			return
		}
		if err != nil {
			switch {
			case b.isCleaned():
			case errors.Is(err, io.EOF):
				b.cleanup("tcp-close", "")
			default:
				logf("ERROR: WS proxy [%s]: TCP error (%s): %s", b.label, b.target, err.Error())
				b.cleanup("tcp-error", err.Error())
			}
			return
		}
	}
}

// fromServer passes bytes of the server to the browser; false when they
// were not valid frames and the bridge ended.
func (b *bridge) fromServer(data []byte) bool {
	b.resetIdle()
	if !b.conn.Open() {
		return true
	}
	if !b.messageTransport {
		b.conn.Send(true, data)
		return true
	}
	frames, err := b.tcpDecoder.feed(data)
	if err != nil {
		logf("ERROR: WS proxy [%s]: invalid relay TCP frame: %s", b.label, err.Error())
		b.cleanup("invalid-tcp-frame", err.Error())
		return false
	}
	for _, f := range frames {
		b.conn.Send(true, f)
	}
	return true
}

// writeTCP writes the browser's messages to the server once the
// connection is up, in order.
func (b *bridge) writeTCP() {
	defer b.wg.Done()
	select {
	case <-b.connected:
	case <-b.done:
		return
	}
	b.mu.Lock()
	tcp := b.tcp
	b.mu.Unlock()
	for {
		select {
		case p := <-b.writes:
			if _, err := tcp.Write(p); err != nil {
				if !b.isCleaned() {
					logf("ERROR: WS proxy [%s]: TCP error (%s): %s", b.label, b.target, err.Error())
					b.cleanup("tcp-error", err.Error())
				}
				return
			}
		case <-b.done:
			return
		}
	}
}

// resetIdle starts the idle timer again.
func (b *bridge) resetIdle() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.cleaned {
		b.resetIdleLocked()
	}
}

func (b *bridge) resetIdleLocked() {
	if b.idle != nil {
		b.idle.Stop()
	}
	b.idle = b.s.clock.AfterFunc(IdleTimeout, func() {
		logf("WS proxy [%s]: Idle timeout for %s", b.label, b.ip)
		b.cleanup("idle-timeout", "")
	})
}

// revalidate asks the API server again whether the guest's link is good,
// and then every GuestRevalidationInterval, or at the link's end when that
// comes sooner. A link the API server no longer finds good, or one past
// its end, ends the bridge. A check that fails is not taken as a refusal:
// the link stays until its end.
func (b *bridge) revalidate() {
	if b.isCleaned() {
		return
	}
	g := b.guest
	data, err := b.s.validate(b.background, g.token, g.peerID)
	if err != nil {
		logf("WARN: WS proxy [%s]: guest grant revalidation failed: %s", b.label, err.Error())
	} else if !goapi.Truthy(goapi.Dig(data, "valid")) {
		b.cleanup("guest-grant-invalid", "")
		return
	}
	b.mu.Lock()
	if b.cleaned {
		b.mu.Unlock()
		return
	}
	var remaining int64
	if end, ok := g.expiresAt(); ok {
		remaining = end - b.nowMillis()
	}
	if remaining <= 0 {
		b.mu.Unlock()
		b.cleanup("guest-grant-expired", "")
		return
	}
	delay := GuestRevalidationInterval
	if left := time.Duration(remaining) * time.Millisecond; left < delay {
		delay = left
	}
	b.revalidation = b.s.clock.AfterFunc(jsDelay(delay), func() {
		b.mu.Lock()
		if b.cleaned {
			b.mu.Unlock()
			return
		}
		b.wg.Add(1)
		b.mu.Unlock()
		defer b.wg.Done()
		b.revalidate()
	})
	b.mu.Unlock()
}

// report tells the API server that a guest session opened or closed.
// A failure is logged.
func (b *bridge) report(event, reason string, openedAt int64) {
	g := b.guest
	if g == nil || g.peerID == "" {
		return
	}
	var duration int64
	if openedAt != 0 {
		if duration = b.nowMillis() - openedAt; duration < 0 {
			duration = 0
		}
	}
	var grantID any = ""
	if v := g.id(); goapi.Truthy(v) {
		grantID = v
	}
	body := goapi.NewObject(
		"event", event,
		"grant_id", grantID,
		"peer_id", g.peerID,
		"session_id", g.sessionID,
		"transport", b.label,
		"source_ip", b.ip,
		"duration_ms", float64(duration),
		"reason", reason,
	)
	if _, err := b.s.opts.GoAPI.Post(b.background, "/guest/access-links/events", goapi.Body(body)); err != nil {
		logf("WARN: WS proxy [%s]: guest audit event failed: %s", b.label, err.Error())
	}
}

// cleanup ends the bridge once: the timers stop, the link no longer lists
// it, a guest session that opened is reported closed, the TCP connection
// is dropped, the socket is closed without a status code (when it is
// still open) and the address is counted down.
func (b *bridge) cleanup(cause, detail string) {
	b.mu.Lock()
	if b.cleaned {
		b.mu.Unlock()
		return
	}
	b.cleaned = true
	if b.idle != nil {
		b.idle.Stop()
	}
	if b.revalidation != nil {
		b.revalidation.Stop()
	}
	reportClosed, openedAt := b.openedReported, b.openedAt
	if reportClosed {
		b.wg.Add(1)
	}
	tcp := b.tcp
	b.mu.Unlock()

	b.unregister()
	if reportClosed {
		go func() {
			defer b.wg.Done()
			b.report("closed", cause, openedAt)
		}()
	}
	close(b.done)
	b.cancel()
	if tcp != nil {
		_ = tcp.Close()
	}
	b.conn.Close(1005, "")
	b.s.mu.Lock()
	b.s.perIP.Done(b.ip)
	b.s.mu.Unlock()
	if detail != "" {
		logf("WS proxy [%s]: closed for %s cause=%s error=%q", b.label, b.ip, cause, detail)
	} else {
		logf("WS proxy [%s]: closed for %s cause=%s", b.label, b.ip, cause)
	}
}
