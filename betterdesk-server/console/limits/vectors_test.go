package limits

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The vectors in testdata were recorded by
// .ui-rework/go/tools/vectors/limits.js from the Node console's own
// limiters (middleware/rateLimiter.js and middleware/wanSecurity.js on
// express-rate-limit 7.5.1), mounted the way server.js mounts them, with a
// clock the script moves by hand. "at" is that clock in milliseconds.

type step struct {
	ID      string      `json:"id"`
	At      int64       `json:"at"`
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Headers [][2]string `json:"headers"`
	Body    *string     `json:"body"`
	Fill    *struct {
		Fill      int `json:"fill"`
		ChunkSize int `json:"chunkSize"`
	} `json:"fill"`
	Answer struct {
		Status  int         `json:"status"`
		Headers [][2]string `json:"headers"`
		Body    string      `json:"body"`
	} `json:"answer"`
	Trace [][]any `json:"trace"`
}

type consoleVectors struct {
	Base      int64 `json:"base"`
	Scenarios []struct {
		Name  string            `json:"name"`
		Env   map[string]string `json:"env"`
		Steps []step            `json:"steps"`
	} `json:"scenarios"`
}

func loadJSON(t testing.TB, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

// request builds the step's request as a handler sees it behind the
// console's listener: the socket is the local proxy the script used.
func (s step) request() *http.Request {
	r := &http.Request{Method: s.Method, RequestURI: s.Path, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Host: "vectors.test", RemoteAddr: "127.0.0.1:40000", Body: http.NoBody}
	if u, err := url.ParseRequestURI(s.Path); err == nil {
		r.URL = u
	} else {
		r.URL = &url.URL{Path: "/"}
	}
	for _, h := range s.Headers {
		r.Header.Add(h[0], h[1])
	}
	var body []byte
	switch {
	case s.Body != nil:
		body, _ = base64.StdEncoding.DecodeString(*s.Body)
	case s.Fill != nil:
		body = bytes.Repeat([]byte("a"), s.Fill.Fill)
	default:
		return r
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if s.Fill != nil && s.Fill.ChunkSize > 0 {
		r.ContentLength = -1
		r.TransferEncoding = []string{"chunked"}
	} else {
		r.Header.Set("Content-Length", fmt.Sprint(len(body)))
	}
	return r
}

func headersOf(pairs [][2]string, keep func(name string) bool) map[string]string {
	out := map[string]string{}
	for _, h := range pairs {
		name := strings.ToLower(h[0])
		if keep(name) {
			out[name] = h[1]
		}
	}
	return out
}

func recorded(h http.Header, keep func(name string) bool) map[string]string {
	out := map[string]string{}
	for name, values := range h {
		if lower := strings.ToLower(name); keep(lower) {
			out[lower] = strings.Join(values, ", ")
		}
	}
	return out
}

func diff(want, got map[string]string) string {
	var lines []string
	for name, w := range want {
		if g, ok := got[name]; !ok {
			lines = append(lines, "missing "+name+": "+w)
		} else if g != w {
			lines = append(lines, fmt.Sprintf("%s: got %q want %q", name, g, w))
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			lines = append(lines, "extra "+name+": "+g)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ")
}

var loopback, _ = httpx.ParseTrustProxy("loopback")

// testUser is the fake session of the recording app: a header names the
// signed-in user.
func testUser(r *http.Request) string {
	if r.Header.Get("X-Test-No-Session") != "" {
		return ""
	}
	if user := r.Header.Get("X-Test-User"); user != "" {
		return user
	}
	return r.Header.Get("X-Test-User-Obj")
}

var (
	avatarRoute = regexp.MustCompile(`^/api/users/[^/]+/avatar$`)
	fileRoute   = regexp.MustCompile(`^/api/files/[^/]+$`)
	remoteRoute = regexp.MustCompile(`^/remote/[^/]+$`)
)

// sampleRoutes are the routes of the recording app with the limiters the
// console's route files put on them.
func sampleRoutes(set *Set) http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, struct {
			OK bool `json:"ok"`
		}{true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.ToLower(httpx.RawPath(r))
		if len(route) > 1 {
			route = strings.TrimSuffix(route, "/")
		}
		get := r.Method == http.MethodGet || r.Method == http.MethodHead
		post := r.Method == http.MethodPost
		var limiter *Limiter
		switch {
		case post && (route == "/api/auth/login" || route == "/api/auth/totp/verify"):
			limiter = set.Login
		case post && (route == "/api/auth/password" || route == "/api/users"):
			limiter = set.PasswordChange
		case get && (route == "/api/users/avatars" || avatarRoute.MatchString(route)):
			limiter = set.PanelRead
		case get && remoteRoute.MatchString(route):
			limiter = set.RDClientPage
		case post && route == "/api/files/upload":
			limiter = set.Upload
		case get && fileRoute.MatchString(route):
			limiter = set.FileAccess
		case get && route == "/api/logs/recent":
			limiter = set.API
		case post && route == "/api/bd/register":
			limiter = set.Register
		}
		if limiter != nil {
			limiter.Middleware(ok).ServeHTTP(w, r)
			return
		}
		ok.ServeHTTP(w, r)
	})
}

func limitHeader(name string) bool {
	return strings.HasPrefix(name, "ratelimit") || name == "retry-after" || name == "content-type" || name == "content-length" || name == "etag"
}

func TestConsoleLimitersAgainstNode(t *testing.T) {
	var file consoleVectors
	loadJSON(t, "console.json", &file)
	total := 0
	for _, scenario := range file.Scenarios {
		now := file.Base
		var trace [][]any
		set := NewSet(Options{
			ClientIP: loopback.ClientIP,
			UserKey:  testUser,
			Getenv:   func(name string) string { return scenario.Env[name] },
			Now:      func() time.Time { return time.UnixMilli(now) },
		})
		for _, l := range set.All() {
			l.counted = func(_ *http.Request, name string, hits, limit int64, refused bool) {
				outcome := "passed"
				if refused {
					outcome = "refused"
				}
				trace = append(trace, []any{name, float64(hits), float64(limit), outcome})
			}
		}
		handler := set.AppLevel()(sampleRoutes(set))
		for _, s := range scenario.Steps {
			total++
			now = file.Base + s.At
			trace = nil
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, s.request())
			name := fmt.Sprintf("%s: %s at %d %s %s", scenario.Name, s.ID, s.At, s.Method, s.Path)

			if rec.Code != s.Answer.Status {
				t.Errorf("%s: status %d, want %d", name, rec.Code, s.Answer.Status)
			}
			if d := diff(headersOf(s.Answer.Headers, limitHeader), recorded(rec.Header(), limitHeader)); d != "" {
				t.Errorf("%s: headers: %s", name, d)
			}
			wantBody, _ := base64.StdEncoding.DecodeString(s.Answer.Body)
			if s.Method != http.MethodHead && rec.Body.String() != string(wantBody) {
				t.Errorf("%s: body %q, want %q", name, rec.Body.String(), wantBody)
			}
			if fmt.Sprint(trace) != fmt.Sprint(s.Trace) {
				t.Errorf("%s: counted %v, want %v", name, trace, s.Trace)
			}
		}
	}
	if total < 900 {
		t.Errorf("%d steps replayed, expected the recorded 1023", total)
	}
}

type wanVectors struct {
	Base  int64  `json:"base"`
	Steps []step `json:"steps"`
}

func TestClientAPIStackAgainstNode(t *testing.T) {
	var file wanVectors
	loadJSON(t, "wan.json", &file)
	if len(file.Steps) < 600 {
		t.Fatalf("%d steps, expected the recorded 687", len(file.Steps))
	}
	now := file.Base
	stack := WANStack(WANOptions{
		ClientIP: loopback.ClientIP,
		Now:      func() time.Time { return time.UnixMilli(now) },
		Log:      func(bool, string) {},
	})
	handler := stack(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		httpx.WriteJSON(w, r, http.StatusOK, struct {
			Reached bool   `json:"reached"`
			Method  string `json:"method"`
			Path    string `json:"path"`
			Bytes   int    `json:"bytes"`
		}{true, r.Method, httpx.RawPath(r), len(body)})
	}))
	keep := func(name string) bool {
		switch name {
		case "date", "connection", "keep-alive", "transfer-encoding":
			return false
		}
		return true
	}
	for _, s := range file.Steps {
		now = file.Base + s.At
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, s.request())
		name := fmt.Sprintf("%s at %d %s %s", s.ID, s.At, s.Method, s.Path)
		if rec.Code != s.Answer.Status {
			t.Errorf("%s: status %d, want %d", name, rec.Code, s.Answer.Status)
			continue
		}
		wantBody, _ := base64.StdEncoding.DecodeString(s.Answer.Body)
		if s.Method != http.MethodHead && rec.Body.String() != string(wantBody) {
			t.Errorf("%s: body %q, want %q", name, rec.Body.String(), wantBody)
		}
		want, got := headersOf(s.Answer.Headers, keep), recorded(rec.Header(), keep)
		if len(wantBody) == 0 {
			// An answer without a body: the length header is the server's
			delete(want, "content-length")
			delete(got, "content-length")
		}
		if d := diff(want, got); d != "" {
			t.Errorf("%s: headers: %s", name, d)
		}
	}
}
