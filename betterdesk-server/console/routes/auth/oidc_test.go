package auth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/auth"
)

// provider makes the fake signal server speak single sign-on the way the
// real one does: authorize remembers a state with the way back and names
// the identity provider's address, the callback trades the provider's code
// and the state for a one-time code, and the exchange hands out the user
// of that code once.
type provider struct {
	mu sync.Mutex
	// states maps a state to the return path it was started with.
	states map[string]string
	// codes maps a one-time code to the exchange answer it is good for.
	codes map[string]string
	// user is the exchange answer without return_url, as JSON members.
	user string
	next int
}

func (w *world) provider(user string) *provider {
	p := &provider{states: map[string]string{}, codes: map[string]string{}, user: user}
	w.api.on("GET /api/auth/oidc/authorize", func(rw http.ResponseWriter, r *http.Request, _ string) {
		p.mu.Lock()
		p.next++
		state := fmt.Sprintf("state-%d", p.next)
		p.states[state] = r.URL.Query().Get("return_url")
		p.mu.Unlock()
		rw.Header().Set("Location", "https://idp.example/authorize?client_id=console&state="+state+"&nonce=n-"+state+
			"&redirect_uri="+url.QueryEscape("https://console.example/api/auth/oidc/callback"))
		rw.WriteHeader(http.StatusFound)
	})
	w.api.on("GET /api/auth/oidc/callback", func(rw http.ResponseWriter, r *http.Request, _ string) {
		q := r.URL.Query()
		p.mu.Lock()
		back, known := p.states[q.Get("state")]
		delete(p.states, q.Get("state"))
		p.next++
		code := fmt.Sprintf("once-%d", p.next)
		if known && q.Get("code") != "" {
			p.codes[code] = fmt.Sprintf(`{%s,"return_url":%q}`, p.user, back)
		}
		p.mu.Unlock()
		if !known || q.Get("code") == "" {
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.Header().Set("Cache-Control", "no-store")
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte("invalid or expired state\n"))
			return
		}
		rw.Header().Set("Location", "/api/auth/oidc/session?code="+code)
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("X-Not-Passed-On", "1")
		rw.WriteHeader(http.StatusFound)
	})
	w.api.on("POST /api/auth/oidc/exchange", func(rw http.ResponseWriter, _ *http.Request, body string) {
		var sent struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(body), &sent)
		p.mu.Lock()
		answer, ok := p.codes[sent.Code]
		delete(p.codes, sent.Code)
		p.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		if !ok {
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = rw.Write([]byte(`{"error":"invalid or expired code"}`))
			return
		}
		_, _ = rw.Write([]byte(answer))
	})
	return p
}

// pictures records what the profile picture sync was called with.
type pictures struct {
	mu    sync.Mutex
	calls []string
}

func (p *pictures) sync(userID int64, exchange *goapi.Object) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fmt.Sprintf("%d %s", userID, exchange.Str("picture")))
}

// singleSignOn takes a browser through authorize, the provider and the
// callback, and returns the answer of the session route.
func singleSignOn(t *testing.T, a *agent, returnURL string) answer {
	t.Helper()
	target := "/api/auth/oidc/authorize"
	if returnURL != "" {
		target += "?return_url=" + url.QueryEscape(returnURL)
	}
	res := a.get(target)
	location, err := url.Parse(res.header.Get("Location"))
	if res.status != http.StatusFound || err != nil || location.Host != "idp.example" {
		t.Fatalf("authorize: %d %q", res.status, res.header.Get("Location"))
	}
	// The provider signs the person in and sends the browser back
	res = a.get("/api/auth/oidc/callback?code=from-the-provider&state=" + location.Query().Get("state"))
	if res.status != http.StatusFound || !strings.HasPrefix(res.header.Get("Location"), "/api/auth/oidc/session?code=") {
		t.Fatalf("callback: %d %q %s", res.status, res.header.Get("Location"), res.body)
	}
	return a.get(res.header.Get("Location"))
}

func TestSingleSignOn(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		seen := &pictures{}
		auth.RegisterOIDCSession(w.s.Deps(), seen.sync)
		w.provider(`"token":"go-jwt","username":"sso-user","role":"operator","picture":"https://idp.example/me.png"`)
		a := w.agent("X-Client-Fingerprint", "fp-of-the-browser")
		a.setSession(`{"before":"sign-in"}`)
		before := a.sessionID()

		res := singleSignOn(t, a, "/devices?tab=1")
		if res.status != http.StatusFound || res.header.Get("Location") != "/devices?tab=1" {
			t.Fatalf("session: %d %q %s", res.status, res.header.Get("Location"), res.body)
		}
		if res.cookie("bd.guest") != clearedGuest || !strings.HasPrefix(res.cookie("bd.sid"), "bd.sid=s%3A") {
			t.Errorf("cookies of the session route: %q", res.cookies())
		}
		if a.sessionID() == before {
			t.Error("single sign-on kept the session id")
		}
		// The account was made for the user, bound to single sign-on
		id := w.int(`SELECT id FROM users WHERE username = 'sso-user'`)
		if row := w.rows(`SELECT role, auth_provider FROM users WHERE id = ?`, id); row[0] != "operator|oidc" {
			t.Errorf("the new account: %v", row)
		}
		if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, id); at == "" {
			t.Error("the sign-in is not noted on the account")
		}
		// The session carries what other code reads only of such a session
		wantStored := regexp.MustCompile(`^\{"cookie":\{[^}]+\},` +
			fmt.Sprintf(`"userId":%d,"user":\{"id":%d,"username":"sso-user","role":"operator","preferred_language":null\},`, id, id) +
			`"createdAt":\d{13},"lastActivityAt":\d{13},"loginIp":"127\.0\.0\.1","loginUserAgent":"auth-test/1","clientFingerprint":"fp-of-the-browser",` +
			`"username":"sso-user","role":"operator","goToken":"go-jwt","authMethod":"oidc"\}$`)
		if stored := w.stored(a); !wantStored.MatchString(stored) {
			t.Errorf("stored session:\n%s", stored)
		}
		res = a.get("/api/auth/verify")
		if res.status != http.StatusOK || !strings.HasPrefix(res.body, fmt.Sprintf(`{"success":true,"user":{"id":%d,"username":"sso-user","role":"operator","preferred_language":null},`, id)) {
			t.Errorf("verify: %d %s", res.status, res.body)
		}
		equal(t, "audit", w.audit(), []string{
			fmt.Sprintf("login|User logged in via OIDC; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|%d", id),
		})
		equal(t, "picture sync", seen.calls, []string{fmt.Sprintf("%d https://idp.example/me.png", id)})
		// Single sign-on is no password attempt
		if rows := w.attempts(); len(rows) != 0 {
			t.Errorf("attempts: %v", rows)
		}
		// The signal server was asked with the way back, and got the provider's answer
		if calls := w.api.seen("GET /api/auth/oidc/authorize"); len(calls) != 1 || calls[0].Query != "return_url=%2Fdevices%3Ftab%3D1" {
			t.Errorf("authorize call: %+v", calls)
		}
		if calls := w.api.seen("GET /api/auth/oidc/callback"); len(calls) != 1 || calls[0].Query != "code=from-the-provider&state=state-1" {
			t.Errorf("callback call: %+v", calls)
		}

		// A second sign-on from another browser ends the first session and
		// brings the stored role in line with the provider's
		w.exec(`UPDATE users SET role = 'viewer' WHERE id = ?`, id)
		res = singleSignOn(t, w.agent(), "")
		if res.status != http.StatusFound || res.header.Get("Location") != "/" {
			t.Errorf("second sign-on: %d %q", res.status, res.header.Get("Location"))
		}
		if role := w.text(`SELECT role FROM users WHERE id = ?`, id); role != "operator" {
			t.Errorf("stored role after the second sign-on: %q", role)
		}
		rows := w.audit()
		equal(t, "audit of the second", rows[1:], []string{
			fmt.Sprintf("login|User logged in via OIDC; ip=127.0.0.1; ua=auth-test/1; replaced=1 prior session(s)|127.0.0.1|%d", id),
			fmt.Sprintf("session_replaced|OIDC login replaced 1 prior session(s); newIp=127.0.0.1|127.0.0.1|%d", id),
		})
		want(t, "the first browser", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
	})
}

func TestSingleSignOnRolesAndWaysBack(t *testing.T) {
	w := newWorld(t, nil, setup{})
	auth.RegisterOIDCSession(w.s.Deps(), nil)
	stored := w.addUser(user{name: "known", role: "global_admin", language: "fr"})
	pro := w.addUser(user{name: "desk", role: "pro"})

	// Without a role from the provider the stored one counts, and a new
	// account is a viewer
	w.provider(`"token":"t1","username":"known"`)
	a := w.agent()
	singleSignOn(t, a, "//evil.example/")
	if s := w.stored(a); !strings.Contains(s, fmt.Sprintf(`"user":{"id":%d,"username":"known","role":"global_admin","preferred_language":"fr"}`, stored)) ||
		!strings.Contains(s, `"username":"known","role":"global_admin","goToken":"t1","authMethod":"oidc"}`) {
		t.Errorf("stored session of a known account: %s", s)
	}
	if row := w.rows(`SELECT role, auth_provider FROM users WHERE id = ?`, stored); row[0] != "global_admin|oidc" {
		t.Errorf("the known account after sign-on: %v", row)
	}
	w.provider(`"token":"t2","username":"fresh","role":""`)
	b := w.agent()
	singleSignOn(t, b, "/login")
	if s := w.stored(b); !strings.Contains(s, `"username":"fresh","role":"viewer","goToken":"t2","authMethod":"oidc"}`) {
		t.Errorf("stored session of a new account: %s", s)
	}
	if role := w.text(`SELECT role FROM users WHERE username = 'fresh'`); role != "viewer" {
		t.Errorf("role of the new account: %q", role)
	}
	// An account for the desktop client is signed in here all the same
	w.provider(`"token":"t3","username":"desk"`)
	c := w.agent()
	res := singleSignOn(t, c, "/devices")
	if res.header.Get("Location") != "/devices" || !strings.Contains(w.stored(c), fmt.Sprintf(`"userId":%d,`, pro)) {
		t.Errorf("sign-on of a pro account: %q %s", res.header.Get("Location"), w.stored(c))
	}

	// The way back is checked when the sign-on starts and again when it ends
	calls := w.api.seen("GET /api/auth/oidc/authorize")
	var asked []string
	for _, call := range calls {
		asked = append(asked, call.Query)
	}
	equal(t, "ways back the signal server was given", asked, []string{"return_url=%2F", "return_url=%2F", "return_url=%2Fdevices"})
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t4","username":"known","return_url":"https://evil.example/"}`)
	if res := w.agent().get("/api/auth/oidc/session?code=x"); res.status != http.StatusFound || res.header.Get("Location") != "/" {
		t.Errorf("an unsafe way back from the signal server: %d %q", res.status, res.header.Get("Location"))
	}
}

func TestSingleSignOnRefusals(t *testing.T) {
	w := newWorld(t, nil, setup{})
	auth.RegisterOIDCSession(w.s.Deps(), nil)
	a := w.agent()
	invalid := func(what string, res answer) {
		t.Helper()
		if res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_invalid" {
			t.Errorf("%s: %d %q", what, res.status, res.header.Get("Location"))
		}
	}
	invalid("no code", a.get("/api/auth/oidc/session"))
	invalid("empty code", a.get("/api/auth/oidc/session?code="))
	invalid("code twice", a.get("/api/auth/oidc/session?code=a&code=b"))
	invalid("code as a list", a.get("/api/auth/oidc/session?code[]=a"))
	if calls := w.api.seen("POST /api/auth/oidc/exchange"); len(calls) != 0 {
		t.Errorf("the signal server was asked without a code: %+v", calls)
	}
	// The signal server does not know the code, or answers without a token or a name
	invalid("unknown code", a.get("/api/auth/oidc/session?code=nope"))
	if calls := w.api.seen("POST /api/auth/oidc/exchange"); len(calls) != 1 || calls[0].Body != `{"code":"nope"}` {
		t.Errorf("exchange call: %+v", calls)
	}
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"username":"x","role":"admin"}`)
	invalid("no token", a.get("/api/auth/oidc/session?code=c"))
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":""}`)
	invalid("no user name", a.get("/api/auth/oidc/session?code=c"))
	if n := w.int(`SELECT COUNT(*) FROM users`); n != 0 {
		t.Errorf("%d accounts after refused sign-ons", n)
	}
	if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 0 {
		t.Errorf("%d signed-in sessions after refused sign-ons", n)
	}

	// A code used once is gone
	w.provider(`"token":"t","username":"once"`)
	b := w.agent()
	res := b.get("/api/auth/oidc/authorize")
	state, _ := url.Parse(res.header.Get("Location"))
	res = b.get("/api/auth/oidc/callback?code=p&state=" + state.Query().Get("state"))
	session := res.header.Get("Location")
	if got := b.get(session); got.header.Get("Location") != "/" {
		t.Fatalf("first use of the code: %d %q", got.status, got.header.Get("Location"))
	}
	invalid("second use of the code", w.agent().get(session))

	// When the account cannot be made the sign-on fails
	w.exec(`DROP TABLE users`)
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"nobody"}`)
	if res := w.agent().get("/api/auth/oidc/session?code=c"); res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_error" {
		t.Errorf("without a users table: %d %q", res.status, res.header.Get("Location"))
	}
}

func TestOIDCAuthorize(t *testing.T) {
	w := newWorld(t, nil, setup{})
	a := w.agent()
	failed := func(what string, res answer) {
		t.Helper()
		if res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_error" {
			t.Errorf("%s: %d %q", what, res.status, res.header.Get("Location"))
		}
	}
	// Single sign-on is off: the signal server answers without a redirect
	failed("not configured", a.get("/api/auth/oidc/authorize"))
	w.api.json("GET /api/auth/oidc/authorize", 400, `{"error":"OIDC is not enabled"}`)
	failed("refused", a.get("/api/auth/oidc/authorize"))
	// A redirect that is no address of a provider does not reach the browser
	for _, location := range []string{"/relative", "javascript:alert(1)", ""} {
		w.api.on("GET /api/auth/oidc/authorize", func(rw http.ResponseWriter, _ *http.Request, _ string) {
			if location != "" {
				rw.Header().Set("Location", location)
			}
			rw.WriteHeader(http.StatusFound)
		})
		failed("Location "+location, a.get("/api/auth/oidc/authorize"))
	}

	w.provider(`"token":"t","username":"u"`)
	for query, wantReturn := range map[string]string{
		"":                                 "/",
		"?return_url=%2Fdevices%3Ftab%3D1": "/devices?tab=1",
		"?return_url=/a+b":                 "/a%20b",
		"?return_url=https%3A%2F%2Fevil.example%2F":  "/",
		"?return_url=%2F%2Fevil.example":             "/",
		"?return_url=%2F..%2F%2Fevil.example":        "/",
		"?return_url=%2Fapi%2Fusers":                 "/",
		"?return_url=%2Flogin":                       "/",
		"?return_url=%2Fa&return_url=%2Fb":           "/",
		"?return_url[]=%2Fa":                         "/",
		"?return_url[x]=%2Fa":                        "/",
		"?return_url=":                               "/",
		"?other=1":                                   "/",
		"?return%5Furl=%2Fdevices":                   "/devices",
		"?return_url=%2Fdevices&return_url[]=%2Fb":   "/",
		"?return_url=%2Fbad%ZZescape":                "/",
		"?return_url=%2Fdevices%0A":                  "/",
		"?return_url=" + strings.Repeat("%2Fa", 600): "/" + strings.Repeat("a/", 599) + "a",
	} {
		before := len(w.api.seen("GET /api/auth/oidc/authorize"))
		res := a.get("/api/auth/oidc/authorize" + query)
		if res.status != http.StatusFound || !strings.HasPrefix(res.header.Get("Location"), "https://idp.example/authorize?client_id=console&state=") {
			t.Errorf("%q: %d %q", query, res.status, res.header.Get("Location"))
			continue
		}
		calls := w.api.seen("GET /api/auth/oidc/authorize")
		got, _ := url.ParseQuery(calls[before].Query)
		if got.Get("return_url") != wantReturn {
			t.Errorf("%q: the signal server got the way back %q, want %q", query, got.Get("return_url"), wantReturn)
		}
	}
}

func TestOIDCCallback(t *testing.T) {
	w := newWorld(t, nil, setup{})
	a := w.agent()
	// The signal server does not answer
	w.api.on("GET /api/auth/oidc/callback", func(http.ResponseWriter, *http.Request, string) { panic(http.ErrAbortHandler) })
	for _, target := range []string{"/api/auth/oidc/callback?code=c&state=s", "/api/oidc/callback?code=c&state=s"} {
		res := a.get(target)
		if res.status != http.StatusBadGateway || res.body != "OIDC callback proxy failed" || res.header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("%s without an answer: %d %q %q", target, res.status, res.header.Get("Content-Type"), res.body)
		}
	}

	w.provider(`"token":"t","username":"u"`)
	// A state the signal server does not know: its refusal goes to the browser as it is
	for _, target := range []string{"/api/auth/oidc/callback?code=c&state=unknown", "/api/oidc/callback?code=c&state=unknown", "/api/auth/oidc/callback"} {
		res := a.get(target)
		if res.status != http.StatusBadRequest || res.body != "invalid or expired state\n" ||
			res.header.Get("Content-Type") != "text/plain; charset=utf-8" || res.header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %q %q %q", target, res.status, res.header.Get("Content-Type"), res.header.Get("Cache-Control"), res.body)
		}
	}
	// A good one: status, Location and Cache-Control are passed on, other headers are not
	res := a.get("/api/auth/oidc/authorize")
	state, _ := url.Parse(res.header.Get("Location"))
	res = a.get("/api/oidc/callback?state=" + state.Query().Get("state") + "&code=a%20b&code=second&extra[]=1&nested[x]=2")
	if res.status != http.StatusFound || !strings.HasPrefix(res.header.Get("Location"), "/api/auth/oidc/session?code=once-") ||
		res.header.Get("Cache-Control") != "no-store" || res.header.Get("X-Not-Passed-On") != "" || res.body != "" {
		t.Errorf("callback: %d %q %q %q", res.status, res.header.Get("Location"), res.header.Get("Cache-Control"), res.body)
	}
	// The query went on with the first value of each parameter
	calls := w.api.seen("GET /api/auth/oidc/callback")
	if got, _ := url.ParseQuery(calls[len(calls)-1].Query); got.Get("code") != "a b" || len(got["code"]) != 1 || got.Get("extra") != "1" || got.Has("nested[x]") || got.Has("nested") {
		t.Errorf("query passed to the signal server: %q", calls[len(calls)-1].Query)
	}
	// The callback itself signs nobody in
	if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 0 {
		t.Errorf("%d signed-in sessions after callbacks", n)
	}
}

func TestOIDCStatus(t *testing.T) {
	const off = `{"enabled":false}`
	w := newWorld(t, nil, setup{})
	a := w.agent()
	want(t, "no answer of the signal server", a.get("/api/auth/oidc/status"), http.StatusOK, off)
	w.api.json("GET /api/auth/oidc/status", 500, `{"error":"down"}`)
	want(t, "an error of the signal server", a.get("/api/auth/oidc/status"), http.StatusOK, off)
	w.api.json("GET /api/auth/oidc/status", 200, `{"enabled":true,"display_name":"Example IdP","prefer_sso":true}`)
	// A client on a private network may still sign in with a password
	want(t, "from loopback", a.get("/api/auth/oidc/status"), http.StatusOK, `{"enabled":true,"display_name":"Example IdP","prefer_sso":true,"local_bypass":true}`)
	// A forwarded address counts only from a proxy the console trusts
	want(t, "forwarded, not trusted", a.get("/api/auth/oidc/status", "X-Forwarded-For", "203.0.113.25"), http.StatusOK,
		`{"enabled":true,"display_name":"Example IdP","prefer_sso":true,"local_bypass":true}`)
	// What the signal server says about local_bypass itself does not count
	w.api.json("GET /api/auth/oidc/status", 200, `{"local_bypass":false,"enabled":true}`)
	want(t, "its own local_bypass", a.get("/api/auth/oidc/status"), http.StatusOK, `{"local_bypass":true,"enabled":true}`)

	trusting := newWorld(t, nil, setup{trustProxy: "loopback"})
	trusting.api.json("GET /api/auth/oidc/status", 200, `{"enabled":true,"prefer_sso":true}`)
	for address, local := range map[string]bool{
		"203.0.113.25": false, "10.1.2.3": true, "172.16.0.1": true, "172.32.0.1": false, "192.168.1.1": true,
		"::1": true, "fd00::1": true, "2001:db8::1": false,
	} {
		wantBody := fmt.Sprintf(`{"enabled":true,"prefer_sso":true,"local_bypass":%t}`, local)
		want(t, "from "+address, trusting.agentFrom(address).get("/api/auth/oidc/status"), http.StatusOK, wantBody)
	}
}

func TestSingleSignOnWhenTheAccountCannotBeWritten(t *testing.T) {
	w := newWorld(t, nil, setup{})
	auth.RegisterOIDCSession(w.s.Deps(), nil)
	known := w.addUser(user{name: "known", role: "viewer"})
	blank := w.addUser(user{name: "blank"})
	w.exec(`UPDATE users SET role = '' WHERE id = ?`, blank)
	w.exec(`CREATE TRIGGER roles_are_fixed BEFORE UPDATE OF role ON users BEGIN SELECT RAISE(ABORT, 'roles are fixed'); END`)

	// The role of the session is the provider's, also when the stored one
	// could not be brought in line
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"known","role":"global_admin"}`)
	a := w.agent()
	if res := a.get("/api/auth/oidc/session?code=c"); res.status != http.StatusFound || res.header.Get("Location") != "/" {
		t.Fatalf("sign-on: %d %q", res.status, res.header.Get("Location"))
	}
	if role := w.text(`SELECT role FROM users WHERE id = ?`, known); role != "viewer" {
		t.Fatalf("the stored role changed to %q: the test does not test what it means to", role)
	}
	if s := w.stored(a); !strings.Contains(s, fmt.Sprintf(`"user":{"id":%d,"username":"known","role":"global_admin",`, known)) ||
		!strings.Contains(s, `"username":"known","role":"global_admin","goToken":"t","authMethod":"oidc"}`) {
		t.Errorf("stored session: %s", s)
	}
	// An account without a role, and a provider that names none: a viewer
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"blank"}`)
	b := w.agent()
	b.get("/api/auth/oidc/session?code=c")
	if s := w.stored(b); !strings.Contains(s, fmt.Sprintf(`"user":{"id":%d,"username":"blank","role":"viewer",`, blank)) {
		t.Errorf("stored session of an account without a role: %s", s)
	}

	// A user whose account cannot be made gets no session
	w.exec(`CREATE TRIGGER no_new_accounts BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT, 'no new accounts'); END`)
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"stranger","role":"admin"}`)
	c := w.agent()
	if res := c.get("/api/auth/oidc/session?code=c"); res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_error" {
		t.Errorf("sign-on without an account: %d %q", res.status, res.header.Get("Location"))
	}
	if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 2 {
		t.Errorf("%d signed-in sessions, want the two from before", n)
	}
}
