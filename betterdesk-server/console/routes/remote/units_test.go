package remote

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// refusedTokens are the requests of testdata/crashes.json whose guest or
// share token is no text: the Go console refuses them with 400 (MC1), as
// the guest and MeshCentral routes refuse such a token.
var refusedTokens = map[string]string{
	"/api/remote/viewer/dev-1?guest[toString]=1":      `{"error":"Invalid guest token"}`,
	"/api/remote/viewer/dev-1?t[toString]=1":          `{"error":"Invalid guest token"}`,
	"/api/remote/viewer/dev-1?mesh_share[toString]=1": `{"error":"Invalid share token"}`,
	"/api/remote/guest-info?t[toString]=1":            `{"error":"Invalid guest token"}`,
	"/api/remote/guest-info?guest[toString]=1":        `{"error":"Invalid guest token"}`,
}

// TestRequestsThatEndedNode sends the requests that ended the Node
// process (testdata/crashes.json, from testdata/nodeoracle/crash.js): a
// query value with a member toString where the route needed text. A
// guest or share token of that kind is refused with 400 (refusedTokens);
// for the rest the Go console closes that one connection without an
// answer (DECISIONS A19). The requests Node still answered get Node's
// status.
func TestRequestsThatEndedNode(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "crashes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Path     string `json:"path"`
		SignedIn bool   `json:"signedIn"`
		Ended    bool   `json:"ended"`
		Status   int    `json:"status"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, storetest.Migrated(t), "1.0.0", false)
	operator := &account{UserID: 5}
	operator.User.ID, operator.User.Username, operator.User.Role = 5, "e2e_operator", "operator"
	ended := 0
	for _, c := range cases {
		v := &vector{Request: request{Method: "GET", Path: c.Path}}
		if c.SignedIn {
			v.Request.Session = operator
		}
		var rec *httptest.ResponseRecorder
		aborted := func() (aborted bool) {
			defer func() {
				if p := recover(); p != nil {
					if err, ok := p.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
						panic(p)
					}
					aborted = true
				}
			}()
			rec = w.do(v)
			return false
		}()
		refusal, refused := refusedTokens[c.Path]
		switch {
		case c.Ended && refused:
			if aborted || rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != refusal {
				t.Errorf("%s (signed in %v): aborted %v; want 400 %s", c.Path, c.SignedIn, aborted, refusal)
			}
		case c.Ended && !aborted:
			t.Errorf("%s (signed in %v) answered %d; Node ended there, so the connection should be dropped", c.Path, c.SignedIn, rec.Code)
		case !c.Ended && aborted:
			t.Errorf("%s (signed in %v) dropped the connection; Node answered %d", c.Path, c.SignedIn, c.Status)
		case !c.Ended && rec.Code != c.Status:
			t.Errorf("%s (signed in %v) answered %d, Node %d", c.Path, c.SignedIn, rec.Code, c.Status)
		}
		if c.Ended {
			ended++
		}
	}
	if ended < 8 {
		t.Fatalf("only %d requests ended Node in the recording, want the 8 of crash.js", ended)
	}
}

// TestDevMockSwitch checks that the dev mock is served only when
// BD_DEV_MOCK_REMOTE is 1 (after trimming), as lib/devMockRemote did, and
// that without it the address is no route of the group.
func TestDevMockSwitch(t *testing.T) {
	for _, tc := range []struct {
		env string
		on  bool
	}{{"", false}, {"1", true}, {" 1 ", true}, {"\t1\n", true}, {"0", false}, {"true", false}, {"11", false}, {"yes", false}} {
		d := &kit.Deps{Router: kit.NewRouter(), Config: &config.Config{Getenv: func(key string) string {
			if key == "BD_DEV_MOCK_REMOTE" {
				return tc.env
			}
			return ""
		}}}
		src := defaultSources(d)
		if got := src.mock != nil; got != tc.on {
			t.Errorf("BD_DEV_MOCK_REMOTE=%q: mock on %v, want %v", tc.env, got, tc.on)
		}
	}
	if src := defaultSources(&kit.Deps{}); src.mock != nil {
		t.Error("without a configuration the mock is on")
	}
}

// TestDevMockScript serves the mock with the headers res.sendFile and the
// old route gave it, the bytes of the old file, and passes the request on
// when the mock is off.
func TestDevMockScript(t *testing.T) {
	on := newWorld(t, storetest.Migrated(t), "1.0.0", true)
	rec := on.do(&vector{Request: request{Method: "GET", Path: "/dev/mock-remote.js"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("mock on: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}
	if !strings.HasPrefix(rec.Header().Get("ETag"), `W/"`) || rec.Header().Get("Last-Modified") == "" || rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("mock on: ETag %q, Last-Modified %q, Accept-Ranges %q", rec.Header().Get("ETag"), rec.Header().Get("Last-Modified"), rec.Header().Get("Accept-Ranges"))
	}
	original, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "web-nodejs", "lib", "devMockRemote", "mock-session.js"))
	if err == nil && rec.Body.String() != string(original) {
		t.Error("the embedded mock is not the old console's file")
	}
	off := newWorld(t, storetest.Migrated(t), "1.0.0", false)
	rec = off.do(&vector{Request: request{Method: "GET", Path: "/dev/mock-remote.js"}})
	if rec.Code != http.StatusNotFound || rec.Body.String() != "no route" {
		t.Errorf("mock off: %d %q, want the request passed on", rec.Code, rec.Body.String())
	}
}

// TestRegisteredRoutes lists the group's routes in the order of the route
// file, the dev mock first, and the agent token route in the early stage.
func TestRegisteredRoutes(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), "1.0.0", false)
	var got []string
	for _, route := range w.deps.Router.Routes() {
		if !strings.HasPrefix(route.Path, "/__test") {
			got = append(got, route.Method+" "+route.Path)
		}
	}
	want := []string{
		"GET /dev/mock-remote.js",
		"GET /api/remote/login-info",
		"GET /api/remote/guest-info",
		"GET /api/remote/desk",
		"GET /remote-dashboard",
		"GET /api/remote/viewer/{deviceId}",
		"GET /remote-cdap/{deviceId}",
		"GET /remote-desktop/{deviceId}",
		"GET /api/remote/sessions",
		"GET /api/remote/session/{deviceId}",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var early []string
	for _, route := range w.deps.Early.Routes() {
		early = append(early, route.Method+" "+route.Path)
	}
	if strings.Join(early, "\n") != "POST /api/bd/remote-agent-token" {
		t.Errorf("early routes: %v", early)
	}
}

// TestWithoutTheRelay answers as the old routes did when the relay module
// could not be loaded: no sessions, 404 for a session, and 500 for a token.
func TestWithoutTheRelay(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), "1.0.0", false)
	w.deps.Router, w.deps.Early = kit.NewRouter(), kit.NewRouter()
	src := w.sources("1.0.0")
	src.relay = nil
	register(w.deps, src)
	registerAgentToken(w.deps, src)
	w.deps.Router.Get("/__test/signin", signIn(t))
	operator := &account{UserID: 5}
	operator.User.ID, operator.User.Username, operator.User.Role = 5, "e2e_operator", "operator"
	body := "{}"
	for _, c := range []struct {
		v      vector
		status int
		body   string
	}{
		{vector{Request: request{Path: "/api/remote/sessions", Session: operator}}, 200, `{"sessions":[]}`},
		{vector{Request: request{Path: "/api/remote/session/dev-1", Session: operator}}, 404, `{"error":"Remote relay not available"}`},
		{vector{Request: request{Method: "POST", Path: "/api/bd/remote-agent-token", Type: "application/json", Body: &body,
			Headers: map[string]string{"Authorization": "Bearer tok-device-a"}}}, 500, `{"error":"Token issuance failed"}`},
	} {
		rec := w.do(&c.v)
		if rec.Code != c.status || rec.Body.String() != c.body {
			t.Errorf("%s: %d %s, want %d %s", c.v.Request.Path, rec.Code, rec.Body.String(), c.status, c.body)
		}
	}
}

// TestNoComposeFileSwitchesTheMockOn keeps the dev mock out of every
// compose file at the top of the repository: only the local dev setup may
// set BD_DEV_MOCK_REMOTE.
func TestNoComposeFileSwitchesTheMockOn(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "docker-compose*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no compose files in %s", root)
	}
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), "BD_DEV_MOCK_REMOTE") {
			t.Errorf("%s switches the dev mock on", filepath.Base(file))
		}
	}
}

// repoRoot is the folder with the compose files: above the test, or the
// worktree in the dev image for a copy of the server folder.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if found, _ := filepath.Glob(filepath.Join(dir, "docker-compose*.y*ml")); len(found) > 0 {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "/repo"
		}
		dir = parent
	}
}

// TestGuestCookieWithoutATextIsRefused: a guest cookie that holds no text
// is refused with 400 by the guest page and the viewer, as a query token
// of that kind is.
func TestGuestCookieWithoutATextIsRefused(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), "1.0.0", false)
	for _, path := range []string{"/api/remote/guest-info", "/api/remote/viewer/dev-1"} {
		v := &vector{Request: request{Method: "GET", Path: path, Headers: map[string]string{"Cookie": `bd.guest=j:{"toString":1}`}}}
		rec := w.do(v)
		if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != `{"error":"Invalid guest token"}` {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
