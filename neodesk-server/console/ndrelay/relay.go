// Package ndrelay is the agent's connection to the console
// (services/bdRelay.js of the old console): the signal socket
// /ws/nd-signal each NeoDesk agent keeps open, through which the
// console pushes connection requests and asks the agent for things
// (RequestFromDevice), and the relay socket /ws/nd-relay that pairs two
// devices and passes their end-to-end encrypted frames on.
//
// Relay sessions and the open signal sockets live in this process only,
// as they did in Node.
package ndrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The limits of the old relay.
const (
	// MaxRelayPerIP is how many relay and signal sockets one address may
	// hold together. The address is the client's as the console's
	// TRUST_PROXY setting gives it (ws.Upgrade.ClientIP).
	MaxRelayPerIP = 10
	// MaxFrameBytes is the largest message on a relay socket.
	MaxFrameBytes = 2 << 20
	// SignalMaxPayload is the largest message on a signal socket.
	SignalMaxPayload = 64 << 10
	// SessionTTL is how long a relay session's tokens are good for, and
	// how long one side waits for the other.
	SessionTTL = 60 * time.Second
	// IdleTimeout ends a paired relay that passed no message for this long.
	IdleTimeout = 120 * time.Second
	// CleanupInterval is how often old sessions and dead sockets are
	// dropped.
	CleanupInterval = 30 * time.Second
	// RequestTimeout is how long RequestFromDevice waits by default.
	RequestTimeout = 15 * time.Second
	// PingInterval is how often a signal socket is pinged; one that left
	// two pings in a row unanswered is dropped (ws.Conn.Heartbeat).
	PingInterval = 30 * time.Second
)

// Clock is the time the relay sees: the old code's Date.now and setTimeout.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f once after d in its own goroutine, unless the
	// timer is stopped first.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc.
type Timer interface {
	// Stop prevents the call; it reports whether it did.
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Options configure the service.
type Options struct {
	// VerifyDevice checks the token a device presents on the signal
	// socket (verifyDeviceWsAuth; authn.Service.VerifyDeviceWSAuth). An
	// error closes the socket with 4500 "Auth error".
	VerifyDevice func(ctx context.Context, deviceID, token string) (bool, error)
	// Clock is real time when nil.
	Clock Clock
	// Random is crypto/rand when nil: session ids and tokens.
	Random io.Reader
	// PingInterval overrides PingInterval (tests).
	PingInterval time.Duration
}

// Service holds the relay sessions and the agents' signal sockets.
type Service struct {
	opts  Options
	clock Clock
	perIP *ws.AddrCounter

	mu       sync.Mutex
	sessions orderedMap[*session]
	online   orderedMap[*ws.Conn]
	pending  map[string]*pendingRequest
}

// New returns the service. Register adds its sockets, Run its sweep.
func New(opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = PingInterval
	}
	return &Service{
		opts: opts, clock: opts.Clock, perIP: ws.NewAddrCounter(),
		sessions: newOrderedMap[*session](), online: newOrderedMap[*ws.Conn](),
		pending: map[string]*pendingRequest{},
	}
}

// session is a relay session: two devices, a token each, and their
// sockets once they join.
type session struct {
	id                          string
	initiatorID, targetID       string
	initiatorToken, targetToken string
	initiator, target           *ws.Conn
	createdAt                   int64
	// status is pending, active or closed.
	status string
	// idle is the timer of a paired relay.
	idle Timer
}

// SessionInfo is a session without its tokens, as the device API shows it.
type SessionInfo struct {
	SessionID   string `json:"sessionId"`
	InitiatorID string `json:"initiatorId"`
	TargetID    string `json:"targetId"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"createdAt"`
}

// Pending is a session waiting for a device, as the heartbeat lists it.
type Pending struct {
	SessionID   string `json:"session_id"`
	InitiatorID string `json:"initiator_id"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Service) nowMillis() int64 { return s.clock.Now().UnixMilli() }

func (s *Service) randomHex(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.opts.Random, b); err != nil {
		panic(fmt.Sprintf("ndrelay: random source failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// randomUUID is crypto.randomUUID(): a version 4 UUID in lower case.
func (s *Service) randomUUID() string {
	b := make([]byte, 16)
	if _, err := io.ReadFull(s.opts.Random, b); err != nil {
		panic(fmt.Sprintf("ndrelay: random source failed: %v", err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// CreateRelaySession starts a session between two devices and returns
// its id and the token of each side (createRelaySession).
func (s *Service) CreateRelaySession(initiatorID, targetID string) (id, initiatorToken, targetToken string) {
	id = s.randomUUID()
	initiatorToken = s.randomHex(32)
	targetToken = s.randomHex(32)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions.set(id, &session{
		id: id, initiatorID: initiatorID, targetID: targetID,
		initiatorToken: initiatorToken, targetToken: targetToken,
		createdAt: s.nowMillis(), status: "pending",
	})
	return id, initiatorToken, targetToken
}

// GetRelaySession returns a session without its tokens, nil when there is
// none (getRelaySession).
func (s *Service) GetRelaySession(id string) *SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions.get(id)
	if !ok {
		return nil
	}
	return &SessionInfo{SessionID: id, InitiatorID: sess.initiatorID, TargetID: sess.targetID, Status: sess.status, CreatedAt: sess.createdAt}
}

// DeleteSession ends a session for the device API's DELETE: the sides
// that joined are closed with 1000 "Session deleted". It reports whether
// there was one.
func (s *Service) DeleteSession(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions.get(id)
	if !ok {
		return false
	}
	s.stopIdleLocked(sess)
	s.teardownLocked(id, 1000, "Session deleted")
	return true
}

// PendingFor lists the sessions that wait for a device to join as target,
// in the order they were made.
func (s *Service) PendingFor(targetID string) []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Pending{}
	s.sessions.each(func(id string, sess *session) {
		if sess.targetID == targetID && sess.status == "pending" {
			out = append(out, Pending{SessionID: id, InitiatorID: sess.initiatorID, CreatedAt: sess.createdAt})
		}
	})
	return out
}

// NotifyTarget sends payload as JSON to a device's signal socket and
// reports whether the device had one open (notifyTarget).
func (s *Service) NotifyTarget(targetID string, payload any) bool {
	s.mu.Lock()
	conn, ok := s.online.get(targetID)
	s.mu.Unlock()
	if !ok || !conn.Open() {
		return false
	}
	text, err := stringify(payload)
	if err != nil {
		return false
	}
	conn.SendText(text)
	return true
}

// IsDeviceOnline is isDeviceOnline: known is false when the device never
// had a signal socket that is still listed (the old function returned
// undefined then), online whether that socket is open.
func (s *Service) IsDeviceOnline(id string) (online, known bool) {
	s.mu.Lock()
	conn, ok := s.online.get(id)
	s.mu.Unlock()
	if !ok {
		return false, false
	}
	return conn.Open(), true
}

// OnlineDeviceIDs lists the devices with an open signal socket, in the
// order they first connected (getOnlineDeviceIds).
func (s *Service) OnlineDeviceIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	s.online.each(func(id string, c *ws.Conn) {
		if c.Open() {
			ids = append(ids, id)
		}
	})
	return ids
}

// teardownLocked ends a session: it is forgotten, and the sockets that are
// still open are closed with code and reason.
func (s *Service) teardownLocked(id string, code int, reason string) {
	sess, ok := s.sessions.get(id)
	if !ok {
		return
	}
	sess.status = "closed"
	s.sessions.delete(id)
	for _, c := range []*ws.Conn{sess.initiator, sess.target} {
		if c != nil {
			c.Close(code, reason)
		}
	}
}

// Run drops old sessions and dead sockets every CleanupInterval until ctx
// ends (the "nd-relay session cleanup" job).
func (s *Service) Run(ctx context.Context) {
	var mu sync.Mutex
	var timer Timer
	var arm func()
	arm = func() {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		timer = s.clock.AfterFunc(CleanupInterval, func() {
			if ctx.Err() != nil {
				return
			}
			s.Cleanup()
			arm()
		})
	}
	arm()
	<-ctx.Done()
	mu.Lock()
	if timer != nil {
		timer.Stop()
	}
	mu.Unlock()
}

// Cleanup is one sweep: pending sessions older than twice SessionTTL are
// ended with 4408 "Session expired", and devices whose signal socket is
// no longer open are forgotten.
func (s *Service) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowMillis()
	var expired []string
	s.sessions.each(func(id string, sess *session) {
		if sess.status == "pending" && now-sess.createdAt > 2*SessionTTL.Milliseconds() {
			expired = append(expired, id)
		}
	})
	for _, id := range expired {
		s.teardownLocked(id, 4408, "Session expired")
	}
	var dead []string
	s.online.each(func(id string, c *ws.Conn) {
		if !c.Open() {
			dead = append(dead, id)
		}
	})
	for _, id := range dead {
		s.online.delete(id)
	}
}

// Counts are the numbers of sessions, listed signal sockets and pending
// requests, for tests and the status page.
func (s *Service) Counts() (sessions, online, requests int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions.len(), s.online.len(), len(s.pending)
}

// stringify is JSON.stringify for the messages the relay sends.
func stringify(v any) (string, error) {
	b, err := httpx.Marshal(v)
	return string(b), err
}

func logf(format string, args ...any) { logging.Logf("console", format, args...) }

// orderedMap is a JavaScript Map: keys keep the order they were first
// set in; setting a key again keeps its place.
type orderedMap[V any] struct {
	keys []string
	vals map[string]V
}

func newOrderedMap[V any]() orderedMap[V] { return orderedMap[V]{vals: map[string]V{}} }

func (m *orderedMap[V]) get(k string) (V, bool) {
	v, ok := m.vals[k]
	return v, ok
}

func (m *orderedMap[V]) set(k string, v V) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

func (m *orderedMap[V]) delete(k string) {
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	for i, key := range m.keys {
		if key == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
}

func (m *orderedMap[V]) len() int { return len(m.keys) }

// each calls f for every entry in order; f must not change the map.
func (m *orderedMap[V]) each(f func(k string, v V)) {
	for _, k := range append([]string(nil), m.keys...) {
		if v, ok := m.vals[k]; ok {
			f(k, v)
		}
	}
}
