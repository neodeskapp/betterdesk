package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// request is what the fake console saw.
type request struct {
	Method, Path, Body, CSRF, Cookie, Agent, Language, Accept string
}

// fakeConsole answers the routes the seed uses with fixed answers and
// records every request. answer may override a route.
type fakeConsole struct {
	mu       sync.Mutex
	requests []request
	answer   func(r *http.Request, body string) (status int, header map[string]string, json string, ok bool)
}

func (f *fakeConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	body := string(data)
	f.mu.Lock()
	f.requests = append(f.requests, request{r.Method, r.URL.Path, body, r.Header.Get("X-Csrf-Token"), r.Header.Get("Cookie"),
		r.Header.Get("User-Agent"), r.Header.Get("Accept-Language"), r.Header.Get("Accept")})
	f.mu.Unlock()
	if f.answer != nil {
		if status, header, text, ok := f.answer(r, body); ok {
			for k, v := range header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, text)
			return
		}
	}
	reply := func(status int, text string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, text)
	}
	signedIn := strings.Contains(r.Header.Get("Cookie"), "nd.sid=s1")
	switch r.Method + " " + r.URL.Path {
	case "GET /api/console/bootstrap":
		if !signedIn {
			reply(401, `{"error":"Unauthorized"}`)
			return
		}
		reply(200, `{"csrfToken":"tok-2"}`)
	case "GET /api/auth/login-page":
		http.SetCookie(w, &http.Cookie{Name: "__csrf_h", Value: "h1", Path: "/"})
		reply(200, `{"csrfToken":"tok-1"}`)
	case "POST /api/auth/login":
		if r.Header.Get("X-Csrf-Token") != "tok-1" || !strings.Contains(r.Header.Get("Cookie"), "__csrf_h=h1") {
			reply(403, `{"error":"csrf"}`)
			return
		}
		if body != `{"username":"admin","password":"pw"}` {
			reply(401, `{"success":false}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "nd.sid", Value: "s1", Path: "/", HttpOnly: true})
		reply(200, `{"success":true}`)
	case "GET /api/users":
		reply(200, `{"data":{"users":[{"username":"admin","id":1},{"username":"demo_viewer","id":3}]}}`)
	case "GET /api/folders":
		reply(200, `{"data":{"folders":[{"name":"Lab","id":9}]}}`)
	case "POST /api/folders":
		var f struct{ Name string }
		_ = json.Unmarshal(data, &f)
		reply(201, `{"data":{"id":`+map[string]string{"Office": "1", "Warehouse": "2", "Servers": "3"}[f.Name]+`}}`)
	case "GET /api/device-groups":
		reply(200, `{"data":{"groups":[{"name":"All laptops","guid":"g-laptops"}]}}`)
	case "POST /api/device-groups":
		if strings.Contains(body, "manual") {
			reply(201, `{"group":{"guid":"g-support"}}`)
			return
		}
		reply(201, `{"data":{"guid":"g-critical"}}`)
	case "POST /api/users":
		reply(409, `{"error":"exists"}`)
	default:
		reply(200, `{}`)
	}
}

func (f *fakeConsole) seen() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.requests...)
}

func newTestClient(t *testing.T, f *fakeConsole) (*consoleClient, *strings.Builder, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	var warn strings.Builder
	slept := &[]time.Duration{}
	c := newConsoleClient(srv.URL, io.Discard, &warn)
	c.sleep = func(_ context.Context, d time.Duration) error { *slept = append(*slept, d); return nil }
	return c, &warn, slept
}

func TestSignInTakesTheTokenAndTheSessionCookie(t *testing.T) {
	f := &fakeConsole{}
	c, _, _ := newTestClient(t, f)
	ctx := context.Background()
	if err := c.signIn(ctx, "", "admin", "pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.api(ctx, http.MethodGet, "/api/users", nil); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range f.seen() {
		paths = append(paths, r.Method+" "+r.Path)
	}
	want := "GET /api/console/bootstrap, GET /api/auth/login-page, POST /api/auth/login, GET /api/console/bootstrap, GET /api/users"
	if got := strings.Join(paths, ", "); got != want {
		t.Fatalf("requests:\n%s\nwant:\n%s", got, want)
	}
	last := f.seen()[4]
	if last.CSRF != "tok-2" || last.Cookie != "__csrf_h=h1; nd.sid=s1" {
		t.Errorf("the request after sign-in carried token %q and cookies %q", last.CSRF, last.Cookie)
	}
	// The headers of a Node fetch: the console keeps the user agent of the
	// seed's session and puts it into the audit log
	if last.Agent != "node" || last.Language != "*" || last.Accept != "*/*" {
		t.Errorf("headers: user agent %q, language %q, accept %q", last.Agent, last.Language, last.Accept)
	}
	if first := f.seen()[0]; first.Accept != "application/json" {
		t.Errorf("the bootstrap asked for %q", first.Accept)
	}
}

func TestSignInFailsOnAWrongPassword(t *testing.T) {
	c, _, _ := newTestClient(t, &fakeConsole{})
	if err := c.signIn(context.Background(), "", "admin", "wrong"); err == nil || !strings.Contains(err.Error(), "admin login failed") {
		t.Fatalf("sign-in with a wrong password: %v", err)
	}
}

func TestRateLimitedRequestsWaitAndTryAgain(t *testing.T) {
	calls := 0
	f := &fakeConsole{answer: func(r *http.Request, _ string) (int, map[string]string, string, bool) {
		if r.URL.Path != "/api/folders" {
			return 0, nil, "", false
		}
		calls++
		switch calls {
		case 1:
			return 429, map[string]string{"Ratelimit-Reset": "2"}, `{"error":"slow down"}`, true
		case 2:
			return 429, nil, `{"error":"slow down"}`, true
		}
		return 200, nil, `{"data":{"folders":[]}}`, true
	}}
	c, warn, slept := newTestClient(t, f)
	got, err := c.api(context.Background(), http.MethodGet, "/api/folders", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || listIn(prop(got, "data"), "folders") == nil {
		t.Fatalf("%d calls, answer %v", calls, got)
	}
	// RateLimit-Reset plus a second; 15 seconds plus one without the header
	if len(*slept) != 2 || (*slept)[0] != 3*time.Second || (*slept)[1] != 16*time.Second {
		t.Errorf("waited %v", *slept)
	}
	if warn.Len() != 0 {
		t.Errorf("warned about a request that went through: %s", warn.String())
	}

	// Eight retries at most, then the 429 is the answer and is reported
	f.answer = func(*http.Request, string) (int, map[string]string, string, bool) {
		return 429, map[string]string{"Ratelimit-Reset": "1"}, `{"message":"too many"}`, true
	}
	*slept = nil
	before := len(f.seen())
	if _, err := c.api(context.Background(), http.MethodGet, "/x", nil); err != nil {
		t.Fatal(err)
	}
	if n := len(f.seen()) - before; n != 9 || len(*slept) != 8 {
		t.Errorf("%d requests and %d waits, want 9 and 8", n, len(*slept))
	}
	if !strings.Contains(warn.String(), "GET /x -> 429 too many") {
		t.Errorf("warning: %q", warn.String())
	}
}

func TestSeedViaAPISkipsWhatThereIsAndSendsTheBodies(t *testing.T) {
	f := &fakeConsole{}
	c, warn, _ := newTestClient(t, f)
	ctx := context.Background()
	if err := c.signIn(ctx, "", "admin", "pw"); err != nil {
		t.Fatal(err)
	}
	start := len(f.seen())
	d := newDemo(time.UnixMilli(int64(sampleMS)))
	if err := d.seedViaAPI(ctx, c); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range f.seen()[start:] {
		if r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/tags") {
			continue
		}
		got = append(got, r.Method+" "+r.Path+" "+r.Body)
	}
	tags := 0
	for _, r := range f.seen()[start:] {
		if strings.HasSuffix(r.Path, "/tags") {
			tags++
			if r.Path == "/api/devices/900100015/tags" && r.Body != `{"tags":["server","linux","critical"]}` {
				t.Errorf("tags body %s", r.Body)
			}
		}
	}
	// Every device with tags but the deleted one
	if tags != 29 {
		t.Errorf("%d tag requests, want 29", tags)
	}
	want := []string{
		"GET /api/users ",
		`POST /api/users {"username":"demo_operator","role":"operator","email":"operator@example.invalid","password":"Demo-Password-demo_operator-42!"}`,
		`POST /api/users {"username":"demo_serveradmin","role":"server_admin","email":"serveradmin@example.invalid","password":"Demo-Password-demo_serveradmin-42!"}`,
		"GET /api/folders ",
		`POST /api/folders {"name":"Office","color":"#22c55e"}`,
		`POST /api/folders {"name":"Warehouse","color":"#f59e0b"}`,
		`POST /api/folders {"name":"Servers","color":"#6366f1"}`,
	}
	folderOf := map[string]string{"Office": "1", "Warehouse": "2", "Servers": "3", "Lab": "9"}
	for _, dv := range devices {
		if dv.Folder != "" && !dv.Deleted {
			want = append(want, `PATCH /api/devices/`+dv.ID+`/folder {"folderId":`+folderOf[dv.Folder]+`}`)
		}
	}
	want = append(want,
		"GET /api/device-groups ",
		// The manual group has no tag filter: the key is left out
		`POST /api/device-groups {"name":"Critical servers","source_type":"tag","tag_filter":"critical"}`,
		`POST /api/device-groups {"name":"Support team devices","source_type":"manual"}`,
		`PUT /api/devices/900100022/groups {"groupGuids":["g-support"]}`,
		`PUT /api/devices/900100023/groups {"groupGuids":["g-support"]}`,
	)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// 409 on a new user is expected and not reported
	if warn.Len() != 0 {
		t.Errorf("warnings: %s", warn.String())
	}
}
