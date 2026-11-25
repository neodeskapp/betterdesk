package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
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

// holdsCode gives the agent the cookie that ties a single sign-on to the
// browser, as the callback sets it for the one-time code.
func (a *agent) holdsCode(code string) *agent {
	a.cookies["nd.oidc"] = code
	return a
}

// pngPicture is a PNG of one pixel as the data URL a provider can send as
// "picture".
const pngPicture = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC"

// picture waits for the profile picture sync of an account and returns its
// stored picture as "source|type|version", "" when it has none.
func (w *world) picture(userID int64) string {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w.s.Deps().Avatars.WaitForSync(ctx, userID)
	rows := w.rows(`SELECT source, mime_type, version FROM user_avatars WHERE user_id = ?`, userID)
	if len(rows) == 0 {
		return ""
	}
	return rows[0]
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
	// One session per account, so the second sign-on replaces the first
	eachWorld(t, setup{env: map[string]string{"SESSION_MAX_PER_USER": "1"}}, func(t *testing.T, w *world) {
		w.provider(`"token":"go-jwt","username":"sso-user","role":"operator","picture":"` + pngPicture + `"`)
		a := w.agent("X-Client-Fingerprint", "fp-of-the-browser")
		a.setSession(`{"before":"sign-in"}`)
		before := a.sessionID()

		res := singleSignOn(t, a, "/devices?tab=1")
		if res.status != http.StatusFound || res.header.Get("Location") != "/devices?tab=1" {
			t.Fatalf("session: %d %q %s", res.status, res.header.Get("Location"), res.body)
		}
		if res.cookie("nd.guest") != clearedGuest || !strings.HasPrefix(res.cookie("nd.sid"), "nd.sid=s%3A") {
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
		// The provider's picture became the account's
		if got := w.picture(id); !strings.HasPrefix(got, "oidc|image/png|") {
			t.Errorf("profile picture after the sign-on: %q", got)
		}
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
		want(t, "the first browser", a.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_replaced", replacedElsewhere))
	})
}

func TestSingleSignOnRolesAndWaysBack(t *testing.T) {
	w := newWorld(t, nil, setup{})
	stored := w.addUser(user{name: "known", role: "global_admin", language: "fr", provider: "oidc"})

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
	// The way back is checked when the sign-on starts and again when it ends
	calls := w.api.seen("GET /api/auth/oidc/authorize")
	var asked []string
	for _, call := range calls {
		asked = append(asked, call.Query)
	}
	equal(t, "ways back the signal server was given", asked, []string{"return_url=%2F", "return_url=%2F"})
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t4","username":"known","return_url":"https://evil.example/"}`)
	if res := w.agent().holdsCode("x").get("/api/auth/oidc/session?code=x"); res.status != http.StatusFound || res.header.Get("Location") != "/" {
		t.Errorf("an unsafe way back from the signal server: %d %q", res.status, res.header.Get("Location"))
	}
}

// A single sign-on whose name belongs to a local or directory account gets
// the answer of the password sign-in's collision and no session; the
// account stays as it was (RV116).
func TestSingleSignOnLeavesOtherAccountsAlone(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		local := w.addUser(user{name: "admin", role: "admin"})
		directory := w.addUser(user{name: "carol", role: "operator", provider: "ldap"})
		for _, tc := range []struct {
			name string
			id   int64
			row  string
		}{{"admin", local, "admin|local"}, {"carol", directory, "operator|ldap"}} {
			w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"`+tc.name+`","role":"viewer"}`)
			a := w.agent().holdsCode("c")
			res := a.get("/api/auth/oidc/session?code=c")
			if res.status != http.StatusFound || res.header.Get("Location") != "/login?error=username_collision" {
				t.Errorf("%s: %d %q", tc.name, res.status, res.header.Get("Location"))
			}
			if row := w.rows(`SELECT role, auth_provider FROM users WHERE id = ?`, tc.id); row[0] != tc.row {
				t.Errorf("%s afterwards: %v", tc.name, row)
			}
			want(t, tc.name+" verify", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		}
		if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 0 {
			t.Errorf("%d signed-in sessions", n)
		}
		if rows := w.audit(); len(rows) != 0 {
			t.Errorf("audit: %v", rows)
		}
	})
}

func TestSingleSignOnRefusals(t *testing.T) {
	w := newWorld(t, nil, setup{})
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
	invalid("unknown code", a.holdsCode("nope").get("/api/auth/oidc/session?code=nope"))
	if calls := w.api.seen("POST /api/auth/oidc/exchange"); len(calls) != 1 || calls[0].Body != `{"code":"nope"}` {
		t.Errorf("exchange call: %+v", calls)
	}
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"username":"x","role":"admin"}`)
	invalid("no token", a.holdsCode("c").get("/api/auth/oidc/session?code=c"))
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":""}`)
	invalid("no user name", a.holdsCode("c").get("/api/auth/oidc/session?code=c"))
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
	invalid("second use of the code", w.agent().holdsCode(strings.TrimPrefix(session, "/api/auth/oidc/session?code=")).get(session))

	// When the account cannot be made the sign-on fails
	w.exec(`DROP TABLE users`)
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"nobody"}`)
	if res := w.agent().holdsCode("c").get("/api/auth/oidc/session?code=c"); res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_error" {
		t.Errorf("without a users table: %d %q", res.status, res.header.Get("Location"))
	}
}

// oidcCookieLine is the Set-Cookie line that ties a single sign-on to the
// browser.
func oidcCookieLine(value string) string {
	return "nd.oidc=" + value + "; Max-Age=600; Path=/api; HttpOnly; SameSite=Lax"
}

// A single sign-on counts only in the browser that started it: authorize
// gives the browser a short-lived cookie with the state, the callback
// passes a sign-in on only to that browser and swaps the cookie for the
// one-time code, and the session route wants the cookie (RV120).
func TestSingleSignOnNeedsTheBrowserThatStartedIt(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.provider(`"token":"t","username":"mallory","role":"admin"`)
		attacker, victim := w.agent(), w.agent()
		res := attacker.get("/api/auth/oidc/authorize")
		location, _ := url.Parse(res.header.Get("Location"))
		state := location.Query().Get("state")
		if got := res.cookie("nd.oidc"); got != oidcCookieLine(state) {
			t.Errorf("the cookie of authorize: %q", got)
		}
		// The attacker's callback address opened in another browser
		res = victim.get("/api/auth/oidc/callback?code=from-the-provider&state=" + state)
		if res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_invalid" {
			t.Errorf("callback in another browser: %d %q", res.status, res.header.Get("Location"))
		}
		// A cookie of another state does not do either
		res = attacker.get("/api/auth/oidc/authorize")
		location, _ = url.Parse(res.header.Get("Location"))
		state = location.Query().Get("state")
		victim.cookies["nd.oidc"] = "state-of-another-sign-in"
		if res = victim.get("/api/auth/oidc/callback?code=p&state=" + state); res.header.Get("Location") != "/login?error=oidc_invalid" {
			t.Errorf("callback with the cookie of another state: %d %q", res.status, res.header.Get("Location"))
		}
		delete(victim.cookies, "nd.oidc")
		// The attacker passes the callback and sends the session address on
		res = attacker.get("/api/auth/oidc/authorize")
		location, _ = url.Parse(res.header.Get("Location"))
		res = attacker.get("/api/auth/oidc/callback?code=p&state=" + location.Query().Get("state"))
		session := res.header.Get("Location")
		code := strings.TrimPrefix(session, "/api/auth/oidc/session?code=")
		if res.status != http.StatusFound || code == session || res.cookie("nd.oidc") != oidcCookieLine(code) {
			t.Fatalf("callback in the attacker's browser: %d %q %q", res.status, session, res.cookies())
		}
		exchanges := len(w.api.seen("POST /api/auth/oidc/exchange"))
		res = victim.get(session)
		if res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_invalid" {
			t.Errorf("session address in another browser: %d %q", res.status, res.header.Get("Location"))
		}
		if n := len(w.api.seen("POST /api/auth/oidc/exchange")); n != exchanges {
			t.Errorf("the code was exchanged for a browser without the cookie")
		}
		want(t, "the victim", victim.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 0 {
			t.Errorf("%d signed-in sessions", n)
		}
		// In the browser that started it the sign-in goes through, and the cookie is used up
		res = attacker.get(session)
		if res.status != http.StatusFound || res.header.Get("Location") != "/" {
			t.Errorf("session in the right browser: %d %q", res.status, res.header.Get("Location"))
		}
		if got := res.cookie("nd.oidc"); got != "nd.oidc=; Path=/api; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Lax" {
			t.Errorf("the session route left the cookie: %q", got)
		}

		// A Redirect URL on the signal server's port: the console sees no
		// callback, and the exchange names the state the browser holds
		w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"mallory","state":"state-x"}`)
		direct := w.agent()
		direct.cookies["nd.oidc"] = "state-x"
		if res := direct.get("/api/auth/oidc/session?code=c"); res.header.Get("Location") != "/" {
			t.Errorf("the state of the exchange: %d %q", res.status, res.header.Get("Location"))
		}
		other := w.agent()
		other.cookies["nd.oidc"] = "state-y"
		if res := other.get("/api/auth/oidc/session?code=c"); res.header.Get("Location") != "/login?error=oidc_invalid" {
			t.Errorf("another state than the exchange's: %d %q", res.status, res.header.Get("Location"))
		}
		want(t, "another state", other.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)

		// The desktop client's sign-in started at the signal server: its
		// result page goes to the browser without a cookie
		w.api.on("GET /api/auth/oidc/callback", func(rw http.ResponseWriter, _ *http.Request, _ string) {
			rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = rw.Write([]byte("<p>Sign-in successful</p>"))
		})
		if res := w.agent().get("/api/auth/oidc/callback?code=p&state=client-poll"); res.status != http.StatusOK || res.body != "<p>Sign-in successful</p>" {
			t.Errorf("the client's result page: %d %q", res.status, res.body)
		}
	})
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
	known := w.addUser(user{name: "known", role: "viewer", provider: "oidc"})
	blank := w.addUser(user{name: "blank", provider: "oidc"})
	w.exec(`UPDATE users SET role = '' WHERE id = ?`, blank)
	w.exec(`CREATE TRIGGER roles_are_fixed BEFORE UPDATE OF role ON users BEGIN SELECT RAISE(ABORT, 'roles are fixed'); END`)

	// The role of the session is the provider's, also when the stored one
	// could not be brought in line
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"known","role":"global_admin"}`)
	a := w.agent().holdsCode("c")
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
	b := w.agent().holdsCode("c")
	b.get("/api/auth/oidc/session?code=c")
	if s := w.stored(b); !strings.Contains(s, fmt.Sprintf(`"user":{"id":%d,"username":"blank","role":"viewer",`, blank)) {
		t.Errorf("stored session of an account without a role: %s", s)
	}

	// A user whose account cannot be made gets no session
	w.exec(`CREATE TRIGGER no_new_accounts BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT, 'no new accounts'); END`)
	w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"t","username":"stranger","role":"admin"}`)
	c := w.agent().holdsCode("c")
	if res := c.get("/api/auth/oidc/session?code=c"); res.status != http.StatusFound || res.header.Get("Location") != "/login?error=oidc_error" {
		t.Errorf("sign-on without an account: %d %q", res.status, res.header.Get("Location"))
	}
	if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id IS NOT NULL`); n != 2 {
		t.Errorf("%d signed-in sessions, want the two from before", n)
	}
}

func TestSingleSignOnProfilePicture(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		signOn := func(picture string) (answer, int64) {
			t.Helper()
			w.api.json("POST /api/auth/oidc/exchange", 200, `{"token":"jwt","username":"alice","role":"operator","return_url":"/devices"`+picture+`}`)
			res := w.agent().holdsCode("one-time").get("/api/auth/oidc/session?code=one-time")
			if res.status != http.StatusFound || res.header.Get("Location") != "/devices" {
				t.Fatalf("sign-on with picture %q: %d %q", picture, res.status, res.header.Get("Location"))
			}
			return res, w.int(`SELECT id FROM users WHERE username = 'alice'`)
		}
		// The first sign-on stores the provider's picture
		_, id := signOn(`,"picture":"` + pngPicture + `"`)
		first := w.picture(id)
		if !strings.HasPrefix(first, "oidc|image/png|") {
			t.Fatalf("picture after the first sign-on: %q", first)
		}
		// A server that sends no picture field leaves it alone
		signOn("")
		if got := w.picture(id); got != first {
			t.Errorf("picture after a sign-on without the field: %q, want %q", got, first)
		}
		// A picture the console refuses keeps the old one, and the sign-on goes on
		signOn(`,"picture":"data:image/png;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+"`)
		if got := w.picture(id); got != first {
			t.Errorf("picture after a refused one: %q, want %q", got, first)
		}
		// An empty claim removes it
		signOn(`,"picture":""`)
		if got := w.picture(id); got != "" {
			t.Errorf("picture after an empty claim: %q", got)
		}
		// A picture that cannot be fetched stops nothing
		signOn(`,"picture":"http://127.0.0.1:9/a.png"`)
		if got := w.picture(id); got != "" {
			t.Errorf("picture after a failed fetch: %q", got)
		}
	})
}

// TestSingleSignOnNamesTheBrowsersAddress: the console hands the identity
// provider's callback to the API server with the browser's address as the
// sign-in's address, so the server's audit entry of the sign-in names the
// browser, as an LDAP sign-in does, not 127.0.0.1 (L357).
func TestSingleSignOnNamesTheBrowsersAddress(t *testing.T) {
	w := newWorld(t, nil, setup{trustProxy: "loopback"})
	w.provider(`"username":"oadm","role":"admin"`)
	singleSignOn(t, w.agentFrom("203.0.113.7"), "")
	if calls := w.api.seen("GET /api/auth/oidc/callback"); len(calls) != 1 || calls[0].Addr != "203.0.113.7" {
		t.Errorf("the API server got the callback with the address %+v, want the browser's 203.0.113.7", calls)
	}
}
