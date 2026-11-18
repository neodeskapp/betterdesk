package listen

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// listed is the client API port's path list, written out again here
// (middleware/wanSecurity.js), so the fuzz tests check the port against
// the list and not against itself.
var listed = map[string]string{
	"/api/login": "POST", "/api/logout": "POST", "/api/currentUser": "GET POST", "/api/login-options": "GET",
	"/api/oidc/auth": "POST", "/api/oidc/auth-query": "GET", "/api/oidc/callback": "GET", "/api/auth/oidc/callback": "GET",
	"/api/heartbeat": "POST", "/api/sysinfo": "POST", "/api/sysinfo_ver": "POST", "/api/telemetry/key": "GET",
	"/api/branding": "GET", "/api/devices/register": "POST", "/api/devices/register/status": "GET",
	"/api/pairing/redeem": "POST", "/api/pairing/config": "POST", "/api/peers": "GET", "/api/audit": "GET",
	"/api/audit/conn": "*", "/api/audit/file": "*", "/api/audit/alarm": "*", "/api/device-group": "*",
	"/api/device-group/accessible": "GET", "/api/user/group": "GET", "/api/user-groups": "*", "/api/strategies": "*",
	"/api/strategies/assign": "POST", "/api/devices": "GET", "/api/ab": "*", "/api/ab/personal": "*", "/api/ab/tags": "GET",
	"/api/users": "GET", "/api/software": "GET", "/api/software/client-download-link": "GET", "/api/health": "GET",
	"/api/server/pubkey": "GET", "/api/server-key": "GET", "/api/server-key/fingerprint": "GET", "/api/group": "*",
	"/api/group/get": "*", "/api/peers/list": "*", "/api/bd/register-request": "POST", "/api/bd/register-status": "GET",
}

var listedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^/api/peer-key/[a-zA-Z0-9_-]{1,32}$`),
	regexp.MustCompile(`(?i)^/api/strategies/[0-9a-f-]{36}$`),
	regexp.MustCompile(`(?i)^/api/strategies/[0-9a-f-]{36}/status$`),
	regexp.MustCompile(`(?i)^/api/devices/[0-9a-f-]{36}/assign$`),
}

// mayPass reports whether the list lets a method and path through.
func mayPass(method, path string) bool {
	if methods, ok := listed[path]; ok {
		return methods == "*" || strings.Contains(" "+methods+" ", " "+method+" ")
	}
	for _, p := range listedPatterns {
		if p.MatchString(path) {
			return method == "GET"
		}
	}
	return false
}

// recorder is an upstream that keeps what reached it.
type recorder struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newRecorder(t testing.TB, answer func(w http.ResponseWriter, r *http.Request)) *recorder {
	rec := &recorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.seen = append(rec.seen, r.Method+" "+r.RequestURI)
		rec.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (rec *recorder) take() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := rec.seen
	rec.seen = nil
	return out
}

// fuzzPort is a client API port for the fuzz tests, with a clock that
// moves a minute per request, so the rate limits never refuse.
func fuzzPort(t testing.TB, proxy bool, api, node *recorder) (*ClientAPI, func()) {
	var minutes atomic.Int64
	apiURL, _ := url.Parse(api.URL)
	nodeURL, _ := url.Parse(node.URL)
	c := NewClientAPI(ClientAPIOptions{
		Deps: &kit.Deps{}, Proxy: proxy, APIOrigin: apiURL, Node: nodeURL,
		Now: func() time.Time { return time.Unix(0, 0).Add(time.Duration(minutes.Load()) * time.Minute) },
	})
	return c, func() { minutes.Add(2) }
}

// serve answers a raw request the way the server would get it; false
// when net/http would not have read it.
func serve(h http.Handler, raw []byte) (*httptest.ResponseRecorder, *http.Request, bool) {
	r, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil || r.Method == http.MethodConnect {
		return nil, nil, false
	}
	r.RemoteAddr = "203.0.113.9:40000"
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != nil && !errors.Is(asError(p), http.ErrAbortHandler) {
				panic(p)
			}
		}()
		h.ServeHTTP(rec, r)
	}()
	return rec, r, true
}

func asError(p any) error {
	err, _ := p.(error)
	return err
}

var allowedStatus = map[int]bool{200: true, 204: true, 400: true, 404: true, 405: true, 408: true, 413: true, 415: true, 429: true, 500: true, 502: true}

// FuzzClientAPIPathList: whatever the request target, the server's API
// gets only listed paths with a method the list allows, as the client
// wrote them, and the Node console only the two registration requests,
// with their constant path.
func FuzzClientAPIPathList(f *testing.F) {
	for _, seed := range []string{
		"GET /api/health", "POST /api/login", "GET /api/peer-key/abc", "GET /api/strategies/0123abcd-0123-4567-89ab-0123456789ab",
		"POST /api/bd/register-request", "GET /api/bd/register-status?device_id=x", "GET /api/bd/../login",
		"GET /api/bd/register-status/../../x", "GET /api/bd%2Fregister-status", "GET //api/health", "GET /API/health",
		"GET /api/health/", "GET http://evil/api/health", "OPTIONS *", "GET /api/bd/register-status#x?y", "PUT /api/ab",
		"GET /api/health?x=%2F..%2F", "GET /api/bd/REGISTER-STATUS", "GET /api/bd/register-status;x", "GET /api/./health",
	} {
		method, target, _ := strings.Cut(seed, " ")
		f.Add(method, target, []byte("{}"))
	}
	api := newRecorder(f, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	node := newRecorder(f, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	proxyPort, tickProxy := fuzzPort(f, true, api, node)
	localPort, tickLocal := fuzzPort(f, false, api, node)
	f.Fuzz(func(t *testing.T, method, target string, body []byte) {
		raw := fmt.Appendf(nil, "%s %s HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", method, target, len(body))
		raw = append(raw, body...)
		for _, port := range []struct {
			c    *ClientAPI
			tick func()
		}{{proxyPort, tickProxy}, {localPort, tickLocal}} {
			port.tick()
			rec, r, ok := serve(port.c.Handler(), raw)
			if !ok {
				return
			}
			if rec.Code != 0 && !allowedStatus[rec.Code] {
				t.Fatalf("%s %q: status %d", method, target, rec.Code)
			}
			p := httpx.RawPath(r)
			for _, seen := range api.take() {
				m, uri, _ := strings.Cut(seen, " ")
				path, _, _ := strings.Cut(uri, "?")
				if m != r.Method || path != p || !mayPass(m, path) || strings.HasPrefix(path, "/api/bd/") {
					t.Fatalf("%s %q reached the API as %q", method, target, seen)
				}
			}
			for _, seen := range node.take() {
				m, uri, _ := strings.Cut(seen, " ")
				path, _, _ := strings.Cut(uri, "?")
				if registrationRoutes[path] != m || path != p {
					t.Fatalf("%s %q reached the Node console as %q", method, target, seen)
				}
			}
		}
	})
}

// validStrict is what express.json accepts: an object or an array, or
// no body at all (blanks alone are refused).
func validStrict(body []byte) bool {
	if len(body) == 0 {
		return true
	}
	text := bytes.Trim(body, " \t\n\r")
	return len(text) > 0 && (text[0] == '{' || text[0] == '[') && json.Valid(text)
}

// FuzzJSONGate: in local mode a JSON body that does not parse gets 400
// {"error":"Invalid JSON"}, one that parses goes on (here to the empty
// 404, as no routes are registered), and nothing else comes out.
func FuzzJSONGate(f *testing.F) {
	for _, seed := range []string{`{"a":1}`, `[]`, ``, `null`, `1`, `"x"`, `{bad`, `{"a":1}x`, ` {} `, `{"a":"\ud800"}`, `[1,2`, "\xef\xbb\xbf{}"} {
		f.Add([]byte(seed), uint8(0))
	}
	api := newRecorder(f, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	node := newRecorder(f, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	port, tick := fuzzPort(f, false, api, node)
	types := []string{"application/json", "application/json; charset=utf-8", "application/json; charset=latin1", "text/plain; a=application/json", "application/json-patch+json"}
	f.Fuzz(func(t *testing.T, body []byte, kind uint8) {
		contentType := types[int(kind)%len(types)]
		for _, path := range []string{"/api/login", "/api/ab"} {
			tick()
			raw := fmt.Appendf(nil, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n", path, contentType, len(body))
			rec, _, ok := serve(port.Handler(), append(raw, body...))
			if !ok {
				t.Fatal("the request did not parse")
			}
			out, _ := io.ReadAll(rec.Body)
			limit := 4096
			if path == "/api/ab" {
				limit = 524288
			}
			switch {
			case len(body) > limit:
				if rec.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("%d bytes on %s: %d", len(body), path, rec.Code)
				}
			case contentType == "application/json; charset=latin1":
				if rec.Code != http.StatusInternalServerError || string(out) != `{"error":"Server error"}` {
					t.Fatalf("latin1: %d %q", rec.Code, out)
				}
			case contentType == "text/plain; a=application/json" || contentType == "application/json-patch+json":
				// Not a type the parser reads: the body goes on unread
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s: %d %q", contentType, rec.Code, out)
				}
			case bytes.HasPrefix(body, []byte("\xef\xbb\xbf")):
				if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
					t.Fatalf("BOM: %d", rec.Code)
				}
			case validStrict(body):
				if rec.Code != http.StatusNotFound {
					t.Fatalf("valid %q: %d %q", body, rec.Code, out)
				}
			default:
				if rec.Code != http.StatusBadRequest || string(out) != `{"error":"Invalid JSON"}` {
					t.Fatalf("invalid %q: %d %q", body, rec.Code, out)
				}
			}
		}
		if seen := api.take(); len(seen) > 0 {
			t.Fatalf("local mode reached the API: %v", seen)
		}
	})
}
