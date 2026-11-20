package limits

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/logging"
)

// The client API port's connection timeouts. The listener's http.Server
// gets them: ReadHeaderTimeout and ReadTimeout from WANRequestTimeout,
// IdleTimeout from WANKeepAliveTimeout (package listen).
const (
	WANRequestTimeout   = 10 * time.Second
	WANKeepAliveTimeout = 5 * time.Second
)

// wanMethods says which methods a path of the client API port takes: "*"
// for any, else the listed ones.
var wanMethods = map[string][]string{
	"/api/login":                         {"POST"},
	"/api/logout":                        {"POST"},
	"/api/currentUser":                   {"GET", "POST"},
	"/api/login-options":                 {"GET"},
	"/api/oidc/auth":                     {"POST"},
	"/api/oidc/auth-query":               {"GET"},
	"/api/oidc/callback":                 {"GET"},
	"/api/auth/oidc/callback":            {"GET"},
	"/api/heartbeat":                     {"POST"},
	"/api/sysinfo":                       {"POST"},
	"/api/sysinfo_ver":                   {"POST"},
	"/api/telemetry/key":                 {"GET"},
	"/api/branding":                      {"GET"},
	"/api/devices/register":              {"POST"},
	"/api/devices/register/status":       {"GET"},
	"/api/pairing/redeem":                {"POST"},
	"/api/pairing/config":                {"POST"},
	"/api/peers":                         {"GET"},
	"/api/audit":                         {"GET"},
	"/api/audit/conn":                    {"*"},
	"/api/audit/file":                    {"*"},
	"/api/audit/alarm":                   {"*"},
	"/api/device-group":                  {"*"},
	"/api/device-group/accessible":       {"GET"},
	"/api/user/group":                    {"GET"},
	"/api/user-groups":                   {"*"},
	"/api/strategies":                    {"*"},
	"/api/strategies/assign":             {"POST"},
	"/api/devices":                       {"GET"},
	"/api/ab":                            {"*"},
	"/api/ab/personal":                   {"*"},
	"/api/ab/tags":                       {"GET"},
	"/api/users":                         {"GET"},
	"/api/software":                      {"GET"},
	"/api/software/client-download-link": {"GET"},
	"/api/health":                        {"GET"},
	"/api/server/pubkey":                 {"GET"},
	"/api/server-key":                    {"GET"},
	"/api/server-key/fingerprint":        {"GET"},
	"/api/group":                         {"*"},
	"/api/group/get":                     {"*"},
	"/api/peers/list":                    {"*"},
	"/api/bd/register-request":           {"POST"},
	"/api/bd/register-status":            {"GET"},
}

// wanPatterns are the paths with a parameter. They take GET only.
var wanPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^/api/peer-key/[a-zA-Z0-9_-]{1,32}$`),
	regexp.MustCompile(`(?i)^/api/strategies/[0-9a-f-]{36}$`),
	regexp.MustCompile(`(?i)^/api/strategies/[0-9a-f-]{36}/status$`),
	regexp.MustCompile(`(?i)^/api/devices/[0-9a-f-]{36}/assign$`),
}

// wanBodyLimits are the largest request bodies in bytes, by path. Other
// paths have wanDefaultBodyLimit.
var wanBodyLimits = map[string]int64{
	"/api/login":          4096,
	"/api/heartbeat":      8192,
	"/api/oidc/auth":      4096,
	"/api/sysinfo":        8192,
	"/api/sysinfo_ver":    512,
	"/api/pairing/redeem": 4096,
	"/api/pairing/config": 512,
	"/api/ab":             524288,
	"/api/ab/personal":    524288,
	"/api/audit/conn":     2048,
	"/api/audit/file":     4096,
	"/api/audit/alarm":    2048,
	"/api/device-group":   2048,
	"/api/user-groups":    2048,
	"/api/strategies":     4096,
	"/api/group":          2048,
	"/api/group/get":      2048,
	"/api/peers/list":     2048,
}

const wanDefaultBodyLimit = 1024

// WANOptions configure the client API port's stack.
type WANOptions struct {
	// ClientIP is the client's address (req.ip under TRUST_PROXY).
	ClientIP func(r *http.Request) string
	// Now is the clock of the limiters. nil means time.Now.
	Now func() time.Time
	// Timeout is how long a request may take to be answered. Zero means
	// WANRequestTimeout.
	Timeout time.Duration
	// Log gets one line per request; warn is true for a status of 400 and
	// above. nil writes through the logging package.
	Log func(warn bool, line string)
	// GatewayTimeout answers a request that arrived whole and is not
	// answered in time with 504 {"error":"Gateway Timeout"}, for a port
	// that passes requests on and waits for the answer. Otherwise such a
	// request is closed without an answer.
	GatewayTimeout bool
}

type wanMessage struct {
	Error string `json:"error"`
}

// WANStack is what every request on the client API port passes, in this
// order:
//
//  1. A timeout: a request not answered in time gets 408 while its body
//     is still arriving, and once it has arrived whole is closed without
//     an answer, or gets 504 with GatewayTimeout.
//  2. Headers: no caching, no framing, no content types to sniff, and the
//     CORS headers for GET and POST. OPTIONS ends here with 204.
//  3. A log line when the answer is out.
//  4. The path list: 404 for a path that is not on it, 405 for a method
//     the path does not take. Both without a body.
//  5. Limiters per address and minute: 60 for everything, 20 for POST
//     under /api/audit/, 5 for /api/login and /api/logout.
//  6. POST needs the media type application/json (any letter case) in
//     its Content-Type, or it gets 415.
//  7. The body limit: 1024 bytes, or the path's own. A larger body gets
//     413 and the connection is closed. The body is read here and handed
//     on whole. A body that stops before its end gets 408 when the
//     listener's read deadline passed, 400 otherwise.
func WANStack(opts WANOptions) func(http.Handler) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Timeout <= 0 {
		opts.Timeout = WANRequestTimeout
	}
	if opts.ClientIP == nil {
		opts.ClientIP = httpx.SocketIP
	}
	if opts.Log == nil {
		opts.Log = func(warn bool, line string) {
			if warn {
				line = "WARN: " + line
			}
			logging.Logf("client-api", "%s", line)
		}
	}
	key := func(r *http.Request) string {
		if ip := opts.ClientIP(r); ip != "" {
			return ip
		}
		return "unknown"
	}
	const minute = 60 * 1000
	global := New(Config{Name: "wanGlobalLimiter", WindowMs: minute, Max: 60, Message: wanMessage{"Too many requests"}, Key: key, Now: opts.Now})
	audit := New(Config{Name: "wanAuditLimiter", WindowMs: minute, Max: 20, Message: wanMessage{"Audit rate limit exceeded"}, Key: key, Now: opts.Now,
		Skip: func(r *http.Request, _ string) bool {
			return !strings.HasPrefix(httpx.RawPath(r), "/api/audit/") || r.Method != http.MethodPost
		}})
	login := New(Config{Name: "wanLoginLimiter", WindowMs: minute, Max: 5, Message: wanMessage{"Too many requests, please try again later"}, Key: key, Now: opts.Now,
		Skip: func(r *http.Request, _ string) bool {
			p := httpx.RawPath(r)
			return p != "/api/login" && p != "/api/logout"
		}})

	return func(next http.Handler) http.Handler {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := httpx.RawPath(r)
			if status := wanPathStatus(p, r.Method); status != 0 {
				w.WriteHeader(status)
				return
			}
			for _, l := range []*Limiter{global, audit, login} {
				if !l.allow(w, r, p) {
					return
				}
			}
			if r.Method == http.MethodPost && !isJSON(r.Header.Get("Content-Type")) {
				httpx.WriteJSON(w, r, http.StatusUnsupportedMediaType, wanMessage{"Content-Type must be application/json"})
				return
			}
			limit, ok := wanBodyLimits[p]
			if !ok {
				limit = wanDefaultBodyLimit
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
			if err != nil {
				// The body did not arrive: in time (the listener's read
				// deadline) it is 408, else the connection broke or the
				// framing was wrong and it is 400; the connection closes
				w.Header().Set("Connection", "close")
				status := http.StatusBadRequest
				var timeout interface{ Timeout() bool }
				if errors.As(err, &timeout) && timeout.Timeout() {
					status = http.StatusRequestTimeout
				}
				w.WriteHeader(status)
				return
			}
			if int64(len(body)) > limit {
				w.Header().Set("Connection", "close")
				httpx.WriteJSON(w, r, http.StatusRequestEntityTooLarge, wanMessage{"Request too large"})
				return
			}
			if tw, ok := w.(*timeoutWriter); ok {
				tw.arrived()
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Del("X-Powered-By")
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
			h.Set("Pragma", "no-cache")
			h.Set("Expires", "0")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-XSS-Protection", "0")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), usb=()")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("Access-Control-Allow-Methods", "GET, POST")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			tw := &timeoutWriter{ResponseWriter: w, header: h.Clone(), gateway: opts.GatewayTimeout}
			start := opts.Now()
			status := 0
			defer func() {
				line := "[API:" + r.Method + "] " + httpx.RawPath(r) + " " + strconv.Itoa(status) + " " +
					strconv.FormatInt(opts.Now().Sub(start).Milliseconds(), 10) + "ms IP:" + wanLogAddress(r)
				opts.Log(status >= 400, line)
			}()

			// The rest runs beside this function, so the timeout can answer
			// and end the request while a handler is stuck.
			done := make(chan struct{})
			panicked := make(chan any, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicked <- p
						return
					}
					close(done)
				}()
				inner.ServeHTTP(tw, r)
			}()
			timer := time.NewTimer(opts.Timeout)
			defer timer.Stop()
			select {
			case <-done:
			case p := <-panicked:
				panic(p)
			case <-timer.C:
				switch what, code := tw.timeout(r); what {
				case timedOutAnswered:
					status = code
					return
				case timedOutDropped:
					// The request had arrived whole: the connection ends
					// without an answer
					panic(http.ErrAbortHandler)
				}
				// An answer is on its way out: let it finish
				select {
				case <-done:
				case p := <-panicked:
					panic(p)
				}
			}
			status = tw.finish()
		})
	}
}

// isJSON reports a Content-Type whose media type is application/json, in
// any letter case and with any parameters.
func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return (err == nil || errors.Is(err, mime.ErrInvalidMediaParameter)) && mediaType == "application/json"
}

// wanPathStatus is 0 for a path and method the client API port takes,
// else 404 or 405.
func wanPathStatus(p, method string) int {
	methods, listed := wanMethods[p]
	if !listed {
		for _, pattern := range wanPatterns {
			if pattern.MatchString(p) {
				if method != http.MethodGet && method != http.MethodOptions {
					return http.StatusMethodNotAllowed
				}
				return 0
			}
		}
		return http.StatusNotFound
	}
	for _, m := range methods {
		if m == "*" || m == method {
			return 0
		}
	}
	if method == http.MethodOptions {
		return 0
	}
	return http.StatusMethodNotAllowed
}

// wanLogAddress is the address in the request log: the first
// X-Forwarded-For entry, else X-Real-IP, else the socket. It is for the
// log only and trusts whatever the client sent.
func wanLogAddress(r *http.Request) string {
	if forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ", "); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if first = strings.TrimSpace(first); first != "" {
			return first
		}
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return real
	}
	if socket := httpx.SocketIP(r); socket != "" {
		return socket
	}
	return "unknown"
}

// timeoutWriter lets the timeout end a request from its timer while the
// handler is still at work: whoever writes the status first wins, and
// after the timeout the handler's writes go nowhere. The handler sets
// its headers on a map of its own, so the timer never reads a map the
// handler is writing.
type timeoutWriter struct {
	http.ResponseWriter
	header http.Header

	mu       sync.Mutex
	code     int
	timedOut bool
	finished bool
	// whole says the request, body included, has arrived
	whole bool
	// gateway answers a whole request 504 at the timeout
	gateway bool
}

// What the timeout did.
const (
	// timedOutLate: the handler had started its answer
	timedOutLate = iota
	// timedOutAnswered: 408 or 504 went out
	timedOutAnswered
	// timedOutDropped: nothing went out, the connection is to be closed
	timedOutDropped
)

// arrived records that the request has arrived whole.
func (t *timeoutWriter) arrived() {
	t.mu.Lock()
	t.whole = true
	t.mu.Unlock()
}

func (t *timeoutWriter) Header() http.Header { return t.header }

// commit copies the handler's headers to the connection and writes the
// status. The caller holds the lock.
func (t *timeoutWriter) commit(code int) {
	t.code = code
	dst := t.ResponseWriter.Header()
	for name := range dst {
		delete(dst, name)
	}
	for name, values := range t.header {
		dst[name] = values
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *timeoutWriter) WriteHeader(code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut || t.code != 0 {
		return
	}
	t.commit(code)
}

func (t *timeoutWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if t.code == 0 {
		t.commit(http.StatusOK)
	}
	return t.ResponseWriter.Write(p)
}

// timeout ends the request unless the handler has started its answer, and
// says what it did and with which status: a request still arriving gets
// 408, as Node's request timeout answered; one that arrived whole gets 504
// on a port that waits for another server, else nothing, as Node destroyed
// the socket of a complete request that timed out.
func (t *timeoutWriter) timeout(r *http.Request) (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished || t.code != 0 {
		return timedOutLate, 0
	}
	t.timedOut = true
	switch {
	case t.whole && t.gateway:
		t.code = http.StatusGatewayTimeout
		httpx.WriteJSON(t.ResponseWriter, r, http.StatusGatewayTimeout, wanMessage{"Gateway Timeout"})
		return timedOutAnswered, t.code
	case t.whole:
		return timedOutDropped, 0
	}
	t.code = http.StatusRequestTimeout
	// The rest of the body is not waited for
	t.ResponseWriter.Header().Set("Connection", "close")
	t.ResponseWriter.WriteHeader(http.StatusRequestTimeout)
	return timedOutAnswered, t.code
}

// finish ends the handler's turn and returns the status that went out. A
// handler that wrote nothing answered 200.
func (t *timeoutWriter) finish() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.code == 0 {
		t.commit(http.StatusOK)
	}
	t.finished = true
	return t.code
}
