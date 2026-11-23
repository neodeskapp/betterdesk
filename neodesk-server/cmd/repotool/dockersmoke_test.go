package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseSmokeArgs(t *testing.T) {
	if o, err := parseSmokeArgs(nil); err != nil || o.databaseType != "sqlite" || o.keep {
		t.Errorf("defaults %+v %v", o, err)
	}
	if o, err := parseSmokeArgs([]string{"--database-type", "postgres", "--database-url", "postgres://x", "--keep"}); err != nil || o.databaseURL != "postgres://x" || !o.keep {
		t.Errorf("postgres %+v %v", o, err)
	}
	if o, err := parseSmokeArgs([]string{"--database-type"}); err != nil || o.databaseType != "sqlite" {
		t.Errorf("type without value %+v %v", o, err)
	}
	for args, want := range map[string]string{
		"--database-type mysql":    "--database-type must be sqlite or postgres",
		"--database-type postgres": "--database-url is required for postgres smoke tests",
		"--fast":                   "Unknown option: --fast",
	} {
		if _, err := parseSmokeArgs(strings.Fields(args)); err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %s", args, err, want)
		}
	}
	runCommand(dockerSmoke, t.TempDir(), "--database-type", "mysql").want(t, 1, "", "FAIL local Docker smoke: --database-type must be sqlite or postgres\n")
}

func TestCSRFTokenAndCookies(t *testing.T) {
	for html, want := range map[string]string{
		`window.boot = { csrfToken: 'abc' }`:             "abc",
		`{csrfToken:"def"}`:                              "def",
		`<input type="hidden" NAME="_csrf" value="ghi">`: "ghi",
		`<div>nothing</div>`:                             "",
	} {
		if got := getCsrfToken(html); got != want {
			t.Errorf("getCsrfToken(%q) = %q", html, got)
		}
	}
	h := http.Header{}
	h.Add("Set-Cookie", "nd.sid=s1; Path=/; HttpOnly")
	h.Add("Set-Cookie", "csrf=c1")
	h.Add("Set-Cookie", "; Path=/")
	if got := firstCookie(h); got != "nd.sid=s1; csrf=c1" {
		t.Errorf("firstCookie = %q", got)
	}
}

// fakePanel is the console and the API of the container under test.
type fakePanel struct {
	mu       sync.Mutex
	requests []string
	login    string // what POST /api/auth/login answers
	verify   string
}

func (p *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.requests = append(p.requests, r.Method+" "+r.URL.Path+" cookie="+r.Header.Get("Cookie")+" csrf="+r.Header.Get("X-CSRF-Token"))
	p.mu.Unlock()
	switch r.URL.Path {
	case "/api/health", "/health":
		w.WriteHeader(http.StatusOK)
	case "/login":
		http.Redirect(w, r, "/app/login", http.StatusFound)
	case "/app/login":
		fmt.Fprint(w, "<html>the console</html>")
	case "/api/auth/login-page":
		http.SetCookie(w, &http.Cookie{Name: "nd.sid", Value: "pre"})
		fmt.Fprint(w, `{"csrfToken":"tok"}`)
	case "/api/auth/login":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "admin" || body["password"] != smokePassword || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, `{"success":false}`, http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "nd.sid", Value: "signed-in"})
		fmt.Fprint(w, p.login)
	case "/api/auth/verify":
		fmt.Fprint(w, p.verify)
	default:
		http.NotFound(w, r)
	}
}

// fakeDocker answers the docker commands of the smoke test and records them.
type fakeDocker struct {
	port    string
	keys    []string
	calls   []string
	healthy []string
}

func (d *fakeDocker) run(args ...string) dockerResult {
	call := strings.Join(args, " ")
	d.calls = append(d.calls, call)
	switch {
	case args[0] == "port":
		return dockerResult{stdout: "127.0.0.1:" + d.port + "\n"}
	case strings.HasSuffix(call, "cat /opt/rustdesk/.api_key"):
		key := d.keys[0]
		d.keys = d.keys[1:]
		return dockerResult{stdout: key + "\n"}
	case strings.Contains(call, "neodesk-server check"):
		return dockerResult{stdout: "PASS http://127.0.0.1:21121/api/health (200)\n"}
	case args[0] == "inspect":
		status := d.healthy[0]
		if len(d.healthy) > 1 {
			d.healthy = d.healthy[1:]
		}
		return dockerResult{stdout: status + "\n"}
	case args[0] == "logs":
		return dockerResult{stdout: "the container's log\n"}
	}
	return dockerResult{}
}

func newSmoke(t *testing.T, p *fakePanel, d *fakeDocker) (*smoke, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	d.port = u.Port()
	var out, errOut bytes.Buffer
	return &smoke{
		stdout: &out, stderr: &errOut,
		docker: d.run, client: smokeClient(),
		sleep:  func(time.Duration) {},
		suffix: "1-2",
	}, &out, &errOut
}

func TestDockerSmokeRun(t *testing.T) {
	p := &fakePanel{login: `{"success":true}`, verify: `{"success":true,"user":{"username":"admin"}}`}
	d := &fakeDocker{keys: []string{"key1", "key1"}, healthy: []string{"starting", "healthy"}}
	s, out, errOut := newSmoke(t, p, d)
	if err := s.run(smokeOptions{databaseType: "sqlite"}); err != nil {
		t.Fatalf("run: %v\n%s", err, errOut)
	}
	addr := "http://127.0.0.1:" + d.port
	if got, want := out.String(), "PASS http://127.0.0.1:21121/api/health (200)\nPASS local Docker smoke (sqlite) "+addr+" "+addr+"\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}

	var runCall string
	var calls []string
	for _, c := range d.calls {
		if strings.HasPrefix(c, "run ") {
			runCall, c = c, "run ..."
		}
		calls = append(calls, c)
	}
	want := []string{
		"volume create neodesk-local-gate-data-1-2",
		"volume create neodesk-local-gate-console-1-2",
		"run ...",
		"port neodesk-local-gate-1-2 5000/tcp",
		"port neodesk-local-gate-1-2 21121/tcp",
		"exec neodesk-local-gate-1-2 sh -c cat /opt/rustdesk/.api_key",
		"exec -u neodesk neodesk-local-gate-1-2 /usr/local/bin/neodesk-server check --api http://127.0.0.1:21121/api/health --panel http://127.0.0.1:5000/health --port 127.0.0.1:21121",
		"restart neodesk-local-gate-1-2",
		"port neodesk-local-gate-1-2 5000/tcp",
		"port neodesk-local-gate-1-2 21121/tcp",
		"exec neodesk-local-gate-1-2 sh -c cat /opt/rustdesk/.api_key",
		"inspect --format {{.State.Health.Status}} neodesk-local-gate-1-2",
		"inspect --format {{.State.Health.Status}} neodesk-local-gate-1-2",
		"rm --force neodesk-local-gate-1-2",
		"volume rm --force neodesk-local-gate-data-1-2",
		"volume rm --force neodesk-local-gate-console-1-2",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("docker calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	for _, part := range []string{
		"run --detach --name neodesk-local-gate-1-2 --label com.neodesk.local-gate=true --publish 127.0.0.1::5000 --publish 127.0.0.1::21121 --volume neodesk-local-gate-data-1-2:/opt/rustdesk --volume neodesk-local-gate-console-1-2:/app/data --add-host host.docker.internal:host-gateway --env NODE_ENV=production",
		"--env ADMIN_PASSWORD=" + smokePassword,
		"--env ENROLLMENT_MODE=managed --env DB_TYPE=sqlite neodesk:local",
	} {
		if !strings.Contains(runCall, part) {
			t.Errorf("docker run lacks %q:\n%s", part, runCall)
		}
	}

	// Sign-in: the redirect to the console, the token from login-page,
	// its cookie on the sign-in and both cookies on the check
	var signIn []string
	for _, r := range p.requests {
		if !strings.Contains(r, "/health") {
			signIn = append(signIn, r)
		}
	}
	wantSignIn := []string{
		"GET /login cookie= csrf=",
		"GET /app/login cookie= csrf=",
		"GET /api/auth/login-page cookie= csrf=",
		"POST /api/auth/login cookie=nd.sid=pre csrf=tok",
		"GET /api/auth/verify cookie=nd.sid=pre; nd.sid=signed-in csrf=",
	}
	if !reflect.DeepEqual(signIn[:5], wantSignIn) || len(signIn) != 10 {
		t.Errorf("sign-in requests:\n%s", strings.Join(signIn, "\n"))
	}
}

func TestDockerSmokePostgres(t *testing.T) {
	p := &fakePanel{login: `{"success":true}`, verify: `{"success":true,"user":{"id":1}}`}
	d := &fakeDocker{keys: []string{"k", "k"}, healthy: []string{"healthy"}}
	s, out, _ := newSmoke(t, p, d)
	s.image = "neodesk:test"
	if err := s.run(smokeOptions{databaseType: "postgres", databaseURL: "postgres://u:p@h/db", keep: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PASS local Docker smoke (postgres) ") {
		t.Errorf("output %q", out)
	}
	for _, c := range d.calls {
		if strings.HasPrefix(c, "run ") && !strings.HasSuffix(c, "--env DB_TYPE=postgres --env DATABASE_URL=postgres://u:p@h/db --env DB_URL=postgres://u:p@h/db neodesk:test") {
			t.Errorf("run: %s", c)
		}
		if strings.HasPrefix(c, "rm ") || strings.HasPrefix(c, "volume rm") {
			t.Errorf("--keep removed: %s", c)
		}
	}
}

func TestDockerSmokeFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		panel  *fakePanel
		docker *fakeDocker
		want   string
	}{
		{"key changed", &fakePanel{login: `{"success":true}`, verify: `{"success":true,"user":{}}`},
			&fakeDocker{keys: []string{"a", "b"}, healthy: []string{"healthy"}}, "API key changed after a container restart"},
		{"no key", &fakePanel{login: `{"success":true}`, verify: `{"success":true,"user":{}}`},
			&fakeDocker{keys: []string{""}}, "container did not create an API key"},
		{"rejected", &fakePanel{login: `{"success":false}`}, &fakeDocker{}, `login was rejected: {"success":false}`},
		{"not JSON", &fakePanel{login: `<html>`}, &fakeDocker{}, "login returned invalid JSON: <html>"},
		{"no session", &fakePanel{login: `{"success":true}`, verify: `{"success":true,"user":null}`},
			&fakeDocker{}, `session verification failed: {"success":true,"user":null}`},
		{"unhealthy", &fakePanel{login: `{"success":true}`, verify: `{"success":true,"user":{}}`},
			&fakeDocker{keys: []string{"a", "a"}, healthy: []string{"unhealthy"}}, "container health status is unhealthy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _, errOut := newSmoke(t, c.panel, c.docker)
			err := s.run(smokeOptions{databaseType: "sqlite"})
			if err == nil || err.Error() != c.want {
				t.Fatalf("error %v, want %s", err, c.want)
			}
			// The container's log is shown and everything is removed
			if errOut.String() != "the container's log\n" {
				t.Errorf("stderr %q", errOut)
			}
			if n := len(c.docker.calls); n < 3 || c.docker.calls[n-3] != "rm --force neodesk-local-gate-1-2" {
				t.Errorf("calls %v", c.docker.calls)
			}
		})
	}
}

func TestDockerSmokeDockerFails(t *testing.T) {
	s := &smoke{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, suffix: "1-2", sleep: func(time.Duration) {}}
	s.docker = func(args ...string) dockerResult {
		if args[0] == "run" {
			return dockerResult{code: 125, stderr: "Unable to find image\n"}
		}
		return dockerResult{}
	}
	err := s.run(smokeOptions{databaseType: "sqlite"})
	if err == nil || !strings.HasPrefix(err.Error(), "docker run --detach --name neodesk-local-gate-1-2 ") || !strings.HasSuffix(err.Error(), " neodesk:local failed: Unable to find image") {
		t.Errorf("error %v", err)
	}
	s.docker = func(args ...string) dockerResult { return dockerResult{code: 1} }
	if err := s.run(smokeOptions{databaseType: "sqlite"}); err == nil || err.Error() != "docker volume create neodesk-local-gate-data-1-2 failed: exit code 1" {
		t.Errorf("error %v", err)
	}
}

func TestWaitForHealth(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	slept := 0
	s := &smoke{client: smokeClient(), sleep: func(time.Duration) { slept++ }}
	if err := s.waitForHealth(srv.URL, srv.URL); err != nil || slept != 1 {
		t.Errorf("err %v after %d sleeps", err, slept)
	}

	// Both have to answer: an API that is up does not do for a console that is not
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	panelCalls := 0
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if panelCalls++; panelCalls <= 2 {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer panel.Close()
	slept = 0
	if err := s.waitForHealth(api.URL, panel.URL); err != nil || slept != 2 {
		t.Errorf("err %v after %d sleeps", err, slept)
	}
}
