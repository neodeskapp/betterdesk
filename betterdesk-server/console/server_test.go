package console

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/api"
	"github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestRouteMatchesLikeExpress: a route takes its path in any letter case
// and with a slash at the end, HEAD on a GET route answers the GET's
// headers without a body, and a path with an empty part fits no route.
func TestRouteMatchesLikeExpress(t *testing.T) {
	w := goOnly(t, setup{})
	w.s.Router().Get("/api/ported/{id}", probe, w.s.Deps().RequireAuth)
	a := w.agent().signIn("viewer")
	for _, target := range []string{"/api/ported/7", "/api/ported/7/", "/API/Ported/7", "/Api/PORTED/7/"} {
		res := a.get(target)
		if res.status != 200 || res.json(t)["route"] != "GET "+target || res.json(t)["user"] != float64(roleIDs["viewer"]) {
			t.Errorf("GET %s: %d %.100s, want the route", target, res.status, res.body)
		}
		wantSecurityHeaders(t, res)
	}
	res := a.do("HEAD", "/api/ported/7", "")
	if res.status != 200 || res.body != "" || res.header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("HEAD on a GET route: %d %q %v", res.status, res.body, res.header)
	}
	if res := a.get("/api/ported//7"); res.status != 404 || res.body != `{"success":false,"error":"Not Found"}` {
		t.Errorf("a path with an empty part: %d %.100s, want the 404", res.status, res.body)
	}
}

// TestStepAsideInTheLaterStageEndsInTheNotFound: a route that steps aside
// (kit.OnMount at another mount) with no later route to take the request
// leaves it to the end of the pipeline, the 404.
func TestStepAsideInTheLaterStageEndsInTheNotFound(t *testing.T) {
	w := goOnly(t, setup{})
	for _, mount := range []string{"/api/test-one", "/api/test-two"} {
		w.s.Router().Mount(mount).Get("/rules", probe, kit.OnMount("/api/test-two"))
	}
	if res := w.agent().get("/api/test-one/rules"); res.status != 404 || res.body != `{"success":false,"error":"Not Found"}` {
		t.Errorf("route that steps aside: %d %.100s, want the 404", res.status, res.body)
	}
	if res := w.agent().get("/api/test-two/rules"); res.status != 200 || res.json(t)["route"] != "GET /api/test-two/rules" {
		t.Errorf("the route at its own mount: %d %.100s", res.status, res.body)
	}
}

// TestRoutingReportNamesGo: the routing report says Go answers every
// request, routes, files, pages, sockets and the 404 alike.
func TestRoutingReportNamesGo(t *testing.T) {
	w := newWorld(t, setup{debug: true})
	w.s.Router().Get("/api/ported/{id}", probe)
	who := func(method, path string, upgrade bool) string {
		q := url.Values{"method": {method}, "path": {path}}
		if upgrade {
			q.Set("upgrade", "websocket")
		}
		res := w.agent().get("/__bd/routing?" + q.Encode())
		if res.status != 200 {
			t.Fatalf("routing report: %d %s", res.status, res.body)
		}
		handler, _ := res.json(t)["handler"].(string)
		return handler
	}
	for _, path := range []string{"/api/client-devices", "/api/ported/7", "/", "/login", "/favicon.svg", "/nothing/here", "/__bd/routing"} {
		for _, method := range []string{"GET", "DELETE"} {
			if got := who(method, path, false); got != "go" {
				t.Errorf("%s %s: %q, want go", method, path, got)
			}
		}
	}
	for _, path := range []string{"/ws/device-status", "/ws/nothing"} {
		if got := who("GET", path, true); got != "go" {
			t.Errorf("an upgrade to %s: %q, want go", path, got)
		}
	}
}

func TestRoutingReportIsOffByDefault(t *testing.T) {
	w := newWorld(t, setup{})
	if res := w.agent().get("/__bd/routing?method=GET&path=/x"); res.status != 404 || strings.Contains(res.body, "handler") {
		t.Errorf("status %d %s, want the 404", res.status, res.body)
	}
}

func TestStartAndStopLeaveNothingRunning(t *testing.T) {
	db := storetest.Migrated(t)
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
	before := settle()

	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(),
		LangDir: "static/files/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
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
	for _, target := range []string{"/health", "/api/nothing"} {
		res, err := client.Get("http://" + s.Addr() + target)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if want := map[string]int{"/health": 200, "/api/nothing": 404}[target]; res.StatusCode != want {
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
		LangDir: "static/files/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
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

// TestAPICallsNameTheSignedInAccount: the console's calls to the API
// server name the account signed in on the request as their actor, which
// the API server records as creator or handler; a call outside a request
// names none (BUGS G7).
func TestAPICallsNameTheSignedInAccount(t *testing.T) {
	actors := make(chan string, 4)
	w := goOnly(t, setup{api: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		id, _ := api.IdentityFromContext(r.Context())
		actors <- id.Username + "/" + id.Actor
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"success":true,"code":"ABC123","id":"x"}`))
	})})
	a := w.agent().signIn("admin")
	if res := a.post("/api/panel/pairing/codes", `{"label":"x"}`); res.status >= 500 {
		t.Fatalf("pairing code: %d %s", res.status, res.body)
	}
	if got := <-actors; got != "apikey:console-bridge/e2e_admin" {
		t.Errorf("the API saw the caller %q", got)
	}
	_, _ = w.s.Deps().GoAPI.Get(context.Background(), "/health")
	if got := <-actors; got != "apikey:console-bridge/" {
		t.Errorf("a call outside a request: %q", got)
	}
}
