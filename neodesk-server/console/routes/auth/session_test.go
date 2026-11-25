package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// The clearing cookies of a sign-out: the session cookie under both names
// it can have, and no other cookie (BUGS.md S1).
var clearedAfterSignOut = []string{
	"neodesk.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
	"nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
}

func TestLogout(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		a := w.agent()
		a.signIn("alice")
		sid := a.sessionID()

		res := a.do("POST", "/api/auth/logout", `{}`, "Content-Type", "application/json")
		if res.status != http.StatusForbidden {
			t.Errorf("sign-out without a CSRF token: %d %s", res.status, res.body)
		}
		res = a.post("/api/auth/logout", `{}`)
		want(t, "sign-out", res, http.StatusOK, `{"success":true}`)
		equal(t, "cookies", res.cookies(), clearedAfterSignOut)
		if row := w.rows(`SELECT session_json, end_reason FROM panel_sessions WHERE sid = ?`, sid); len(row) != 1 || row[0] != "{}|logout" {
			t.Errorf("the session row after sign-out: %v", row)
		}
		equal(t, "session rows", w.sessions(alice), []string{"logout|0"})
		want(t, "after sign-out", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)

		// Signed out, it answers the same and writes no entry
		res = a.post("/api/auth/logout", `{}`)
		want(t, "second sign-out", res, http.StatusOK, `{"success":true}`)
		equal(t, "cookies of the second", res.cookies(), clearedAfterSignOut)
		rows := w.audit()
		equal(t, "audit", rows[1:], []string{fmt.Sprintf("logout|User logged out|127.0.0.1|%d", alice)})
	})
}

// logoutGetText is what GET /logout tells a signed-in browser.
const logoutGetText = "Signing out changed to a POST request for CSRF protection. Use the sign-out button in the panel."

// cookiesBut are the Set-Cookie lines of an answer without the CSRF
// cookie, which every page answer may set.
func cookiesBut(res answer) []string {
	var out []string
	for _, line := range res.cookies() {
		if !strings.HasPrefix(line, "__csrf_h=") {
			out = append(out, line)
		}
	}
	return out
}

func TestLogoutPage(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		a := w.agent()
		a.signIn("alice")
		sid := a.sessionID()
		w.exec(`DELETE FROM audit_log`)

		// Signed in: refused, and the session goes on. Without a build of
		// the web app the answer is plain text.
		res := a.get("/logout", "Accept", "text/html")
		if res.status != http.StatusMethodNotAllowed || res.body != logoutGetText || res.header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("sign-out page: %d %q %s", res.status, res.header.Get("Content-Type"), res.body)
		}
		// The 405 names the method that signs out (BUGS UM27)
		if allow := res.header.Values("Allow"); len(allow) != 1 || allow[0] != "POST" {
			t.Errorf("Allow of the sign-out page: %q", allow)
		}
		for _, line := range cookiesBut(res) {
			if strings.Contains(line, "=;") {
				t.Errorf("the sign-out page clears a cookie: %s", line)
			}
		}
		if row := w.rows(`SELECT end_reason FROM panel_sessions WHERE sid = ?`, sid); len(row) != 1 || row[0] != "" {
			t.Errorf("the session row after the sign-out page: %v", row)
		}
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the sign-out page ended the session: %d %s", res.status, res.body)
		}
		if rows := w.audit(); len(rows) != 0 {
			t.Errorf("the sign-out page wrote audit entries: %v", rows)
		}
		equal(t, "session rows", w.sessions(alice), []string{"|1"})

		// Signed out: to the sign-in page, with no cookie cleared
		res = w.agent().get("/logout", "Accept", "text/html")
		if res.status != http.StatusFound || res.header.Get("Location") != "/login" || res.body != `<p>Found. Redirecting to /login</p>` {
			t.Errorf("sign-out page without a session: %d %q %s", res.status, res.header.Get("Location"), res.body)
		}
		equal(t, "cookies without a session", cookiesBut(res), nil)
		if rows := w.audit(); len(rows) != 0 {
			t.Errorf("the sign-out page wrote audit entries: %v", rows)
		}
	})
}

// With a build of the web app, a signed-in browser gets the app's error
// page with the status 405 and the words of the refusal.
func TestLogoutPageWithTheWebApp(t *testing.T) {
	w := newWorld(t, nil, setup{build: true})
	w.addUser(user{name: "alice"})
	a := w.agent()
	a.signIn("alice")
	res := a.get("/logout", "Accept", "text/html")
	meta := `<meta name="bd-error" content="500" data-title="Logout requires POST" data-message="` + logoutGetText + `">`
	if res.status != http.StatusMethodNotAllowed || !strings.Contains(res.body, "<head>"+meta) || res.header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("sign-out page: %d %q %s", res.status, res.header.Get("Content-Type"), res.body)
	}
	if res.header.Get("Cache-Control") != "no-store" || res.header.Get("Content-Security-Policy") == "" || res.header.Get("Allow") != "POST" {
		t.Errorf("headers of the error page: %v", res.header)
	}
	if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
		t.Errorf("the sign-out page ended the session: %d %s", res.status, res.body)
	}
}

func TestLogoutForm(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		a := w.agent()
		a.signIn("alice")
		sid := a.sessionID()
		w.exec(`DELETE FROM audit_log`)

		// Without the CSRF token nothing happens
		res := a.do("POST", "/logout", "", "Accept", "text/html")
		if res.status != http.StatusForbidden {
			t.Errorf("sign-out form without a CSRF token: %d %s", res.status, res.body)
		}
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("a refused sign-out form ended the session: %d %s", res.status, res.body)
		}

		res = a.do("POST", "/logout", "", "Accept", "text/html", "X-CSRF-Token", a.csrf())
		if res.status != http.StatusFound || res.header.Get("Location") != "/login" || res.body != `<p>Found. Redirecting to /login</p>` {
			t.Errorf("sign-out form: %d %q %s", res.status, res.header.Get("Location"), res.body)
		}
		equal(t, "cookies", cookiesBut(res), clearedAfterSignOut)
		if row := w.rows(`SELECT session_json, end_reason FROM panel_sessions WHERE sid = ?`, sid); len(row) != 1 || row[0] != "{}|logout" {
			t.Errorf("the session row after sign-out: %v", row)
		}
		equal(t, "audit", w.audit(), []string{fmt.Sprintf("logout|User logged out|127.0.0.1|%d", alice)})
		want(t, "after sign-out", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)

		// Signed out: the same answer, no entry
		b := w.agent()
		res = b.do("POST", "/logout", "", "X-CSRF-Token", b.csrf())
		if res.status != http.StatusFound || res.header.Get("Location") != "/login" {
			t.Errorf("sign-out form without a session: %d %q", res.status, res.header.Get("Location"))
		}
		if n := len(w.audit()); n != 1 {
			t.Errorf("%d audit entries after a sign-out without a session", n)
		}
	})
}

func TestLogoutWhenTheAuditLogFails(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice"})
	for _, signOut := range []func(a *agent) answer{
		func(a *agent) answer { return a.post("/api/auth/logout", `{}`) },
		func(a *agent) answer { return a.do("POST", "/logout", "", "X-CSRF-Token", a.csrf()) },
	} {
		a := w.agent()
		a.signIn("alice")
		w.exec(`ALTER TABLE audit_log RENAME TO audit_log_away`)
		res := signOut(a)
		w.exec(`ALTER TABLE audit_log_away RENAME TO audit_log`)
		if res.status != http.StatusOK && res.status != http.StatusFound {
			t.Errorf("sign-out without an audit log: %d %s", res.status, res.body)
		}
		want(t, "after sign-out", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
	}
	equal(t, "session rows", w.sessions(alice), []string{"logout|0", "logout|0"})
}

func TestVerify(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice", language: "de"})
		a := w.agent()
		want(t, "signed out", a.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		before := time.Now().UnixMilli()
		a.signIn("alice")
		res := a.get("/api/auth/verify")
		m := regexp.MustCompile(`^\{"success":true,"user":\{"id":(\d+),"username":"alice","role":"operator","preferred_language":"de"\},` +
			`"session":\{"createdAt":(\d+),"lastActivityAt":(\d+),"loginIp":"127\.0\.0\.1","idleTimeoutMs":1800000,"maxAgeMs":86400000,"warnBeforeMs":60000,` +
			`"idleExpiresAt":(\d+),"absoluteExpiresAt":(\d+),"serverNow":(\d+)\}\}$`).FindStringSubmatch(res.body)
		if res.status != http.StatusOK || m == nil {
			t.Fatalf("verify: %d %s", res.status, res.body)
		}
		var id, created, last, idleEnd, absoluteEnd, now int64
		for i, target := range []*int64{&id, &created, &last, &idleEnd, &absoluteEnd, &now} {
			fmt.Sscan(m[i+1], target)
		}
		after := time.Now().UnixMilli()
		if id != alice || created < before || created > last || last > now || now > after ||
			idleEnd != last+1800000 || absoluteEnd != created+86400000 {
			t.Errorf("verify: id %d, created %d, last %d, idle end %d, absolute end %d, now %d (between %d and %d)",
				id, created, last, idleEnd, absoluteEnd, now, before, after)
		}
	})
}

func TestVerifyOfSessionsWithOtherFields(t *testing.T) {
	w := newWorld(t, nil, setup{})
	// A session an older console left: the id as text, times as text, and
	// fields this console does not write
	a := w.agent()
	a.setSession(`{"userId":"7","user":{"id":"7","username":"old","role":"viewer","extra":true}}`)
	a.setSession(`{"loginIp":"","createdAt":"` + fmt.Sprint(time.Now().UnixMilli()) + `"}`)
	res := a.get("/api/auth/verify")
	if res.status != http.StatusOK || !strings.HasPrefix(res.body, `{"success":true,"user":{"id":"7","username":"old","role":"viewer","extra":true},"session":{"createdAt":1`) ||
		!strings.Contains(res.body, `"loginIp":null,`) {
		t.Errorf("verify: %d %s", res.status, res.body)
	}
	// Its row belongs to no account the list could find: the list is empty, not missing
	if res := a.get("/api/auth/sessions"); res.status != http.StatusOK || !strings.HasPrefix(res.body, `{"success":true,"data":{"sessions":[],"policy":{`) {
		t.Errorf("sessions of a session without a row of its account: %d %s", res.status, res.body)
	}
	// One without a user object has no user in the answer
	b := w.agent()
	b.setSession(`{"userId":7}`)
	res = b.get("/api/auth/verify")
	if res.status != http.StatusOK || !strings.HasPrefix(res.body, `{"success":true,"session":{"createdAt":`) {
		t.Errorf("verify without a user: %d %s", res.status, res.body)
	}
}

func TestSessionList(t *testing.T) {
	// One session per account, so the list holds a replaced one
	eachWorld(t, setup{env: map[string]string{"SESSION_MAX_PER_USER": "1"}}, func(t *testing.T, w *world) {
		w.addUser(user{name: "alice"})
		w.addUser(user{name: "bob"})
		a := w.agent()
		want(t, "signed out", a.get("/api/auth/sessions"), http.StatusUnauthorized, notSignedIn)
		w.agent("X-Client-Fingerprint", "first-browser").signIn("alice")
		w.agent().signIn("bob")
		a.signIn("alice")

		res := a.get("/api/auth/sessions")
		number := `\d+`
		if w.db.IsPostgres() {
			// The 64-bit columns have always gone out as text there
			number = `"\d+"`
		}
		entry := func(current bool, fingerprint, ended, reason string) string {
			return fmt.Sprintf(`\{"id":"[\w-]{32}","current":%t,"username":"alice","ipAddress":"127\.0\.0\.1","userAgent":"auth-test/1","clientFingerprint":%s,`+
				`"createdAt":%s,"lastActivityAt":%s,"endedAt":%s,"endReason":%s,"active":%t,"expiresAt":\d+\}`,
				current, fingerprint, number, number, ended, reason, current)
		}
		wantBody := regexp.MustCompile(`^\{"success":true,"data":\{"sessions":\[` +
			entry(true, "null", "null", "null") + `,` + entry(false, `"first-browser"`, number, `"replaced"`) +
			`\],"policy":\{"idleTimeoutMs":1800000,"maxAgeMs":86400000,"warnBeforeMs":60000,"maxSessionsPerUser":1\}\}\}$`)
		if res.status != http.StatusOK || !wantBody.MatchString(res.body) {
			t.Errorf("sessions: %d %s", res.status, res.body)
		}
		// The list names a session by its handle, never by the cookie's id
		if !strings.Contains(res.body, `{"id":"`+session.Handle(a.sessionID())+`","current":true`) || strings.Contains(res.body, a.sessionID()) {
			t.Error("the current session is not the one of the cookie")
		}
	})
}

func TestSessionListWhenTheTableFails(t *testing.T) {
	w := newWorld(t, nil, setup{})
	w.addUser(user{name: "alice"})
	a := w.agent()
	a.signIn("alice")
	// The request still finds its session: the list is the read that fails
	w.exec(`ALTER TABLE panel_sessions RENAME COLUMN ended_at TO ended_at_away`)
	res := a.get("/api/auth/sessions")
	w.exec(`ALTER TABLE panel_sessions RENAME COLUMN ended_at_away TO ended_at`)
	if res.status != http.StatusInternalServerError || res.body != serverErrorBody {
		t.Errorf("sessions without the column: %d %s", res.status, res.body)
	}
}

func TestHousekeepingJob(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		if names := strings.Join(w.s.Jobs().Names(), ","); !strings.Contains(names, "sign-in housekeeping") {
			t.Errorf("jobs: %s", names)
		}
		alice := w.addUser(user{name: "alice"})
		old, fresh := w.db.Pick("datetime('now', '-2 days')", "NOW() - INTERVAL '2 days'"), w.db.Pick("datetime('now', '+2 days')", "NOW() + INTERVAL '2 days'")
		yes, no := w.db.Pick("1", "TRUE"), w.db.Pick("0", "FALSE")
		w.exec(`INSERT INTO login_attempts (username, ip_address, success, created_at) VALUES ('old', '10.0.0.1', ` + no + `, ` + old + `)`)
		w.exec(`INSERT INTO login_attempts (username, ip_address, success) VALUES ('new', '10.0.0.1', ` + no + `)`)
		for token, row := range map[string]string{"expired": old + ", " + no, "revoked": fresh + ", " + yes, "good": fresh + ", " + no} {
			w.exec(`INSERT INTO access_tokens (token, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, 'c', 'u', `+row+`)`, token, alice)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			auth.RunHousekeeping(ctx, w.s.Deps(), 20*time.Millisecond)
			close(done)
		}()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && w.int(`SELECT COUNT(*) FROM login_attempts`)+w.int(`SELECT COUNT(*) FROM access_tokens`) != 2 {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the job did not end with its context")
		}
		equal(t, "attempts left", w.rows(`SELECT username FROM login_attempts`), []string{"new"})
		equal(t, "tokens left", w.rows(`SELECT token FROM access_tokens`), []string{"good"})
	})
}
