package onboard

import (
	"sync"
	"time"
)

// What the server answered a quick support app that asked about a
// connection's caller (A271, A273), as the helper's viewer reads it.
const (
	// CallerVouched: the person sees Allow.
	CallerVouched = "vouched"
	// CallerNotAllowedYet: the person has not clicked Allow in the quick
	// support window yet.
	CallerNotAllowedYet = "not_allowed_yet"
	// CallerNotHTTPS: the app reached the console over plain HTTP, where
	// quick support takes only the one-time password.
	CallerNotHTTPS = "not_https"
	// CallerRefused: any other refusal.
	CallerRefused = "refused"
	// CallerNotAsked: the app did not ask (yet), as an app too old to
	// accept by click never does.
	CallerNotAsked = "not_asked"
)

// callerBookTTL is how long the book keeps an answer; a viewer reads it
// within seconds.
const callerBookTTL = 10 * time.Minute

type callerNote struct {
	device string
	state  string
	at     time.Time
}

type appScheme struct {
	https bool
	at    time.Time
}

// CallerBook remembers, in memory, what the server answered the quick
// support apps about their callers, by relay session, and whether each
// app's last request came over HTTPS. The app polls every few seconds, so
// the scheme is known again soon after a restart.
type CallerBook struct {
	mu      sync.Mutex
	now     func() time.Time
	callers map[string]callerNote
	schemes map[string]appScheme
}

// NewCallerBook returns an empty book; now nil is time.Now.
func NewCallerBook(now func() time.Time) *CallerBook {
	if now == nil {
		now = time.Now
	}
	return &CallerBook{now: now, callers: map[string]callerNote{}, schemes: map[string]appScheme{}}
}

func (b *CallerBook) pruneLocked(now time.Time) {
	for k, n := range b.callers {
		if now.Sub(n.at) > callerBookTTL {
			delete(b.callers, k)
		}
	}
	for k, s := range b.schemes {
		if now.Sub(s.at) > callerBookTTL {
			delete(b.schemes, k)
		}
	}
}

// NoteApp records the scheme of a request of the app of session sessionID.
func (b *CallerBook) NoteApp(sessionID string, https bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.pruneLocked(now)
	b.schemes[sessionID] = appScheme{https: https, at: now}
}

// AppHTTPS reports whether the app of session sessionID last came over
// HTTPS, and whether the book knows.
func (b *CallerBook) AppHTTPS(sessionID string) (https, known bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(b.now())
	s, ok := b.schemes[sessionID]
	return s.https, ok
}

// Note records the answer about the caller of relay session relay to
// device.
func (b *CallerBook) Note(relay, device, state string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.pruneLocked(now)
	b.callers[relay] = callerNote{device: device, state: state, at: now}
}

// State is the answer about the caller of relay session relay to device,
// CallerNotAsked when the app did not ask about it.
func (b *CallerBook) State(relay, device string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(b.now())
	n, ok := b.callers[relay]
	if !ok || n.device != device {
		return CallerNotAsked
	}
	return n.state
}
