package goapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// signInAPI answers the sign-in paths with a fixed status and body and
// records what it was sent.
type signInAPI struct {
	mu      sync.Mutex
	status  int
	body    string
	calls   int
	request string
	sent    string
	header  http.Header
	hang    bool
}

func (s *signInAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls++
	s.request, s.sent, s.header = r.Method+" "+r.RequestURI, string(body), r.Header
	hang, status, answer := s.hang, s.status, s.body
	s.mu.Unlock()
	if hang {
		<-r.Context().Done()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, answer)
}

// set changes the fake between calls.
func (s *signInAPI) set(change func(s *signInAPI)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s)
}

func (s *signInAPI) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestSignInCallsSendOnlyTheBody(t *testing.T) {
	api := &signInAPI{status: 200, body: `{}`}
	c := New(api)
	ctx := context.Background()
	calls := []struct {
		name    string
		call    func()
		request string
		sent    string
	}{
		{"AuthenticateViaGo", func() { c.AuthenticateViaGo(ctx, "alice", "pw") }, "POST /api/auth/login", `{"username":"alice","password":"pw"}`},
		{"VerifyTOTPViaGo", func() { c.VerifyTOTPViaGo(ctx, "partial", "123456") }, "POST /api/auth/login/2fa", `{"partial_token":"partial","code":"123456"}`},
		{"TryGoServerAuth", func() { c.TryGoServerAuth(ctx, "alice", "pw") }, "POST /api/auth/login", `{"username":"alice","password":"pw"}`},
		{"TryLDAPVerifyOnGo", func() { c.TryLDAPVerifyOnGo(ctx, "alice", "pw") }, "POST /api/auth/ldap/verify", `{"username":"alice","password":"pw"}`},
		{"GetGoSSOStatus", func() { c.GetGoSSOStatus(ctx) }, "GET /api/auth/sso/status", ""},
		{"CheckGoServerHealth", func() { c.CheckGoServerHealth(ctx) }, "GET /api/health", ""},
	}
	for _, tc := range calls {
		tc.call()
		if api.request != tc.request || api.sent != tc.sent {
			t.Errorf("%s: %s %s, want %s %s", tc.name, api.request, api.sent, tc.request, tc.sent)
		}
		wantType := ""
		if tc.sent != "" {
			wantType = "application/json"
		}
		if api.header.Get("Content-Type") != wantType || api.header.Get("Accept") != "" || api.header.Get("User-Agent") != "" {
			t.Errorf("%s: headers = %v", tc.name, api.header)
		}
	}
}

func TestAuthenticateViaGo(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{200, `{"token":"t","role":"admin","username":"alice"}`, `{"token":"t","role":"admin","username":"alice"}`},
		{200, `{"requires_2fa":true,"partial_token":"p"}`, `{"requires_2fa":true,"partial_token":"p"}`},
		{200, `null`, `null`},
		{401, `{"error":"Invalid credentials"}`, `{"rejected":true,"status":401,"error":"Invalid credentials"}`},
		{400, `{"error":"Invalid JSON"}`, `{"rejected":true,"status":400,"error":"Invalid JSON"}`},
		{401, `{}`, `{"rejected":true,"status":401}`},
		{401, `null`, `null`},
		{429, `{"error":"Too many login attempts"}`, `{"rejected":true,"rateLimited":true,"error":"Too many login attempts"}`},
		{403, `{"error":"x"}`, `null`},
		{500, `{"error":"x"}`, `null`},
		{401, `not json`, `null`},
		{200, ``, `null`},
	}
	for _, c := range cases {
		api := &signInAPI{status: c.status, body: c.body}
		if got := toJSON(t, New(api).AuthenticateViaGo(context.Background(), "u", "p")); got != c.want {
			t.Errorf("status %d body %s: %s, want %s", c.status, c.body, got, c.want)
		}
	}
	if got := New(&signInAPI{hang: true}, WithTimeout(20*time.Millisecond)).AuthenticateViaGo(context.Background(), "u", "p"); got != nil {
		t.Errorf("no answer: %v, want nil", got)
	}
}

func TestVerifyTOTPViaGo(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{200, `{"token":"t","role":"admin"}`, `{"token":"t","role":"admin"}`},
		{200, `{"role":"admin"}`, `null`},
		{401, `{"error":"Invalid code"}`, `{"rejected":true,"error":"Invalid code"}`},
		{400, `{"error":"bad"}`, `{"rejected":true,"error":"bad"}`},
		{429, `{"error":"slow down"}`, `{"rejected":true,"rateLimited":true,"error":"slow down"}`},
		{500, `{"error":"x"}`, `null`},
		{200, `null`, `null`},
	}
	for _, c := range cases {
		api := &signInAPI{status: c.status, body: c.body}
		if got := toJSON(t, New(api).VerifyTOTPViaGo(context.Background(), "p", "c")); got != c.want {
			t.Errorf("status %d body %s: %s, want %s", c.status, c.body, got, c.want)
		}
	}
}

func TestTryGoServerAuth(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{200, `{"token":"t","role":"operator","auth_provider":"ldap"}`, `{"role":"operator","auth_provider":"ldap"}`},
		{200, `{"token":"t","role":"viewer"}`, `{"role":"viewer"}`},
		{200, `{"requires_2fa":true,"partial_token":"p"}`, `{"requires2fa":true}`},
		{200, `{"token":"t"}`, `null`},
		{200, `{}`, `null`},
		{200, `null`, `null`},
		{401, `{"error":"Invalid credentials"}`, `null`},
		{429, `{"error":"Too many login attempts"}`, `null`},
		{200, `not json`, `null`},
		// A disabled account whose credentials were right (L107)
		{403, `{"code":"account_disabled","error":"This account is disabled"}`, `{"account_disabled":true}`},
		{403, `{"error":"Forbidden"}`, `null`},
	}
	for _, c := range cases {
		api := &signInAPI{status: c.status, body: c.body}
		if got := toJSON(t, New(api).TryGoServerAuth(context.Background(), "u", "p")); got != c.want {
			t.Errorf("status %d body %s: %s, want %s", c.status, c.body, got, c.want)
		}
	}
	if got := New(&signInAPI{hang: true}, WithTimeout(20*time.Millisecond)).TryGoServerAuth(context.Background(), "u", "p"); got != nil {
		t.Errorf("no answer: %v, want nil", got)
	}
}

func TestTryLDAPVerifyOnGo(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{200, `{"authenticated":true}`, true},
		{200, `{"authenticated":false}`, false},
		{200, `{}`, false},
		{200, `null`, false},
		{401, `{"authenticated":true}`, false},
		{429, `{"error":"x"}`, false},
	}
	for _, c := range cases {
		api := &signInAPI{status: c.status, body: c.body}
		if got := New(api).TryLDAPVerifyOnGo(context.Background(), "u", "p"); got != c.want {
			t.Errorf("status %d body %s: %v, want %v", c.status, c.body, got, c.want)
		}
	}
}

func TestGetGoSSOStatusAndItsCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	api := &signInAPI{status: 200, body: `{"ldap_enabled":true,"oidc_enabled":false,"any_enabled":true}`}
	c := New(api)
	c.now = func() time.Time { return now }

	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":true,"oidc":false,"any":true}` {
		t.Fatalf("status = %s", got)
	}
	// Within a minute the answer is reused, also when it would change.
	api.body = `{"ldap_enabled":false}`
	now = now.Add(59 * time.Second)
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":true,"oidc":false,"any":true}` || api.calls != 1 {
		t.Errorf("after 59 s: %s, %d calls", got, api.calls)
	}
	now = now.Add(2 * time.Second)
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":false,"oidc":false,"any":false}` || api.calls != 2 {
		t.Errorf("after 61 s: %s, %d calls", got, api.calls)
	}
	// A caller that changes its copy does not change the kept answer.
	c.GetGoSSOStatus(ctx).Set("ldap", "changed")
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":false,"oidc":false,"any":false}` {
		t.Errorf("after a caller changed its copy: %s", got)
	}

	// Another status than 200 is all false, and kept as well.
	refused := &signInAPI{status: 500, body: `{"ldap_enabled":true}`}
	c = New(refused)
	c.now = func() time.Time { return now }
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":false,"oidc":false,"any":false}` {
		t.Errorf("status 500: %s", got)
	}
	refused.status = 200
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":false,"oidc":false,"any":false}` || refused.calls != 1 {
		t.Errorf("status 500 is not kept: %s, %d calls", got, refused.calls)
	}

	// No answer is all false and not kept.
	down := &signInAPI{hang: true}
	c = New(down, WithTimeout(20*time.Millisecond))
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":false,"oidc":false,"any":false}` {
		t.Errorf("no answer: %s", got)
	}
	down.set(func(s *signInAPI) { s.hang, s.status, s.body = false, 200, `{"oidc_enabled":1,"any_enabled":"yes"}` })
	if got := toJSON(t, c.GetGoSSOStatus(ctx)); got != `{"ldap":false,"oidc":true,"any":true}` || down.callCount() != 2 {
		t.Errorf("after no answer: %s, %d calls", got, down.callCount())
	}
}

func TestCheckGoServerHealthAndItsCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		status int
		want   bool
	}{{200, true}, {204, true}, {302, true}, {399, true}, {400, false}, {404, false}, {500, false}, {199, false}} {
		api := &signInAPI{status: c.status}
		if got := New(api).CheckGoServerHealth(ctx); got != c.want {
			t.Errorf("status %d: %v, want %v", c.status, got, c.want)
		}
	}

	api := &signInAPI{status: 200}
	c := New(api)
	c.now = func() time.Time { return now }
	if !c.CheckGoServerHealth(ctx) {
		t.Fatal("healthy server reported as down")
	}
	api.status = 500
	now = now.Add(9 * time.Second)
	if !c.CheckGoServerHealth(ctx) || api.calls != 1 {
		t.Errorf("after 9 s the kept result was not used (%d calls)", api.calls)
	}
	now = now.Add(2 * time.Second)
	if c.CheckGoServerHealth(ctx) || api.calls != 2 {
		t.Errorf("after 11 s the server was not asked again (%d calls)", api.calls)
	}
	// A failure is kept too.
	api.status = 200
	if c.CheckGoServerHealth(ctx) || api.calls != 2 {
		t.Errorf("the failure was not kept (%d calls)", api.calls)
	}

	// The check gives up after two seconds, whatever the client's timeout.
	slow := &signInAPI{hang: true}
	start := time.Now()
	if New(slow, WithTimeout(time.Minute)).CheckGoServerHealth(ctx) {
		t.Error("a server that does not answer is healthy")
	}
	if elapsed := time.Since(start); elapsed < 1900*time.Millisecond || elapsed > 10*time.Second {
		t.Errorf("gave up after %v, want two seconds", elapsed)
	}
}

func TestSignInTimeouts(t *testing.T) {
	short := New(nil, WithTimeout(2*time.Second))
	long := New(nil, WithTimeout(30*time.Second))
	none := New(nil, WithTimeout(0))
	if short.cappedTimeout() != 2*time.Second || long.cappedTimeout() != 5*time.Second || none.cappedTimeout() != 5*time.Second {
		t.Errorf("capped: %v %v %v", short.cappedTimeout(), long.cappedTimeout(), none.cappedTimeout())
	}
	if short.ownTimeout() != 2*time.Second || long.ownTimeout() != 30*time.Second || none.ownTimeout() != 3*time.Second {
		t.Errorf("own: %v %v %v", short.ownTimeout(), long.ownTimeout(), none.ownTimeout())
	}
}

func TestStartOIDCAuthorize(t *testing.T) {
	ctx := context.Background()
	redirect := func(status int, location string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if location != "" {
				w.Header().Set("Location", location)
			}
			w.WriteHeader(status)
			io.WriteString(w, `{"error":"OIDC is not enabled"}`)
		})
	}
	cases := []struct {
		name     string
		status   int
		location string
		want     string
	}{
		{"redirect to the provider", 302, "https://idp.example/auth?client_id=x", `{"success":true,"data":{"auth_url":"https://idp.example/auth?client_id=x"}}`},
		{"provider address on an error status", 500, "http://idp.example/auth", `{"success":true,"data":{"auth_url":"http://idp.example/auth"}}`},
		{"redirect to a path", 302, "/login?error=x", `{"success":false,"error":"OIDC authorize did not return an IdP redirect URL"}`},
		{"redirect to another scheme", 302, "javascript:alert(1)", `{"success":false,"error":"OIDC authorize did not return an IdP redirect URL"}`},
		{"no redirect", 200, "", `{"success":false,"error":"OIDC authorize did not return an IdP redirect URL"}`},
		{"error with the server's text", 400, "", `{"success":false,"error":"OIDC is not enabled"}`},
	}
	for _, c := range cases {
		if got := toJSON(t, New(redirect(c.status, c.location)).StartOIDCAuthorize(ctx, "/devices")); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	plain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) })
	if got := toJSON(t, New(plain).StartOIDCAuthorize(ctx, "")); got != `{"success":false,"error":"Request failed with status code 500"}` {
		t.Errorf("error without a text of the server: %s", got)
	}
}

func TestProxyOIDCCallback(t *testing.T) {
	ctx := context.Background()
	var request string
	var header http.Header
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, header = r.Method+" "+r.RequestURI, r.Header
		w.Header().Set("Location", "/login?oidc_code=abc")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Set-Cookie", "x=1")
		w.WriteHeader(302)
		io.WriteString(w, "<a>moved</a>")
	})
	query := url.Values{"code": {"c 1", "second"}, "state": {"s"}, "scope[]": {"openid"}, "nested[a]": {"x"}, "empty": {}}
	got := New(handler).ProxyOIDCCallback(ctx, query)
	if got.Status != 302 || got.Location != "/login?oidc_code=abc" || got.CacheControl != "no-store" ||
		got.ContentType != "text/html; charset=utf-8" || string(got.Body) != "<a>moved</a>" {
		t.Errorf("answer = %+v", got)
	}
	if request != "GET /api/auth/oidc/callback?code=c+1&scope=openid&state=s" {
		t.Errorf("request = %s", request)
	}
	if header.Get("Content-Type") != "" || !strings.HasPrefix(header.Get("Accept"), "text/html") {
		t.Errorf("headers = %v", header)
	}

	// Every status is an answer.
	failing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "state mismatch", 400) })
	got = New(failing).ProxyOIDCCallback(ctx, nil)
	if got.Status != 400 || string(got.Body) != "state mismatch\n" || got.ContentType != "text/plain; charset=utf-8" || got.Location != "" {
		t.Errorf("answer for a 400 = %+v", got)
	}

	got = New(&fakeAPI{answer: noAnswer}).ProxyOIDCCallback(ctx, nil)
	if got.Status != 502 || string(got.Body) != "OIDC callback proxy failed" || got.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("answer without the API server = %+v", got)
	}

	// The call may take at least 30 seconds.
	var deadline time.Duration
	timed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, _ := r.Context().Deadline()
		deadline = time.Until(d)
	})
	New(timed).ProxyOIDCCallback(ctx, nil)
	if deadline < 29*time.Second || deadline > 31*time.Second {
		t.Errorf("deadline with the default timeout = %v, want 30 s", deadline)
	}
	New(timed, WithTimeout(45*time.Second)).ProxyOIDCCallback(ctx, nil)
	if deadline < 44*time.Second || deadline > 46*time.Second {
		t.Errorf("deadline with a 45 s timeout = %v, want 45 s", deadline)
	}
}
