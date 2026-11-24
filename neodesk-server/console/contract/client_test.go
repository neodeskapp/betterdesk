package contract

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeConsole answers like the console does where the client depends on it:
// the sign-in page's data and the bootstrap hand out a CSRF token with its
// cookie, sign-in sets a session cookie, writes need the token.
type fakeConsole struct {
	t        *testing.T
	password string
	// seen is every request the console got: "METHOD path".
	seen []*http.Request
	// bodies holds the body of each request in seen.
	bodies []string
	// routingAsked counts the questions to the routing report.
	routingAsked int
	// mu guards seen and bodies: a request that timed out on the client can
	// still be running when the next one comes.
	mu sync.Mutex
}

// lastBody is the body of the request that came last.
func (f *fakeConsole) lastBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[len(f.bodies)-1]
}

func (f *fakeConsole) handler() http.Handler {
	mux := http.NewServeMux()
	token := func(w http.ResponseWriter, r *http.Request) string {
		if c, err := r.Cookie("__csrf_h"); err == nil {
			return "token-for-" + c.Value
		}
		http.SetCookie(w, &http.Cookie{Name: "__csrf_h", Value: "c1", Path: "/", HttpOnly: true})
		return "token-for-c1"
	}
	signedIn := func(r *http.Request) bool {
		c, err := r.Cookie("nd.sid")
		return err == nil && strings.HasPrefix(c.Value, "session-of-")
	}
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /api/auth/login-page", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"success": true, "csrfToken": token(w, r)})
	})
	mux.HandleFunc("GET /api/console/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(r) {
			writeJSON(w, 401, map[string]any{"success": false})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "csrfToken": token(w, r)})
	})
	mux.HandleFunc("GET /api/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(r) {
			writeJSON(w, 401, map[string]any{"success": false})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		_ = json.Unmarshal([]byte(f.lastBody()), &body)
		c, _ := r.Cookie("__csrf_h")
		switch {
		case c == nil || r.Header.Get("X-CSRF-Token") != "token-for-"+c.Value:
			writeJSON(w, 403, map[string]any{"success": false, "error": "Invalid CSRF token"})
		case body.Password != f.password:
			writeJSON(w, 401, map[string]any{"success": false})
		case body.Username == "e2e_pro":
			writeJSON(w, 403, map[string]any{"success": false, "error": "pro only"})
		default:
			http.SetCookie(w, &http.Cookie{Name: "nd.sid", Value: "session-of-" + body.Username, Path: "/", HttpOnly: true})
			writeJSON(w, 200, map[string]any{"success": true, "user": map[string]any{"username": body.Username}})
		}
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "nd.sid", Value: "", Path: "/", Expires: time.Unix(0, 0)})
		writeJSON(w, 200, map[string]any{"success": true})
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"method": r.Method, "query": r.URL.RawQuery, "cookie": r.Header.Get("Cookie"), "csrf": r.Header.Get("X-CSRF-Token"),
			"accept": r.Header.Get("Accept"), "contentType": r.Header.Get("Content-Type"), "agent": r.Header.Get("User-Agent"),
			"encoding": strings.Join(r.Header.Values("Accept-Encoding"), ","), "language": r.Header.Get("Accept-Language"), "extra": r.Header.Get("X-Extra"),
			"body": f.lastBody(), "host": r.Host,
		})
	})
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"accept": r.Header.Get("Accept")})
	})
	mux.HandleFunc("/rotate-csrf", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "__csrf_h", Value: "c2", Path: "/"})
		writeJSON(w, 200, map[string]any{})
	})
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte(`{"compressed":true}`))
		_ = zw.Close()
	})
	mux.HandleFunc("/brotli", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write([]byte{0x0b, 0x02, 0x80})
	})
	mux.HandleFunc("/hop1", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/hop2?x=1", http.StatusFound) })
	mux.HandleFunc("/hop2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/echo?from=hop2", http.StatusSeeOther)
	})
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/x", http.StatusFound)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok"}) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, r)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}

// newTestClient starts a fake console and returns a client for it. routing
// adds the routing report, with /api/ported answered by Go.
func newTestClient(t *testing.T, routing bool) (*Client, *fakeConsole) {
	t.Helper()
	fake := &fakeConsole{t: t, password: "the-password"}
	h := fake.handler()
	if routing {
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/__nd/routing" {
				inner.ServeHTTP(w, r)
				return
			}
			fake.routingAsked++
			handler := "node"
			if r.URL.Query().Get("path") == "/api/ported" && r.URL.Query().Get("method") == "GET" {
				handler = "go"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"handler": handler})
		})
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	passwordFile := filepath.Join(t.TempDir(), "e2e_password")
	if err := os.WriteFile(passwordFile, []byte("the-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(srv.URL)
	client, err := NewClient(&Settings{BaseURL: base, PasswordFile: passwordFile, Timeout: 700 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return client, fake
}

func echo(t *testing.T, a Answer) map[string]string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal(a.Body.JSON, &out); err != nil {
		t.Fatalf("answer is not the echo: %+v", a)
	}
	return out
}

func TestSignInTheWayTheWebAppDoes(t *testing.T) {
	client, fake := newTestClient(t, false)
	sess := client.NewSession("admin")
	answer, err := sess.SignIn()
	if err != nil {
		t.Fatal(err)
	}
	if answer == nil || answer.Status != 200 || !sess.SignedIn() {
		t.Fatalf("sign-in answer %+v, signed in %v", answer, sess.SignedIn())
	}
	var order []string
	for _, r := range fake.seen {
		order = append(order, r.Method+" "+r.URL.Path)
	}
	want := "GET /api/auth/login-page, POST /api/auth/login, GET /api/console/bootstrap"
	if got := strings.Join(order, ", "); got != want {
		t.Errorf("requests: %s\n    want: %s", got, want)
	}
	if got := answer.Headers["set-cookie"]; len(got) != 1 || got[0] != "nd.sid=<value>; Path=/; HttpOnly" {
		t.Errorf("the sign-in answer holds the cookie as %q, want it without its value", got)
	}
	if !sess.Alive() {
		t.Error("Alive() is false right after sign-in")
	}

	got := echo(t, sess.Do(Request{Method: "POST", Path: "/echo?a=1", Body: []byte(`{"x":1}`), ContentType: "application/json"}))
	if got["cookie"] != "__csrf_h=c1; nd.sid=session-of-e2e_admin" || got["csrf"] != "token-for-c1" {
		t.Errorf("a write went out with cookie %q and token %q", got["cookie"], got["csrf"])
	}
	if got["body"] != `{"x":1}` || got["contentType"] != "application/json" || got["query"] != "a=1" || got["method"] != "POST" {
		t.Errorf("the write arrived as %v", got)
	}
	if got["agent"] != userAgent || got["encoding"] != "gzip" || got["accept"] != acceptPage || got["language"] == "" {
		t.Errorf("default headers arrived as %v", got)
	}
}

func TestRoleThatMayNotSignInStaysSignedOut(t *testing.T) {
	client, _ := newTestClient(t, false)
	sess := client.NewSession("pro")
	answer, err := sess.SignIn()
	if err != nil {
		t.Fatal(err)
	}
	if answer == nil || answer.Status != 403 || sess.SignedIn() || sess.Alive() {
		t.Errorf("answer %+v, signed in %v: want the refusal recorded and no session", answer, sess.SignedIn())
	}
	got := echo(t, sess.Do(Request{Method: "POST", Path: "/echo"}))
	if got["cookie"] != "__csrf_h=c1" || got["csrf"] != "token-for-c1" {
		t.Errorf("after the refusal a write goes out with cookie %q and token %q", got["cookie"], got["csrf"])
	}

	out := client.NewSession(SignedOut)
	if answer, err := out.SignIn(); err != nil || answer != nil || out.SignedIn() {
		t.Errorf("signed out: answer %v, err %v", answer, err)
	}
}

func TestRequestOptions(t *testing.T) {
	client, _ := newTestClient(t, false)
	sess := client.NewSession("viewer")
	if _, err := sess.SignIn(); err != nil {
		t.Fatal(err)
	}

	got := echo(t, sess.Do(Request{Method: "GET", Path: "/echo"}))
	if got["csrf"] != "" {
		t.Errorf("a read went out with the CSRF token %q", got["csrf"])
	}
	got = echo(t, sess.Do(Request{Method: "DELETE", Path: "/echo", NoCSRF: true}))
	if got["csrf"] != "" || got["cookie"] == "" {
		t.Errorf("NoCSRF: token %q, cookie %q", got["csrf"], got["cookie"])
	}
	got = echo(t, sess.Do(Request{Method: "POST", Path: "/echo", NoSession: true}))
	if got["cookie"] != "__csrf_h=c1" || got["csrf"] != "token-for-c1" {
		t.Errorf("NoSession: cookie %q, token %q; want the CSRF cookie and token without the session", got["cookie"], got["csrf"])
	}
	got = echo(t, sess.Do(Request{Method: "POST", Path: "/echo", NoCookies: true}))
	if got["cookie"] != "" {
		t.Errorf("NoCookies: cookie %q", got["cookie"])
	}
	got = echo(t, sess.Do(Request{Method: "GET", Path: "/echo", Header: http.Header{"Accept": {"text/plain"}, "X-Extra": {"1"}}}))
	if got["accept"] != "text/plain" || got["extra"] != "1" {
		t.Errorf("case headers: accept %q, extra %q", got["accept"], got["extra"])
	}
	// A case that asks for no encoding gets none: nothing adds gzip behind its back.
	got = echo(t, sess.Do(Request{Method: "GET", Path: "/echo", Header: http.Header{"Accept-Encoding": {""}}}))
	if got["encoding"] != "" {
		t.Errorf("a request with an empty Accept-Encoding arrived with %q", got["encoding"])
	}
	var api map[string]string
	_ = json.Unmarshal(sess.Do(Request{Method: "GET", Path: "/api/echo"}).Body.JSON, &api)
	if api["accept"] != acceptAPI {
		t.Errorf("an API path is asked with Accept %q, want %q", api["accept"], acceptAPI)
	}
}

func TestSessionFollowsTheCookiesOfTheConsole(t *testing.T) {
	client, _ := newTestClient(t, false)
	sess := client.NewSession("operator")
	if _, err := sess.SignIn(); err != nil {
		t.Fatal(err)
	}

	// A request without the session cookie must not touch the browser's cookies.
	sess.Do(Request{Method: "POST", Path: "/api/auth/logout", NoSession: true})
	if !sess.Alive() {
		t.Fatal("a logout sent without the session cookie ended the session")
	}

	// The console replaces the CSRF cookie: the next write uses a token for the new one.
	sess.Do(Request{Method: "GET", Path: "/rotate-csrf"})
	got := echo(t, sess.Do(Request{Method: "POST", Path: "/echo"}))
	if got["csrf"] != "token-for-c2" || !strings.Contains(got["cookie"], "__csrf_h=c2") {
		t.Errorf("after the console replaced the CSRF cookie: token %q, cookie %q", got["csrf"], got["cookie"])
	}

	answer := sess.Do(Request{Method: "POST", Path: "/api/auth/logout"})
	if got := answer.Headers["set-cookie"]; len(got) != 1 || got[0] != "nd.sid=; Path=/; Expires=<past>" {
		t.Errorf("logout cookie = %q", got)
	}
	if sess.cookie(sessionCookies) != "" || sess.Alive() {
		t.Error("the session cookie is still there after the console cleared it")
	}
}

func TestEncodings(t *testing.T) {
	client, _ := newTestClient(t, false)
	sess := client.NewSession(SignedOut)

	a := sess.Do(Request{Method: "GET", Path: "/gzip"})
	if a.Body.Kind != BodyJSON || string(a.Body.JSON) != `{"compressed":true}` {
		t.Errorf("gzip body = %+v, want it unpacked", a.Body)
	}
	if got := a.Headers["content-encoding"]; len(got) != 1 || got[0] != "gzip" {
		t.Errorf("content-encoding = %q, want gzip kept in the answer", got)
	}
	a = sess.Do(Request{Method: "GET", Path: "/brotli"})
	if a.Body.Kind != BodyEncoded || a.Status != 200 {
		t.Errorf("brotli body = %+v, want it marked as not readable", a.Body)
	}
}

func TestRedirects(t *testing.T) {
	client, _ := newTestClient(t, false)
	sess := client.NewSession(SignedOut)

	a := sess.Do(Request{Method: "GET", Path: "/hop1"})
	if a.Status != 302 || a.Headers["location"][0] != "/hop2?x=1" || len(a.Redirects) != 0 {
		t.Errorf("without FollowRedirects: %+v, want the redirect itself", a)
	}
	a = sess.Do(Request{Method: "GET", Path: "/hop1", FollowRedirects: true})
	if a.Status != 200 || len(a.Redirects) != 2 || a.Redirects[0] != "/hop2?x=1" || a.Redirects[1] != "<base>/echo?from=hop2" {
		t.Fatalf("with FollowRedirects: status %d, redirects %q", a.Status, a.Redirects)
	}
	if got := echo(t, a); got["query"] != "from=hop2" {
		t.Errorf("the last answer is %v", got)
	}
	a = sess.Do(Request{Method: "GET", Path: "/away", FollowRedirects: true})
	if a.Status != 302 || a.Headers["location"][0] != "https://elsewhere.example/x" {
		t.Errorf("a redirect to another site was followed: %+v", a)
	}
}

func TestNoAnswer(t *testing.T) {
	client, _ := newTestClient(t, false)
	sess := client.NewSession(SignedOut)

	start := time.Now()
	a := sess.Do(Request{Method: "GET", Path: "/slow"})
	if a.Failed != "timeout" || a.Status != 0 || time.Since(start) > 3*time.Second {
		t.Errorf("slow route: %+v after %s, want a timeout after the client's limit", a, time.Since(start))
	}
	start = time.Now()
	a = sess.Do(Request{Method: "GET", Path: "/events"})
	if a.Failed != "" || a.Status != 200 || a.Body.Kind != BodyStream || time.Since(start) > 500*time.Millisecond {
		t.Errorf("event stream: %+v after %s, want the headers without waiting for the body", a, time.Since(start))
	}
}

func TestHandlerAsksTheRoutingReport(t *testing.T) {
	client, fake := newTestClient(t, true)
	if !client.HasRouting() {
		t.Fatal("HasRouting() = false for a console with the report")
	}
	if got := client.Handler("GET", "/api/ported"); got != HandlerGo {
		t.Errorf("Handler(GET /api/ported) = %q, want go", got)
	}
	if got := client.Handler("POST", "/api/ported"); got != HandlerNode {
		t.Errorf("Handler(POST /api/ported) = %q, want node", got)
	}
	before := fake.routingAsked
	if got := client.Handler("GET", "/api/ported"); got != HandlerGo || fake.routingAsked != before {
		t.Errorf("asked again: handler %q after %d more questions, want the remembered answer", got, fake.routingAsked-before)
	}

	plain, _ := newTestClient(t, false)
	if plain.HasRouting() || plain.Handler("GET", "/api/ported") != HandlerUnknown {
		t.Error("a console without the report: want HasRouting false and an unknown handler")
	}
}

func TestClientNeedsThePasswordFile(t *testing.T) {
	base, _ := url.Parse("http://127.0.0.1:1")
	if _, err := NewClient(&Settings{BaseURL: base, PasswordFile: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Error("NewClient worked without a password file")
	}
}
