package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// mixed is a console with the fake Node console behind it and a few Go
// routes: GET /api/ported/{id} guarded by sign-in, an unguarded probe for
// GET and POST, and a page route.
func mixed(t *testing.T, o setup) *world {
	t.Helper()
	o.node = true
	w := newWorld(t, o)
	d := w.s.Deps()
	w.s.Router().Get("/api/ported/{id}", probe, d.RequireAuth)
	w.s.Router().Get("/api/open", probe)
	w.s.Router().Post("/api/open", probe)
	w.s.Router().Get("/ported-page", probe)
	return w
}

// node decodes what the fake Node console answered; ok is false when the
// answer is not Node's.
func (a answer) node() (seen, bool) {
	var got seen
	if a.status != http.StatusTeapot || json.Unmarshal([]byte(a.body), &got) != nil || got.Server != "node" {
		return seen{}, false
	}
	return got, true
}

// wantUntouched checks that an answer is the Node console's alone: none
// of the Go pipeline's headers and cookies are on it.
func wantUntouched(t *testing.T, name string, a answer) seen {
	t.Helper()
	got, ok := a.node()
	if !ok {
		t.Fatalf("%s: answered %d %.200s, want the Node console's answer", name, a.status, a.body)
	}
	for _, header := range []string{"Content-Security-Policy", "X-Frame-Options", "RateLimit-Limit", "Access-Control-Allow-Origin"} {
		if a.header.Get(header) != "" {
			t.Errorf("%s: Go's %s is on Node's answer", name, header)
		}
	}
	if cookies := a.header.Values("Set-Cookie"); len(cookies) != 1 || !strings.HasPrefix(cookies[0], "node.sid=") {
		t.Errorf("%s: Set-Cookie = %q, want Node's cookie only", name, cookies)
	}
	return got
}

func TestUnportedRequestGoesToNodeUntouched(t *testing.T) {
	w := mixed(t, setup{})
	a := w.agent()
	res := a.do("POST", "/api/devices/A%2Fb?x=1&y=%zz", `{"a":1}`,
		"Host", "panel.example:5000", "Cookie", "bd.sid=s%3Aabc.sig", "Content-Type", "application/json",
		"X-Forwarded-For", "203.0.113.50", "X-Forwarded-Proto", "https", "X-Real-IP", "203.0.113.51")
	got := wantUntouched(t, "unported POST", res)
	want := seen{
		Server: "node", Method: "POST", URI: "/api/devices/A%2Fb?x=1&y=%zz", Host: "panel.example:5000",
		XFF: "127.0.0.1", Proto: "http", XFHost: "panel.example:5000", RealIP: "",
		Cookie: "bd.sid=s%3Aabc.sig", Type: "application/json", Body: `{"a":1}`,
	}
	if got != want {
		t.Errorf("node saw %+v\nwant     %+v", got, want)
	}
}

func TestTrustedProxyHeadersReachNode(t *testing.T) {
	w := mixed(t, setup{trustProxy: "loopback"})
	res := w.agent().get("/api/x", "X-Forwarded-For", "203.0.113.50, 127.0.0.1", "X-Forwarded-Proto", "https", "X-Forwarded-Host", "panel.example")
	got, _ := res.node()
	if got.XFF != "203.0.113.50" || got.Proto != "https" || got.Host != "panel.example" {
		t.Errorf("node saw %+v, want the client behind the trusted proxy", got)
	}
}

// Go answers nothing it has no route for: not the 404, not preflights,
// not files, not pages.
func TestWithNodeGoAnswersOnlyItsRoutes(t *testing.T) {
	w := mixed(t, setup{build: true, public: publicFolder(t)})
	a := w.agent()
	for name, res := range map[string]answer{
		"unknown API path":        a.get("/api/nothing"),
		"preflight of a path":     a.do("OPTIONS", "/api/nothing", "", "Origin", "tauri://localhost"),
		"static file":             a.get("/favicon.svg"),
		"file of the web app":     a.get("/_ui/immutable/assets/app.t3st.css"),
		"page of the web app":     a.get("/devices", "Accept", "text/html"),
		"sign-in page":            a.get("/login", "Accept", "text/html"),
		"broken body, no route":   a.do("POST", "/api/nothing", "{broken", "Content-Type", "application/json"),
		"write without a token":   a.do("POST", "/api/nothing", "{}", "Content-Type", "application/json"),
		"other method of a route": a.do("DELETE", "/api/ported/7", ""),
		"POST where Go has GET":   a.do("POST", "/api/ported/7", `{"a":1}`, "Content-Type", "application/json"),
		"deeper path":             a.get("/api/ported/7/more"),
		"shorter path":            a.get("/api/ported"),
	} {
		wantUntouched(t, name, res)
	}
	// The body of a request Go does not handle is not read by Go
	res := a.do("POST", "/api/ported/7", `{"a":1}`, "Content-Type", "application/json")
	if got, _ := res.node(); got.Body != `{"a":1}` || got.Method != "POST" {
		t.Errorf("node saw %+v", got)
	}
}

func TestWithNodeGoRouteMatchesLikeExpress(t *testing.T) {
	w := mixed(t, setup{})
	a := w.agent().signIn("viewer")
	seenBefore := w.node.count()
	for _, target := range []string{"/api/ported/7", "/api/ported/7/", "/API/Ported/7", "/Api/PORTED/7/"} {
		res := a.get(target)
		if res.status != 200 || res.json(t)["user"] != float64(roleIDs["viewer"]) {
			t.Errorf("GET %s: %d %.100s, want the Go route", target, res.status, res.body)
		}
		wantSecurityHeaders(t, res)
	}
	res := a.do("HEAD", "/api/ported/7", "")
	if res.status != 200 || res.body != "" || res.header.Get("Content-Type") != "application/json; charset=utf-8" || res.header.Get("X-Node") != "" {
		t.Errorf("HEAD on a Go GET route: %d %q %v", res.status, res.body, res.header)
	}
	if w.node.count() != seenBefore {
		t.Errorf("the Node console saw %d of these requests", w.node.count()-seenBefore)
	}
	if w.s.handles(httptest.NewRequest("GET", "/api/ported//7", nil)) {
		t.Error("a path with an empty part matched a Go route")
	}
}

func TestWithNodeOptionsOnAGoRoute(t *testing.T) {
	w := mixed(t, setup{})
	before := w.node.count()
	// Under /api/ the preflight ends in Go's step 3, as it would in Node's
	res := w.agent().do("OPTIONS", "/api/ported/7", "", "Origin", "tauri://localhost")
	if res.status != 204 || res.header.Get("Access-Control-Allow-Origin") != "tauri://localhost" || res.header.Get("X-Node") != "" {
		t.Errorf("OPTIONS on a Go API route: %d %v", res.status, res.header)
	}
	// Outside /api/ the route table lists its methods
	res = w.agent().do("OPTIONS", "/ported-page", "")
	if res.status != 200 || res.header.Get("Allow") != "GET,HEAD" || res.body != "GET,HEAD" {
		t.Errorf("OPTIONS on a Go page route: %d Allow=%q body=%q", res.status, res.header.Get("Allow"), res.body)
	}
	if w.node.count() != before {
		t.Error("the Node console saw a preflight of a Go route")
	}
}

func TestWithNodeGuardRefusalIsGosAnswer(t *testing.T) {
	w := mixed(t, setup{})
	before := w.node.count()
	res := w.agent().get("/api/ported/7")
	if res.status != 401 || res.body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
		t.Errorf("guard refusal: %d %s", res.status, res.body)
	}
	wantSecurityHeaders(t, res)
	// The pipeline's own refusals are Go's too: the CSRF gate and the body step
	if res := w.agent().do("POST", "/api/open", "{}", "Content-Type", "application/json"); res.status != 403 {
		t.Errorf("write to a Go route without a token: %d", res.status)
	}
	if res := w.agent().do("POST", "/api/open", "{broken", "Content-Type", "application/json"); res.status != 400 {
		t.Errorf("broken body to a Go route: %d", res.status)
	}
	if w.node.count() != before {
		t.Errorf("the Node console saw %d refused requests", w.node.count()-before)
	}
}

func TestWithNodeWebsocketUpgradeGoesToNode(t *testing.T) {
	w := mixed(t, setup{})
	// Go has a GET route for this very path
	called := false
	w.s.Router().Get("/api/socket", func(http.ResponseWriter, *http.Request) { called = true })
	w.s.Early().Get("/ws/echo", func(http.ResponseWriter, *http.Request) { called = true })

	for _, path := range []string{"/api/socket", "/ws/echo"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(w.front.URL, "http")+path, nil)
		if err != nil {
			cancel()
			t.Fatalf("dial %s through the front door: %v", path, err)
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
			t.Fatal(err)
		}
		_, msg, err := conn.Read(ctx)
		if err != nil || string(msg) != "node:hello" {
			t.Errorf("%s: got %q %v, want node:hello", path, msg, err)
		}
		conn.Close(websocket.StatusNormalClosure, "")
		cancel()
	}
	if called {
		t.Error("a Go route answered a websocket upgrade")
	}
	// Without the upgrade headers the same path is the Go route
	if res := w.agent().get("/api/socket"); res.status != 200 || !called {
		t.Errorf("plain GET: %d", res.status)
	}
}

func TestWithNodePassOnReachesNodeWithItsBody(t *testing.T) {
	w := mixed(t, setup{})
	bearer := func(rw http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			// The body step has read the body by now
			if text, _ := kit.Body(r).String("name"); text == "" && r.Method == http.MethodPost && r.Header.Get("Content-Type") == "application/json" {
				t.Errorf("the early handler did not see the parsed body")
			}
			kit.PassOn(rw, r)
			return
		}
		kit.JSON(rw, r, http.StatusOK, map[string]string{"stage": "early"})
	}
	w.s.Early().Post("/api/client-devices", bearer)
	w.s.Early().Get("/api/client-devices", bearer)
	w.s.Early().Get("/api/strategies", bearer)
	w.s.Router().Get("/api/strategies", probe)

	a := w.agent().signIn("admin")
	if res := a.do("POST", "/api/client-devices", `{"name":"x"}`, "Content-Type", "application/json", "Authorization", "Bearer t"); res.body != `{"stage":"early"}` {
		t.Fatalf("with a bearer token: %d %s", res.status, res.body)
	}

	// What the early stage passes on still meets the later steps in Go, as
	// it did in Node: without a token the CSRF gate refuses it
	if res := a.do("POST", "/api/client-devices", `{"name":"x"}`, "Content-Type", "application/json"); res.status != 403 || res.header.Get("X-Node") != "" {
		t.Errorf("passed on without a CSRF token: %d, want Go's 403", res.status)
	}

	body := `{"name":"` + strings.Repeat("y", 70000) + `"}`
	res := a.post("/api/client-devices?q=1", body)
	got := wantUntouched(t, "passed on POST", res)
	if got.Body != body || got.Method != "POST" || got.URI != "/api/client-devices?q=1" || got.Type != "application/json" {
		t.Errorf("node saw method %s uri %s type %s and %d body bytes, want the request as it arrived (%d bytes)", got.Method, got.URI, got.Type, len(got.Body), len(body))
	}
	if !strings.Contains(got.Cookie, "bd.sid=") {
		t.Errorf("node saw cookies %q", got.Cookie)
	}

	form := "a=1&b=two+words"
	w.s.Early().Put("/device/form", bearer)
	res = a.do("PUT", "/device/form", form, "Content-Type", "application/x-www-form-urlencoded", "X-CSRF-Token", a.csrf())
	if got := wantUntouched(t, "passed on form", res); got.Body != form {
		t.Errorf("node saw form body %q", got.Body)
	}
	if got := wantUntouched(t, "passed on GET", a.get("/api/client-devices")); got.Body != "" {
		t.Errorf("node saw a body on GET: %q", got.Body)
	}

	// A later Go route takes what the early stage passes on
	before := w.node.count()
	if res := a.get("/api/strategies"); res.status != 200 || !strings.Contains(res.body, `"route":"GET /api/strategies"`) || res.json(t)["lang"] != "en" {
		t.Errorf("passed on to a Go route: %d %.100s", res.status, res.body)
	}
	if w.node.count() != before {
		t.Error("the Node console saw a request a Go route answered")
	}
}

func TestWithNodeStepAsideInTheLaterStageReachesNode(t *testing.T) {
	w := mixed(t, setup{})
	w.s.Router().Mount("/api/bd").Get("/rules", probe, kit.OnMount("/api/automation"))
	res := w.agent().get("/api/bd/rules")
	wantUntouched(t, "route that steps aside", res)
}

func TestRoutingReportFollowsTheRouteTables(t *testing.T) {
	w := mixed(t, setup{debug: true})
	w.s.Early().Get("/api/early/{id}", probe)
	who := func(w *world, method, path string) string {
		q := url.Values{"method": {method}, "path": {path}}
		res := w.agent().get("/__bd/routing?" + q.Encode())
		if res.status != 200 {
			t.Fatalf("routing report: %d %s", res.status, res.body)
		}
		handler, _ := res.json(t)["handler"].(string)
		return handler
	}
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/api/ported/7", "go"},
		{"HEAD", "/API/Ported/7/", "go"},
		{"OPTIONS", "/api/ported/7", "go"},
		{"DELETE", "/api/ported/7", "node"},
		{"GET", "/api/ported", "node"},
		{"GET", "/api/client-devices", "node"},
		{"GET", "/api/early/7", "go"},
		{"POST", "/api/early/7", "node"},
		{"GET", "/__bd/routing", "go"},
	} {
		if got := who(w, c.method, c.path); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.method, c.path, got, c.want)
		}
	}

	alone := newWorld(t, setup{debug: true})
	alone.s.Router().Get("/api/ported/{id}", probe)
	if got := who(alone, "GET", "/api/client-devices"); got != "none" {
		t.Errorf("without Node: %q, want none", got)
	}
	if got := who(alone, "GET", "/api/ported/7"); got != "go" {
		t.Errorf("without Node: %q, want go", got)
	}
}

func TestRoutingReportIsOffByDefault(t *testing.T) {
	w := mixed(t, setup{})
	if _, ok := w.agent().get("/__bd/routing?method=GET&path=/api/ported/7").node(); !ok {
		t.Error("the routing report answers without CONSOLE_DEBUG_ROUTING")
	}
	alone := newWorld(t, setup{})
	if res := alone.agent().get("/__bd/routing?method=GET&path=/x"); res.status != 404 {
		t.Errorf("without Node: status %d, want 404", res.status)
	}
}

func TestNodeDownAnswers502(t *testing.T) {
	w := mixed(t, setup{})
	w.node.Close()
	res := w.agent().get("/api/x")
	if res.status != http.StatusBadGateway || res.json(t)["success"] != false {
		t.Errorf("status %d body %s, want 502 with success false", res.status, res.body)
	}
	// Go's own routes still answer
	if res := w.agent().get("/api/open"); res.status != 200 {
		t.Errorf("a Go route with Node down: %d", res.status)
	}
}

func TestStartAndStopLeaveNothingRunning(t *testing.T) {
	db := storetest.Migrated(t)
	node := newFakeNode(t)
	upstream, _ := url.Parse(node.URL)
	client := &http.Client{Transport: &http.Transport{}}
	settle := func() int {
		client.CloseIdleConnections()
		best := runtime.NumGoroutine()
		for i := 0; i < 100; i++ {
			time.Sleep(20 * time.Millisecond)
			if n := runtime.NumGoroutine(); n < best {
				best = n
			}
		}
		return best
	}
	// One request before the count, so the fake Node console's own goroutines are in it
	if res, err := client.Get(node.URL + "/warm"); err == nil {
		res.Body.Close()
	}
	before := settle()

	cfg := &config.Config{
		Host: "127.0.0.1", NodeUpstream: upstream, DataDir: t.TempDir(), UploadsDir: t.TempDir(),
		LangDir: "../../web-nodejs/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
		Getenv: func(key string) string {
			if key == "SESSION_SECRET" {
				return testSecret
			}
			return ""
		},
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	ticking := make(chan struct{}, 1)
	ended := make(chan struct{})
	s.Jobs().Add("test job", func(ctx context.Context) {
		ticking <- struct{}{}
		<-ctx.Done()
		close(ended)
	})
	// The console's own jobs and the one added here; route groups add more
	names := "," + strings.Join(s.Jobs().Names(), ",") + ","
	for _, want := range []string{"session cleanup", "sign-in housekeeping", "test job"} {
		if !strings.Contains(names, ","+want+",") {
			t.Errorf("jobs = %q, without %q", names, want)
		}
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-ticking
	for _, target := range []string{"/health", "/api/unported"} {
		res, err := client.Get("http://" + s.Addr() + target)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if want := map[string]int{"/health": 200, "/api/unported": 418}[target]; res.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", target, res.StatusCode, want)
		}
	}

	s.Stop()
	select {
	case <-ended:
	default:
		t.Error("a background job is still running after Stop")
	}
	if _, err := client.Get("http://" + s.Addr() + "/health"); err == nil {
		t.Error("the server still answers after Stop")
	}
	if after := settle(); after > before {
		buf := make([]byte, 1<<16)
		t.Errorf("%d goroutines before Start, %d after Stop:\n%s", before, after, buf[:runtime.Stack(buf, true)])
	}
}

// roundTripFunc is an http.RoundTripper made of a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStopWaitsForPictureSyncs(t *testing.T) {
	db := storetest.Migrated(t)
	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(),
		LangDir: "../../web-nodejs/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
		Getenv: func(key string) string {
			if key == "SESSION_SECRET" {
				return testSecret
			}
			return ""
		},
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	// A picture fetch that hangs until the test lets it go
	asked, release := make(chan struct{}), make(chan struct{})
	s.Deps().Avatars = avatars.New(avatars.Options{DB: db, Fetch: avatars.FetchOptions{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			close(asked)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return nil, errors.New("no picture")
		}),
	}})
	s.Deps().Avatars.StartOIDCSync(1, goapi.NewObject().Set("picture", "https://idp.example/me.png"))
	<-asked

	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a picture sync was running")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return after the picture sync ended")
	}
}
