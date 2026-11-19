package auth_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

const signedInAlice = `{"success":true,"user":{"username":"alice","role":"operator"},"redirect":"/","session":{"replacedPriorSessions":0,"idleTimeoutMs":1800000,"maxAgeMs":86400000}}`

func login(name, pass string) string {
	return fmt.Sprintf(`{"username":%q,"password":%q}`, name, pass)
}

func TestLoginRefusesWhatIsNoCredentials(t *testing.T) {
	w := newWorld(t, nil, setup{})
	w.addUser(user{name: "alice"})
	long := strings.Repeat("a", 129)
	// 65 characters outside the BMP are 130 UTF-16 units
	wide := strings.Repeat("\U0001F600", 65)
	for name, body := range map[string]string{
		"no body members":         `{}`,
		"no username":             `{"password":"x"}`,
		"no password":             `{"username":"alice"}`,
		"empty username":          `{"username":"","password":"x"}`,
		"empty password":          `{"username":"alice","password":""}`,
		"null password":           `{"username":"alice","password":null}`,
		"zero as password":        `{"username":"alice","password":0}`,
		"false as username":       `{"username":false,"password":"x"}`,
		"a list as body":          `["alice","x"]`,
		"username of 129":         login(long, password),
		"password of 129":         login("alice", long),
		"username of 130 units":   login(wide, password),
		"a list of 129 as name":   `{"username":[` + strings.Repeat("1,", 128) + `1],"password":"x"}`,
		"an object with a length": `{"username":{"length":129},"password":"x"}`,
		"a length as text":        `{"username":"alice","password":{"length":"200"}}`,
	} {
		want(t, name, w.agent().post("/api/auth/login", body), http.StatusBadRequest, invalidCredentials)
	}
	if rows := w.audit(); len(rows) != 0 {
		t.Errorf("a refused body wrote audit entries: %v", rows)
	}
	if rows := w.attempts(); len(rows) != 0 {
		t.Errorf("a refused body recorded attempts: %v", rows)
	}
	// 128 is still a user name: it passes the check and is not known
	want(t, "username of 128", w.agent().post("/api/auth/login", login(strings.Repeat("a", 128), password)), http.StatusUnauthorized, invalidCredentials)
}

func TestLoginWithWrongCredentials(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		w.addUser(user{name: "sso", provider: "oidc"})
		a := w.agent()

		res := a.post("/api/auth/login", login("alice", "not-the-password"))
		want(t, "wrong password", res, http.StatusUnauthorized, invalidCredentials)
		if len(res.cookies()) != 0 {
			t.Errorf("a refused sign-in set cookies: %v", res.cookies())
		}
		want(t, "unknown user", a.post("/api/auth/login", login("nobody", password)), http.StatusUnauthorized, invalidCredentials)
		want(t, "single sign-on account", a.post("/api/auth/login", login("sso", password)), http.StatusUnauthorized, invalidCredentials)
		want(t, "blank username", a.post("/api/auth/login", login("  ", password)), http.StatusUnauthorized, invalidCredentials)

		equal(t, "audit", w.audit(), []string{
			"login_failed|Username: a***e; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0",
			"login_failed|Username: n***y; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0",
			"login_failed|Username: s***o; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0",
			"login_failed|Username:   ; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0",
		})
		equal(t, "attempts", w.attempts(), []string{
			"alice|127.0.0.1|0", "nobody|127.0.0.1|0", "sso|127.0.0.1|0", "  |127.0.0.1|0",
		})
		if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, alice); at != "" {
			t.Errorf("a refused sign-in noted a sign-in on the account: %q", at)
		}
	})
}

func TestLoginWithValuesThatAreNoText(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.addUser(user{name: "alice"})
		w.addUser(user{name: "sso", provider: "oidc"})
		w.addUser(user{name: "dora", provider: "ldap"})
		a := w.agent()
		post := func(body string) answer { return a.post("/api/auth/login", body) }

		// A user name that is not text is refused like an unknown one. On
		// SQLite the lockout query cannot take a boolean, a list that holds
		// a list, or an object: a server error before the password step.
		refused := func(what, body string, sqliteStatus int, sqliteBody string) {
			t.Helper()
			if w.db.IsPostgres() {
				want(t, what, post(body), http.StatusUnauthorized, invalidCredentials)
				return
			}
			want(t, what, post(body), sqliteStatus, sqliteBody)
		}
		refused("number as name", `{"username":12345,"password":"x"}`, http.StatusUnauthorized, invalidCredentials)
		refused("true as name", `{"username":true,"password":"x"}`, http.StatusInternalServerError, serverErrorBody)
		refused("list as name", `{"username":["a",null,["b"]],"password":"x"}`, http.StatusInternalServerError, serverErrorBody)
		refused("list of one as name", `{"username":["abc"],"password":"x"}`, http.StatusUnauthorized, invalidCredentials)
		refused("object as name", `{"username":{"a":1},"password":"x"}`, http.StatusInternalServerError, serverErrorBody)
		// A password that is not text is a wrong one (BUGS.md AR2)
		want(t, "number as password, unknown user", post(`{"username":"nobody","password":12345}`), http.StatusUnauthorized, invalidCredentials)
		want(t, "number as password, local account", post(`{"username":"alice","password":12345}`), http.StatusUnauthorized, invalidCredentials)
		want(t, "list as password", post(`{"username":"alice","password":["x"]}`), http.StatusUnauthorized, invalidCredentials)
		want(t, "number as password, single sign-on account", post(`{"username":"sso","password":12345}`), http.StatusUnauthorized, invalidCredentials)
		want(t, "number as password, directory account", post(`{"username":"dora","password":true}`), http.StatusUnauthorized, invalidCredentials)
		want(t, "number as password, blank name", post(`{"username":" ","password":12345}`), http.StatusUnauthorized, invalidCredentials)

		failed := func(name string) string {
			return "login_failed|Username: " + name + "; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0"
		}
		others := []string{failed("n***y"), failed("a***e"), failed("a***e"), failed("s***o"), failed("d***a"), "login_failed|Username:  ; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0"}
		// SQLite took a number as a double, which a text column keeps as
		// "12345.0", and a list of one value as that value. PostgreSQL
		// stored the text of every value.
		wantAudit := append([]string{failed("1***5"), failed("a***c")}, others...)
		wantAttempts := []string{"12345.0|127.0.0.1|0", "abc|127.0.0.1|0", "nobody|127.0.0.1|0", "alice|127.0.0.1|0", "alice|127.0.0.1|0", "sso|127.0.0.1|0", "dora|127.0.0.1|0", " |127.0.0.1|0"}
		if w.db.IsPostgres() {
			wantAudit = append([]string{failed("1***5"), failed("t***e"), failed("a***b"), failed("a***c"), failed("[***t Object]")}, others...)
			wantAttempts = []string{
				"12345|127.0.0.1|0", "true|127.0.0.1|0", `{"a",NULL,{"b"}}|127.0.0.1|0`, `{"abc"}|127.0.0.1|0`, `{"a":1}|127.0.0.1|0`,
				"nobody|127.0.0.1|0", "alice|127.0.0.1|0", "alice|127.0.0.1|0", "sso|127.0.0.1|0", "dora|127.0.0.1|0", " |127.0.0.1|0",
			}
		}
		equal(t, "audit", w.audit(), wantAudit)
		equal(t, "attempts", w.attempts(), wantAttempts)
		if calls := w.api.seen("POST /api/auth/login"); len(calls) != 0 {
			t.Errorf("the signal server was asked about a password that is not text: %v", calls)
		}
	})
}

// lockedOut is the answer to a sign-in a lockout refused, for a wait in
// seconds.
func lockedOut(seconds int) string {
	return fmt.Sprintf(`{"success":false,"error":"Too many sign-in attempts. Please try again later.","code":"account_locked","retry_after":%d}`, seconds)
}

func TestLoginRefusesALockedAccount(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.addUser(user{name: "alice"})
		w.addUser(user{name: "bob"})
		w.exec(`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('alice', ` +
			w.db.Pick(`strftime('%Y-%m-%d %H:%M:%f', 'now', '+600 seconds')`, `NOW() + INTERVAL '600 seconds'`) + `, 10)`)
		a := w.agent()

		// The right password does not help, and it is not even looked at
		res := a.post("/api/auth/login", login("alice", password))
		if res.status != http.StatusTooManyRequests || (res.body != lockedOut(600) && res.body != lockedOut(599)) {
			t.Errorf("locked account: %d %s", res.status, res.body)
		}
		if retry := res.header.Get("Retry-After"); retry != "600" && retry != "599" {
			t.Errorf("Retry-After: %q", retry)
		}
		if res.cookie("bd.sid") != "" {
			t.Errorf("a refused sign-in set a session cookie: %q", res.cookies())
		}
		equal(t, "audit", w.audit(), []string{"login_blocked|Account/IP locked; ip=127.0.0.1|127.0.0.1|0"})
		equal(t, "attempts", w.attempts(), nil)
		if n := w.int(`SELECT COUNT(*) FROM account_lockouts WHERE username = 'alice'`); n != 1 {
			t.Error("the refused sign-in removed the lockout")
		}
		want(t, "still signed out", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)

		// Another account from the same address signs in
		res = a.post("/api/auth/login", login("bob", password))
		if res.status != http.StatusOK {
			t.Errorf("another account: %d %s", res.status, res.body)
		}

		// The checks of the body come first
		want(t, "no password", a.post("/api/auth/login", `{"username":"alice"}`), http.StatusBadRequest, invalidCredentials)

		// A lockout that ended does not refuse
		w.exec(`UPDATE account_lockouts SET locked_until = ` + w.db.Pick(`'2000-01-01 00:00:00'`, `'2000-01-01 00:00:00+00'`))
		if res := w.agent().post("/api/auth/login", login("alice", password)); res.status != http.StatusOK {
			t.Errorf("after the lockout: %d %s", res.status, res.body)
		}
	})
}

func TestLoginRefusesAnAddressWithTooManyFailures(t *testing.T) {
	eachWorld(t, setup{trustProxy: "loopback"}, func(t *testing.T, w *world) {
		w.addUser(user{name: "alice"})
		for i := 0; i < 30; i++ {
			w.exec(`INSERT INTO login_attempts (username, ip_address, success) VALUES (?, '198.51.100.7', `+w.db.Pick("0", "false")+`)`, fmt.Sprintf("guess-%d", i))
		}
		// An older failure does not count
		w.exec(`UPDATE login_attempts SET created_at = ` + w.db.Pick(`datetime('now', '-16 minutes')`, `NOW() - INTERVAL '16 minutes'`) + ` WHERE username = 'guess-0'`)

		// 29 failures in the window: the address may still try
		if res := w.agentFrom("198.51.100.7").post("/api/auth/login", login("alice", "not-the-password")); res.status != http.StatusUnauthorized {
			t.Errorf("29 failures: %d %s", res.status, res.body)
		}
		// 30: refused, for the length of the window, whoever the account is
		res := w.agentFrom("198.51.100.7").post("/api/auth/login", login("alice", password))
		want(t, "30 failures", res, http.StatusTooManyRequests, lockedOut(900))
		if retry := res.header.Get("Retry-After"); retry != "900" {
			t.Errorf("Retry-After: %q", retry)
		}
		// Another address is not refused
		if res := w.agentFrom("198.51.100.8").post("/api/auth/login", login("alice", password)); res.status != http.StatusOK {
			t.Errorf("another address: %d %s", res.status, res.body)
		}
		equal(t, "audit", w.audit()[1:2], []string{"login_blocked|Account/IP locked; ip=198.51.100.7|198.51.100.7|0"})
	})
}

func TestLoginLockoutOfNamesThatAreNoText(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		// The lockout a number's own failures wrote: "12345.0" on SQLite,
		// "12345" on PostgreSQL
		name := w.db.Pick("12345.0", "12345")
		w.exec(`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES (?, '2099-01-01 00:00:00', 10)`, name)
		res := w.agent().post("/api/auth/login", `{"username":12345,"password":"x"}`)
		if res.status != http.StatusTooManyRequests {
			t.Errorf("number with a lockout: %d %s", res.status, res.body)
		}
		// A list of one name finds the lockout of that name on SQLite only
		w.exec(`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('abc', '2099-01-01 00:00:00', 10)`)
		status := w.db.Pick("429", "401")
		if res := w.agent().post("/api/auth/login", `{"username":["abc"],"password":"x"}`); fmt.Sprint(res.status) != status {
			t.Errorf("list with a lockout: %d %s, want %s", res.status, res.body, status)
		}
	})
}

// A lockout whose end is no time the console can read refuses for a
// minute.
func TestLoginLockoutWithAnEndThatIsNoTime(t *testing.T) {
	w := newWorld(t, nil, setup{})
	w.addUser(user{name: "alice"})
	w.exec(`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('alice', 'soon', 10)`)
	res := w.agent().post("/api/auth/login", login("alice", password))
	want(t, "unreadable end", res, http.StatusTooManyRequests, lockedOut(60))
	if retry := res.header.Get("Retry-After"); retry != "60" {
		t.Errorf("Retry-After: %q", retry)
	}
}

// Ten failed attempts for a name lock it: the eleventh sign-in is refused
// before its password is looked at, the right one included.
func TestLoginLockoutAfterTenFailures(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.addUser(user{name: "alice"})
		a := w.agent()
		for i := 1; i <= 10; i++ {
			if res := a.post("/api/auth/login", login("alice", "not-the-password")); res.status != http.StatusUnauthorized {
				t.Fatalf("failure %d: %d %s", i, res.status, res.body)
			}
		}
		res := a.post("/api/auth/login", login("alice", password))
		if res.status != http.StatusTooManyRequests || (res.body != lockedOut(900) && res.body != lockedOut(899)) {
			t.Errorf("after ten failures: %d %s", res.status, res.body)
		}
		if n := len(w.attempts()); n != 10 {
			t.Errorf("%d attempts recorded, want 10", n)
		}
	})
}

func TestLoginSignsIn(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		// A lockout that has ended is removed by the sign-in
		w.exec(`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('alice', '2000-01-01 00:00:00', 10)`)
		a := w.agent()
		// A visitor who already has a session (here with a field in it)
		a.setSession(`{"before":"sign-in"}`)
		before := a.sessionID()
		if before == "" {
			t.Fatal("no session before sign-in")
		}

		res := a.post("/api/auth/login", login("alice", password))
		want(t, "sign-in", res, http.StatusOK, signedInAlice)
		if got := res.cookies(); len(got) != 2 || got[0] != clearedGuest ||
			!regexp.MustCompile(`^bd\.sid=s%3A[\w-]{32}\.[\w%]+; Path=/; Expires=\w{3}, \d\d \w{3} \d{4} \d\d:\d\d:\d\d GMT; HttpOnly; SameSite=Lax$`).MatchString(got[1]) {
			t.Errorf("cookies of a sign-in: %q", got)
		}
		// The session is a new one: the id from before sign-in is worth nothing
		after := a.sessionID()
		if after == before {
			t.Error("sign-in kept the session id")
		}
		if left := w.text(`SELECT session_json FROM panel_sessions WHERE sid = ?`, before); left != "{}" {
			t.Errorf("the session from before sign-in still holds %s", left)
		}
		stored := w.stored(a)
		wantStored := regexp.MustCompile(`^\{"cookie":\{"originalMaxAge":1800000,"expires":"[^"]+","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"\},` +
			fmt.Sprintf(`"userId":%d,"user":\{"id":%d,"username":"alice","role":"operator","preferred_language":null\},`, alice, alice) +
			`"createdAt":\d{13},"lastActivityAt":\d{13},"loginIp":"127\.0\.0\.1","loginUserAgent":"auth-test/1","clientFingerprint":""\}$`)
		if !wantStored.MatchString(stored) {
			t.Errorf("stored session:\n%s", stored)
		}
		equal(t, "audit", w.audit(), []string{
			fmt.Sprintf("login|User logged in; ip=127.0.0.1; ua=auth-test/1; fingerprint=none|127.0.0.1|%d", alice),
		})
		equal(t, "attempts", w.attempts(), []string{"alice|127.0.0.1|1"})
		if n := w.int(`SELECT COUNT(*) FROM account_lockouts WHERE username = 'alice'`); n != 0 {
			t.Error("a sign-in left the account's lockout in place")
		}
		if at := w.text(`SELECT CAST(last_login AS TEXT) FROM users WHERE id = ?`, alice); at == "" {
			t.Error("the sign-in is not noted on the account")
		}
		if res := a.get("/api/auth/totp/status"); res.status != http.StatusOK {
			t.Errorf("the new session is not signed in: %d %s", res.status, res.body)
		}
	})
}

func TestLoginRecordsTheClientAndTheWayBack(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice", language: "de"})
	fingerprint := func(body string, header ...string) string {
		t.Helper()
		a := w.agent(header...)
		if res := a.post("/api/auth/login", body); res.status != http.StatusOK {
			t.Fatalf("sign-in with %s: %d %s", body, res.status, res.body)
		}
		return w.text(`SELECT COALESCE(client_fingerprint, '') FROM panel_sessions WHERE sid = ?`, a.sessionID())
	}
	base := `"username":"alice","password":"` + password + `"`
	for name, c := range map[string]struct {
		body   string
		header []string
		want   string
	}{
		"clientFingerprint":            {`{` + base + `,"clientFingerprint":"abcdef0123456789","fingerprint":"zzzzzzzzzz"}`, nil, "abcdef0123456789"},
		"fingerprint":                  {`{` + base + `,"clientFingerprint":"","fingerprint":" fp-from-body "}`, nil, "fp-from-body"},
		"the header":                   {`{` + base + `}`, []string{"X-Client-Fingerprint", "fp-from-header"}, "fp-from-header"},
		"the body before the header":   {`{` + base + `,"fingerprint":"fp-from-body"}`, []string{"X-Client-Fingerprint", "fp-from-header"}, "fp-from-body"},
		"too short":                    {`{` + base + `,"fingerprint":"short"}`, nil, ""},
		"a bad one hides the header":   {`{` + base + `,"fingerprint":"not a fingerprint!"}`, []string{"X-Client-Fingerprint", "fp-from-header"}, ""},
		"a number":                     {`{` + base + `,"fingerprint":1234567890}`, nil, "1234567890"},
		"an empty list hides the rest": {`{` + base + `,"clientFingerprint":[],"fingerprint":"fp-from-body"}`, []string{"X-Client-Fingerprint", "fp-from-header"}, ""},
	} {
		if got := fingerprint(c.body, c.header...); got != c.want {
			t.Errorf("%s: fingerprint %q, want %q", name, got, c.want)
		}
	}
	rows := w.audit()
	if last := rows[0]; last != fmt.Sprintf("login|User logged in; ip=127.0.0.1; ua=auth-test/1; fingerprint=abcdef0123456789|127.0.0.1|%d", alice) &&
		!strings.Contains(strings.Join(rows, "\n"), "fingerprint=abcdef0123456789") {
		t.Errorf("the audit entry does not name the fingerprint: %v", rows)
	}

	for body, wantRedirect := range map[string]string{
		`"return":"/devices?tab=1#x"`:  "/devices?tab=1#x",
		`"return":"/a b"`:              "/a%20b",
		`"return":"//evil.example/"`:   "/",
		`"return":"https://evil.test"`: "/",
		`"return":"/login"`:            "/",
		`"return":"/api/users"`:        "/",
		`"return":["/devices"]`:        "/",
		`"return":7`:                   "/",
		`"other":1`:                    "/",
	} {
		res := w.agent().post("/api/auth/login", `{`+base+`,`+body+`}`)
		if !strings.Contains(res.body, fmt.Sprintf(`"redirect":%q,`, wantRedirect)) {
			t.Errorf("%s: %s, want redirect %q", body, res.body, wantRedirect)
		}
	}
	// The user agent in the audit entry is cut at 160 characters
	a := w.agent("User-Agent", strings.Repeat("u", 200))
	a.signIn("alice")
	rows = w.audit()
	if last := rows[len(rows)-2]; !strings.Contains(last, "; ua="+strings.Repeat("u", 160)+"; fingerprint=none") {
		t.Errorf("audit entry with a long user agent: %s", last)
	}
	// The preferred language goes into the session's user
	if stored := w.stored(a); !strings.Contains(stored, `"preferred_language":"de"}`) {
		t.Errorf("stored session: %s", stored)
	}
}

func TestSecondSignInEndsTheFirst(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		first, second := w.agent(), w.agent()
		first.signIn("alice")
		res := second.post("/api/auth/login", login("alice", password))
		want(t, "second sign-in", res, http.StatusOK, strings.Replace(signedInAlice, `"replacedPriorSessions":0`, `"replacedPriorSessions":1`, 1))
		want(t, "the first browser", first.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		if res := second.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the second browser: %d %s", res.status, res.body)
		}
		equal(t, "session rows", w.sessions(alice), []string{"replaced|0", "|1"})
		equal(t, "audit", w.audit(), []string{
			fmt.Sprintf("login|User logged in; ip=127.0.0.1; ua=auth-test/1; fingerprint=none|127.0.0.1|%d", alice),
			fmt.Sprintf("login|User logged in; ip=127.0.0.1; ua=auth-test/1; fingerprint=none; replaced=1 prior session(s)|127.0.0.1|%d", alice),
			fmt.Sprintf("session_replaced|New login replaced 1 prior session(s); newIp=127.0.0.1|127.0.0.1|%d", alice),
		})
	})
}

func TestSignInAllowsMoreSessionsWhenThePolicyDoes(t *testing.T) {
	w := newWorld(t, nil, setup{env: map[string]string{"SESSION_MAX_PER_USER": "2"}})
	alice := w.addUser(user{name: "alice"})
	first := w.agent()
	first.signIn("alice")
	w.agent().signIn("alice")
	if res := first.get("/api/auth/verify"); res.status != http.StatusOK {
		t.Errorf("the first of two allowed sessions: %d %s", res.status, res.body)
	}
	equal(t, "session rows", w.sessions(alice), []string{"|1", "|1"})
}

func TestLoginLimiterAndTheOrderOfChecks(t *testing.T) {
	w := newWorld(t, nil, setup{env: map[string]string{"LOGIN_RATE_LIMIT_MAX": "2"}})
	w.addUser(user{name: "alice", totpEnabled: true, totpSecret: totpSecret})
	a := w.agent()
	const tooMany = `{"success":false,"error":"Too many login attempts. Please try again in a minute."}`

	// The CSRF gate is in front of the limiter: a refused request does not count
	res := a.do("POST", "/api/auth/login", `{}`, "Content-Type", "application/json")
	if res.status != http.StatusForbidden || !strings.Contains(res.body, "Invalid CSRF token") {
		t.Errorf("without a CSRF token: %d %s", res.status, res.body)
	}
	res = a.do("POST", "/api/auth/login", `{}`, "Content-Type", "application/json", "X-CSRF-Token", strings.Repeat("0", 128))
	if res.status != http.StatusForbidden {
		t.Errorf("with a wrong CSRF token: %d %s", res.status, res.body)
	}
	// So is the content type rule
	res = a.do("POST", "/api/auth/login", `username=alice`, "Content-Type", "application/x-www-form-urlencoded", "X-CSRF-Token", a.csrf())
	want(t, "a form", res, http.StatusUnsupportedMediaType, `{"success":false,"error":"Content-Type must be application/json"}`)

	// The limiter is in front of the validation: a refused body counts
	res = a.post("/api/auth/login", `{}`)
	want(t, "first", res, http.StatusBadRequest, invalidCredentials)
	if got := res.header.Get("RateLimit-Limit") + " " + res.header.Get("RateLimit-Remaining"); got != "2 1" {
		t.Errorf("RateLimit headers of the first request: %q", got)
	}
	want(t, "second", a.post("/api/auth/login", `{}`), http.StatusBadRequest, invalidCredentials)
	want(t, "third", a.post("/api/auth/login", login("alice", password)), http.StatusTooManyRequests, tooMany)
	// The second factor has a limit of its own (BUGS.md AR4)
	const noWait = `{"success":false,"error":"Session expired. Please log in again."}`
	want(t, "first code", a.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusBadRequest, noWait)
	want(t, "second code", a.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusBadRequest, noWait)
	want(t, "third code", a.post("/api/auth/totp/verify", `{"code":"123456"}`), http.StatusTooManyRequests,
		`{"success":false,"error":"Too many verification attempts. Please try again in a minute."}`)
	if rows := w.attempts(); len(rows) != 0 {
		t.Errorf("a limited sign-in reached the account: %v", rows)
	}
}

func TestLoginAnswersInTheRequestLanguage(t *testing.T) {
	w := newWorld(t, nil, setup{})
	res := w.agent().post("/api/auth/login", `{}`, "Accept-Language", "de")
	if res.status != http.StatusBadRequest || res.body == invalidCredentials || !strings.Contains(res.body, `"success":false`) {
		t.Errorf("German sign-in page: %d %s", res.status, res.body)
	}
}

func TestLoginWhenTheAuditLogFails(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice"})
	w.exec(`DROP TABLE audit_log`)
	a := w.agent()
	// The entry of a refused sign-in is part of the answer
	want(t, "refused", a.post("/api/auth/login", login("alice", "wrong")), http.StatusInternalServerError, serverErrorBody)
	// That of a sign-in too, with the fixed English text, after the session exists
	res := a.post("/api/auth/login", login("alice", password), "Accept-Language", "de")
	want(t, "accepted", res, http.StatusInternalServerError, serverErrorBody)
	equal(t, "session rows", w.sessions(alice), []string{"|1"})
}

func TestLoginThroughTheSignalServer(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.api.json("GET /api/auth/sso/status", 200, `{"ldap_enabled":true,"oidc_enabled":false,"any_enabled":true}`)
		dora := w.addUser(user{name: "dora", provider: "ldap", role: "viewer", hash: "$2b$04$unusableunusableunusableunusableunusableunusableunusab"})
		w.addUser(user{name: "bob"})
		accept := map[string]string{}
		w.api.on("POST /api/auth/login", func(rw http.ResponseWriter, _ *http.Request, body string) {
			rw.Header().Set("Content-Type", "application/json")
			for name, answer := range accept {
				if strings.Contains(body, fmt.Sprintf(`"username":%q`, name)) {
					_, _ = rw.Write([]byte(answer))
					return
				}
			}
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = rw.Write([]byte(`{"error":"Invalid credentials"}`))
		})

		// A directory account is checked by the signal server every time
		a := w.agent()
		want(t, "directory refuses", a.post("/api/auth/login", login("dora", "dir-pass")), http.StatusUnauthorized, invalidCredentials)
		accept["dora"] = `{"token":"jwt","role":"operator","auth_provider":"ldap"}`
		res := a.post("/api/auth/login", login("dora", "dir-pass"))
		want(t, "directory accepts", res, http.StatusOK, strings.Replace(signedInAlice, `"alice"`, `"dora"`, 1))
		if role := w.text(`SELECT role FROM users WHERE id = ?`, dora); role != "operator" {
			t.Errorf("the directory's role is not copied to the account: %q", role)
		}
		if calls := w.api.seen("POST /api/auth/login"); len(calls) != 2 || calls[1].Body != `{"username":"dora","password":"dir-pass"}` {
			t.Errorf("calls to the signal server: %+v", calls)
		}

		// A local account whose password the directory knows is a collision
		accept["bob"] = `{"token":"jwt","role":"admin","auth_provider":"ldap"}`
		res = w.agent().post("/api/auth/login", login("bob", "dir-pass"))
		want(t, "collision", res, http.StatusConflict,
			`{"success":false,"error":"A local account with this username already exists. Sign in with your local password, or ask an administrator to rename or remove the conflicting account.","code":"username_collision"}`)
		if provider := w.text(`SELECT auth_provider FROM users WHERE username = 'bob'`); provider != "local" {
			t.Errorf("a collision changed the account's provider to %q", provider)
		}

		// An unknown user the directory accepts gets a local account
		accept["newcomer"] = `{"token":"jwt","role":"viewer","auth_provider":"ldap"}`
		res = w.agent().post("/api/auth/login", login("newcomer", "dir-pass"))
		if res.status != http.StatusOK || !strings.Contains(res.body, `"user":{"username":"newcomer","role":"viewer"}`) {
			t.Errorf("a new directory user: %d %s", res.status, res.body)
		}
		if provider := w.text(`SELECT auth_provider FROM users WHERE username = 'newcomer'`); provider != "ldap" {
			t.Errorf("provider of the new account: %q", provider)
		}

		rows := w.audit()
		equal(t, "audit of the refusals", []string{rows[0], rows[2]}, []string{
			"login_failed|Username: d***a; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0",
			"login_failed|Username collision: b***b; ip=127.0.0.1; ua=auth-test/1|127.0.0.1|0",
		})
		equal(t, "attempts", w.attempts(), []string{"dora|127.0.0.1|0", "dora|127.0.0.1|1", "bob|127.0.0.1|0", "newcomer|127.0.0.1|1"})
	})
}

func TestLoginOverHTTPS(t *testing.T) {
	w := newWorld(t, nil, setup{https: true, trustProxy: "loopback"})
	w.addUser(user{name: "alice"})
	a := w.agent("X-Forwarded-Proto", "https", "X-Forwarded-For", "203.0.113.9")
	res := a.post("/api/auth/login", login("alice", password))
	if res.status != http.StatusOK {
		t.Fatalf("sign-in: %d %s", res.status, res.body)
	}
	if got := res.cookie("betterdesk.guest"); got != "betterdesk.guest=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("guest cookie over HTTPS: %q", got)
	}
	if got := res.cookie("betterdesk.sid"); !strings.Contains(got, "; HttpOnly; Secure; SameSite=Lax") {
		t.Errorf("session cookie over HTTPS: %q", got)
	}
	// The audit entry has the client's address behind the proxy, twice
	if rows := w.audit(); len(rows) != 1 || !strings.HasPrefix(rows[0], "login|User logged in; ip=203.0.113.9; ua=auth-test/1; fingerprint=none|203.0.113.9|") {
		t.Errorf("audit: %v", rows)
	}
}

// Where the signal server made the users table on PostgreSQL, ids are
// 64-bit and have always gone out, and into the session, as text.
func TestSignInKeepsTheFormOfTheAccountID(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		w := newWorld(t, db, setup{})
		alice := w.addUser(user{name: "alice"})
		id := fmt.Sprint(alice)
		if db.Shape().Table("users").Type("id") == store.TypeBigInt {
			id = `"` + id + `"`
		}
		a := w.agent()
		a.signIn("alice")
		if stored := w.stored(a); !strings.Contains(stored, `,"userId":`+id+`,"user":{"id":`+id+`,"username":"alice","role":"operator","preferred_language":null},`) {
			t.Errorf("stored session: %s", stored)
		}
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK || !strings.HasPrefix(res.body, `{"success":true,"user":{"id":`+id+`,"username":"alice",`) {
			t.Errorf("verify: %d %s", res.status, res.body)
		}
		// The session belongs to the account in the table's own column all the same
		if got := w.int(`SELECT user_id FROM panel_sessions WHERE sid = ?`, a.sessionID()); got != alice {
			t.Errorf("the session row belongs to account %d, want %d", got, alice)
		}
		if res := a.get("/api/auth/sessions"); res.status != http.StatusOK || !strings.Contains(res.body, `"current":true,"username":"alice"`) {
			t.Errorf("sessions: %d %s", res.status, res.body)
		}
		// A second sign-in still finds the first session to end
		if res := w.agent().signIn("alice"); !strings.Contains(res.body, `"replacedPriorSessions":1`) {
			t.Errorf("second sign-in: %s", res.body)
		}
		// and so does the second factor's pending state
		w.exec(`UPDATE users SET totp_secret = ?, totp_enabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, totpSecret, alice)
		b := w.agent()
		b.post("/api/auth/login", login("alice", password))
		if stored := w.stored(b); !strings.Contains(stored, `"pendingTotpUserId":`+id+`,"pendingTotpUser":{"id":`+id+`,`) {
			t.Errorf("stored session while the code is due: %s", stored)
		}
		if res := b.post("/api/auth/totp/verify", fmt.Sprintf(`{"code":%q}`, currentCode(t, totpSecret))); res.status != http.StatusOK {
			t.Errorf("code step: %d %s", res.status, res.body)
		}
		if stored := w.stored(b); !strings.Contains(stored, `,"userId":`+id+`,"user":{"id":`+id+`,`) {
			t.Errorf("stored session after the code: %s", stored)
		}
	})
}
