package limits

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Config describes one limiter.
type Config struct {
	// Name appears in the limiter table and in tests.
	Name string
	// WindowMs is the length of a window in milliseconds.
	WindowMs int64
	// Max is how many requests of one key a window allows. A request is
	// refused when its count is above Max.
	Max int64
	// Message is the JSON body of the 429 answer.
	Message any
	// Key names who a request counts against.
	Key func(r *http.Request) string
	// Skip is true for requests that are not counted. apiPath is the
	// request's path as /api/..., also when the limiter sits below /api.
	Skip func(r *http.Request, apiPath string) bool
	// Now is the clock. nil means time.Now.
	Now func() time.Time
}

type client struct {
	hits  int64
	reset int64
}

// Limiter counts requests per key in fixed windows, with the rules of
// express-rate-limit and its memory store:
//
//   - The first request of a key starts a window of WindowMs. A request
//     at or after the window's end starts a new one with a count of zero.
//   - Every counted request raises the count, refused ones too. A refused
//     request does not move the window's end.
//   - A request is refused when the count is above Max, so exactly Max
//     requests pass in a window.
//   - Every counted answer carries RateLimit-Policy, RateLimit-Limit,
//     RateLimit-Remaining and RateLimit-Reset (seconds until the window
//     ends, rounded up, left out when zero). A refused one also carries
//     Retry-After and has status 429 with Message as its JSON body.
//
// Keys that were not seen for one to two windows are dropped, so memory
// is bounded by the keys of the last two windows.
type Limiter struct {
	cfg Config

	mu       sync.Mutex
	current  map[string]*client
	previous map[string]*client
	// rotateAt is when current becomes previous
	rotateAt int64

	// counted is a test hook: it sees every counted request
	counted func(r *http.Request, name string, hits, limit int64, refused bool)
}

// New returns a limiter.
func New(cfg Config) *Limiter {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	l := &Limiter{cfg: cfg, current: map[string]*client{}, previous: map[string]*client{}}
	l.rotateAt = l.nowMs() + cfg.WindowMs
	return l
}

// Name is the limiter's name.
func (l *Limiter) Name() string { return l.cfg.Name }

// Max is the number of requests a window allows.
func (l *Limiter) Max() int64 { return l.cfg.Max }

// Window is the length of a window.
func (l *Limiter) Window() time.Duration { return time.Duration(l.cfg.WindowMs) * time.Millisecond }

func (l *Limiter) nowMs() int64 { return l.cfg.Now().UnixMilli() }

// hit counts one request of key and returns the count and the window's
// end.
func (l *Limiter) hit(key string, now int64) (hits, reset int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rotate(now)
	c, ok := l.current[key]
	if !ok {
		if c, ok = l.previous[key]; ok {
			delete(l.previous, key)
		} else {
			c = &client{reset: now + l.cfg.WindowMs}
		}
		l.current[key] = c
	}
	if c.reset <= now {
		c.hits = 0
		c.reset = now + l.cfg.WindowMs
	}
	c.hits++
	return c.hits, c.reset
}

// rotate ages the keys: once per window the current keys become the
// previous ones and the previous ones are dropped. A key that is seen
// moves back to the current ones, so only idle keys go.
func (l *Limiter) rotate(now int64) {
	if l.cfg.WindowMs <= 0 {
		// Such a window ends before it starts: nothing is worth keeping
		l.previous = map[string]*client{}
		l.current = map[string]*client{}
		return
	}
	if now < l.rotateAt {
		return
	}
	passed := (now-l.rotateAt)/l.cfg.WindowMs + 1
	l.previous = l.current
	l.current = map[string]*client{}
	if passed > 1 {
		l.previous = map[string]*client{}
	}
	l.rotateAt += passed * l.cfg.WindowMs
}

// ceilSeconds turns milliseconds into seconds, rounded up.
func ceilSeconds(ms int64) int64 {
	if ms > 0 {
		return (ms + 999) / 1000
	}
	return ms / 1000
}

// countedKey is the context key of the limiters that counted a request
// (countedBy), so that each counts it once, however many of its mounts
// and routes the request passes.
type countedKey struct{}

type countedBy struct{ limiters []*Limiter }

// countOnce returns r marked for counting each limiter once.
func countOnce(r *http.Request) *http.Request {
	if _, ok := r.Context().Value(countedKey{}).(*countedBy); ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), countedKey{}, &countedBy{}))
}

// allow counts the request and sets the RateLimit headers. When the
// request is over the limit it writes the 429 answer and returns false. A
// request this limiter counted before passes uncounted.
func (l *Limiter) allow(w http.ResponseWriter, r *http.Request, apiPath string) bool {
	if l.cfg.Skip != nil && l.cfg.Skip(r, apiPath) {
		return true
	}
	if by, ok := r.Context().Value(countedKey{}).(*countedBy); ok {
		if slices.Contains(by.limiters, l) {
			return true
		}
		by.limiters = append(by.limiters, l)
	}
	now := l.nowMs()
	hits, reset := l.hit(l.cfg.Key(r), now)

	limit := l.cfg.Max
	remaining := limit - hits
	if remaining < 0 {
		remaining = 0
	}
	resetSeconds := ceilSeconds(reset - now)
	if resetSeconds < 0 {
		resetSeconds = 0
	}
	h := w.Header()
	h.Set("RateLimit-Policy", strconv.FormatInt(limit, 10)+";w="+strconv.FormatInt(ceilSeconds(l.cfg.WindowMs), 10))
	h.Set("RateLimit-Limit", strconv.FormatInt(limit, 10))
	h.Set("RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	if resetSeconds != 0 {
		h.Set("RateLimit-Reset", strconv.FormatInt(resetSeconds, 10))
	}
	refused := hits > limit
	if l.counted != nil {
		l.counted(r, l.cfg.Name, hits, limit, refused)
	}
	if !refused {
		return true
	}
	h.Set("Retry-After", strconv.FormatInt(resetSeconds, 10))
	httpx.WriteJSON(w, r, http.StatusTooManyRequests, l.cfg.Message)
	return false
}

// Middleware puts the limiter in front of a route.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := httpx.RawPath(r)
		if l.allow(w, r, resolveAPIPath("", raw, raw)) {
			next.ServeHTTP(w, r)
		}
	})
}

// resolveAPIPath is the request's path in its /api/... form: base is the
// mount point the limiter sits under as the client spelled it, sub the
// path below it, original the whole path. The comparison is by letter
// case, so a limiter under /API does not see /api paths.
func resolveAPIPath(base, sub, original string) string {
	isAPI := func(p string) bool { return p == "/api" || strings.HasPrefix(p, "/api/") }
	switch {
	case isAPI(base):
		return "/api" + sub
	case isAPI(sub):
		return sub
	case isAPI(original):
		return original
	}
	return sub
}

// parseIntOr is parseInt(s, 10) || fallback, which is how the console
// reads its numeric settings: leading spaces and a sign are fine, digits
// are read up to the first other letter, and a value that is not a number
// or is zero gives the fallback.
func parseIntOr(s string, fallback int64) int64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	const ceiling = int64(1) << 53
	var n int64
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		if n < ceiling {
			n = n*10 + int64(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 || n == 0 {
		return fallback
	}
	if negative {
		n = -n
	}
	return n
}
