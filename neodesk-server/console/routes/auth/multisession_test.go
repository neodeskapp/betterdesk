package auth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The browsers of these tests, as their user agents say.
const (
	firefoxOnWindows = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0"
	chromeOnMac      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// signedInDevice is an entry of GET /api/users/me/sessions.
type signedInDevice struct {
	ID             string `json:"id"`
	Current        bool   `json:"current"`
	Browser        string `json:"browser"`
	System         string `json:"system"`
	IPAddress      string `json:"ipAddress"`
	UserAgent      string `json:"userAgent"`
	CreatedAt      int64  `json:"createdAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
}

type signedInDevices struct {
	Success bool `json:"success"`
	Data    struct {
		Sessions []signedInDevice `json:"sessions"`
		Limit    struct {
			Max    int    `json:"max"`
			Source string `json:"source"`
		} `json:"limit"`
	} `json:"data"`
}

// devices reads the agent's list of its account's signed-in devices.
func (a *agent) devices() signedInDevices {
	a.w.t.Helper()
	res := a.get("/api/users/me/sessions")
	if res.status != http.StatusOK {
		a.w.t.Fatalf("signed-in devices: %d %s", res.status, res.body)
	}
	var out signedInDevices
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		a.w.t.Fatalf("signed-in devices: %v in %s", err, res.body)
	}
	return out
}

// ownID is the id the list gives the agent's own session.
func (a *agent) ownID() string {
	a.w.t.Helper()
	for _, s := range a.devices().Data.Sessions {
		if s.Current {
			return s.ID
		}
	}
	a.w.t.Fatal("no entry of the list is this browser")
	return ""
}

func (a *agent) del(target string) answer {
	a.w.t.Helper()
	return a.do("DELETE", target, "", "X-CSRF-Token", a.csrf())
}

// signedOut is the answer to a browser whose session another one ended.
func signedOut(code, text string) string {
	return fmt.Sprintf(`{"success":false,"error":%q,"code":%q}`, text, code)
}

const (
	signedOutElsewhere = "You were signed out from another device. Please sign in again."
	replacedElsewhere  = "You were signed out because this account signed in on another browser. This server allows one session per account."
	overTheLimit       = "You were signed out because this account signed in on more browsers than this server allows."
	accountChanged     = "You were signed out because the account's password, two-factor sign-in or access changed. Please sign in again."
)

func TestTwoSignInsOfOneAccountBothStayValid(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		first, second := w.agent(), w.agent()
		first.signIn("alice")
		want(t, "second sign-in", second.post("/api/auth/login", login("alice", password)), http.StatusOK, signedInAlice)
		for name, a := range map[string]*agent{"first": first, "second": second} {
			if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
				t.Errorf("the %s browser: %d %s", name, res.status, res.body)
			}
		}
		equal(t, "session rows", w.sessions(alice), []string{"|1", "|1"})
		for _, row := range w.audit() {
			if strings.HasPrefix(row, "session_") {
				t.Errorf("a second sign-in ended something: %s", row)
			}
		}
	})
}

func TestSignedInDevicesListsTheAccountsOwnSessions(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.addUser(user{name: "alice"})
		w.addUser(user{name: "bob"})
		laptop := w.agent("User-Agent", firefoxOnWindows)
		mac := w.agent("User-Agent", chromeOnMac)
		laptop.signIn("alice")
		mac.signIn("alice")
		w.agent().signIn("bob")

		list := laptop.devices()
		if !list.Success || len(list.Data.Sessions) != 2 {
			t.Fatalf("list: %+v", list)
		}
		if list.Data.Limit.Max != 10 || list.Data.Limit.Source != "default" {
			t.Errorf("limit: %+v", list.Data.Limit)
		}
		var got []string
		for _, s := range list.Data.Sessions {
			got = append(got, fmt.Sprintf("%s on %s, current=%v, ip=%s", s.Browser, s.System, s.Current, s.IPAddress))
			if len(s.ID) != 32 || s.ID == laptop.sessionID() || s.ID == mac.sessionID() {
				t.Errorf("id %q is not a handle of its own", s.ID)
			}
			if s.CreatedAt == 0 || s.LastActivityAt < s.CreatedAt {
				t.Errorf("times: %+v", s)
			}
		}
		sort.Strings(got)
		equal(t, "entries", got, []string{
			"Chrome on macOS, current=false, ip=127.0.0.1",
			"Firefox on Windows, current=true, ip=127.0.0.1",
		})
		// The list is the same from the other browser, with its own entry current
		other := mac.devices().Data.Sessions
		if len(other) != 2 || slices.IndexFunc(other, func(s signedInDevice) bool { return s.Current && s.Browser == "Chrome" }) < 0 {
			t.Errorf("from the other browser: %+v", other)
		}
	})
}

func TestEndingOneSessionFromTheListEndsExactlyThatOne(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		bob := w.addUser(user{name: "bob"})
		a, b, c := w.agent(), w.agent("User-Agent", firefoxOnWindows), w.agent()
		a.signIn("alice")
		b.signIn("alice")
		c.signIn("alice")
		bobs := w.agent()
		bobs.signIn("bob")
		target := b.ownID()

		// Another account can't end it, and an unknown id ends nothing
		want(t, "bob ends alice's session", bobs.del("/api/users/me/sessions/"+target), http.StatusNotFound, `{"success":false,"error":"Session not found"}`)
		want(t, "an unknown id", a.del("/api/users/me/sessions/"+strings.Repeat("0", 32)), http.StatusNotFound, `{"success":false,"error":"Session not found"}`)
		// The list's own entry is signed out with Sign out, not from the list
		want(t, "the own session", a.del("/api/users/me/sessions/"+a.ownID()), http.StatusBadRequest, `{"success":false,"error":"This is the session of this browser. Use Sign out.","code":"current_session"}`)

		want(t, "end it", a.del("/api/users/me/sessions/"+target), http.StatusOK, `{"success":true}`)
		want(t, "the ended browser", b.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_signed_out", signedOutElsewhere))
		// Told once: the answer clears its cookie, and then it is signed out
		want(t, "the ended browser again", b.get("/api/auth/verify"), http.StatusUnauthorized, notSignedIn)
		for name, x := range map[string]*agent{"the browser that ended it": a, "the third browser": c, "bob": bobs} {
			if res := x.get("/api/auth/verify"); res.status != http.StatusOK {
				t.Errorf("%s: %d %s", name, res.status, res.body)
			}
		}
		rows := w.sessions(alice)
		sort.Strings(rows)
		equal(t, "session rows", rows, []string{"signed_out|0", "|1", "|1"})
		equal(t, "bob's rows", w.sessions(bob), []string{"|1"})
		audit := w.audit()
		last := audit[len(audit)-1]
		wantPrefix := fmt.Sprintf("session_signed_out|Signed out session %s (Firefox on Windows, 127.0.0.1) of alice from another device|127.0.0.1|%d", target[:8], alice)
		if last != wantPrefix {
			t.Errorf("audit entry:\n%s\nwant\n%s", last, wantPrefix)
		}
		// A page request of the ended browser goes to the sign-in page with the reason
		d := w.agent()
		d.signIn("alice")
		want(t, "end d", a.del("/api/users/me/sessions/"+d.ownID()), http.StatusOK, `{"success":true}`)
		res := d.get("/devices")
		if res.status != http.StatusFound || res.header.Get("Location") != "/login?expired=1&reason=session_signed_out&return=%2Fdevices" {
			t.Errorf("page of the ended browser: %d %q", res.status, res.header.Get("Location"))
		}
	})
}

func TestSignOutEverywhereElse(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		w.addUser(user{name: "bob"})
		a, b, c, bobs := w.agent(), w.agent(), w.agent(), w.agent()
		a.signIn("alice")
		b.signIn("alice")
		c.signIn("alice")
		bobs.signIn("bob")
		want(t, "everywhere else", a.post("/api/users/me/sessions/end-others", `{}`), http.StatusOK, `{"success":true,"ended":2}`)
		for name, x := range map[string]*agent{"second": b, "third": c} {
			want(t, name, x.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_signed_out", signedOutElsewhere))
		}
		for name, x := range map[string]*agent{"this browser": a, "bob": bobs} {
			if res := x.get("/api/auth/verify"); res.status != http.StatusOK {
				t.Errorf("%s: %d %s", name, res.status, res.body)
			}
		}
		if list := a.devices().Data.Sessions; len(list) != 1 || !list[0].Current {
			t.Errorf("the list afterwards: %+v", list)
		}
		rows := w.sessions(alice)
		sort.Strings(rows)
		equal(t, "session rows", rows, []string{"signed_out|0", "signed_out|0", "|1"})
		n := 0
		for _, row := range w.audit() {
			if strings.HasPrefix(row, "session_signed_out|") {
				n++
			}
		}
		if n != 2 {
			t.Errorf("%d audit entries for two ended sessions: %v", n, w.audit())
		}
	})
}

func TestTheCapEndsTheLeastRecentlyUsedSession(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		var browsers []*agent
		for range 10 {
			a := w.agent()
			a.signIn("alice")
			browsers = append(browsers, a)
		}
		// The first one was used last, so the second is the least recently used
		w.exec(`UPDATE panel_sessions SET last_activity_at = last_activity_at - 60000 WHERE sid <> ?`, browsers[0].sessionID())
		w.exec(`UPDATE panel_sessions SET last_activity_at = last_activity_at - 60000 WHERE sid = ?`, browsers[1].sessionID())
		eleventh := w.agent()
		eleventh.signIn("alice")
		want(t, "the least recently used", browsers[1].get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_limit", overTheLimit))
		for i, a := range append([]*agent{eleventh}, append([]*agent{browsers[0]}, browsers[2:]...)...) {
			if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
				t.Errorf("browser %d: %d %s", i, res.status, res.body)
			}
		}
		if n := w.int(`SELECT COUNT(*) FROM panel_sessions WHERE user_id = ? AND end_reason = 'limit'`, alice); n != 1 {
			t.Errorf("%d sessions ended by the limit", n)
		}
		found := false
		for _, row := range w.audit() {
			if strings.HasPrefix(row, "session_limit|Sign-in from 127.0.0.1 ended the least recently used session ") &&
				strings.HasSuffix(row, " at the limit of 10 sessions per account|127.0.0.1|"+fmt.Sprint(alice)) {
				found = true
			}
		}
		if !found {
			t.Errorf("no session_limit audit entry: %v", w.audit())
		}
	})
}

func TestOneSessionPerAccountSettingRestoresTheOldRule(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		admin := w.addUser(user{name: "root", role: "admin"})
		root := w.agent()
		root.signIn("root")
		want(t, "setting", root.get("/api/settings/console-sessions"), http.StatusOK,
			`{"success":true,"data":{"single_session":false,"limit":10,"source":"default","env_value":null}}`)
		want(t, "turn it on", root.do("PUT", "/api/settings/console-sessions", `{"single_session":true}`, "Content-Type", "application/json", "X-CSRF-Token", root.csrf()),
			http.StatusOK, `{"success":true}`)
		want(t, "setting on", root.get("/api/settings/console-sessions"), http.StatusOK,
			`{"success":true,"data":{"single_session":true,"limit":1,"source":"setting","env_value":null}}`)

		first, second := w.agent(), w.agent()
		first.signIn("alice")
		want(t, "second sign-in", second.post("/api/auth/login", login("alice", password)), http.StatusOK,
			strings.Replace(signedInAlice, `"replacedPriorSessions":0`, `"replacedPriorSessions":1`, 1))
		want(t, "the first browser", first.get("/api/auth/verify"), http.StatusUnauthorized, signedOut("session_replaced", replacedElsewhere))
		equal(t, "session rows", w.sessions(alice), []string{"replaced|0", "|1"})
		audit := w.audit()
		if !slices.Contains(audit, fmt.Sprintf("console_sessions_policy_changed|One session per account: true|127.0.0.1|%d", admin)) {
			t.Errorf("no audit entry for the setting: %v", audit)
		}
		if !slices.Contains(audit, fmt.Sprintf("session_replaced|New login replaced 1 prior session(s); newIp=127.0.0.1|127.0.0.1|%d", alice)) {
			t.Errorf("no session_replaced entry: %v", audit)
		}
		// Without the permission the setting is refused
		other := w.agent()
		other.signIn("alice")
		if res := other.get("/api/settings/console-sessions"); res.status != http.StatusForbidden {
			t.Errorf("an operator reads the setting: %d %s", res.status, res.body)
		}
	})
}

func TestTheEnvValueDecidesOverTheSetting(t *testing.T) {
	for _, tc := range []struct {
		env, setting string
		wantLimit    int
		ends         bool
	}{
		{"1", "false", 1, true},
		{"3", "true", 3, false},
		{"0", "true", 0, false},
	} {
		t.Run("env="+tc.env, func(t *testing.T) {
			w := newWorld(t, nil, setup{env: map[string]string{"SESSION_MAX_PER_USER": tc.env}})
			w.addUser(user{name: "alice"})
			w.addUser(user{name: "root", role: "admin"})
			w.exec(`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT)`)
			w.exec(`INSERT INTO server_config (key, value) VALUES ('console_single_session', ?)`, tc.setting)
			root := w.agent()
			root.signIn("root")
			want(t, "setting", root.get("/api/settings/console-sessions"), http.StatusOK,
				fmt.Sprintf(`{"success":true,"data":{"single_session":%s,"limit":%d,"source":"env","env_value":%q}}`, tc.setting, tc.wantLimit, tc.env))
			first, second := w.agent(), w.agent()
			first.signIn("alice")
			second.signIn("alice")
			if res := first.get("/api/auth/verify"); (res.status == http.StatusUnauthorized) != tc.ends {
				t.Errorf("the first browser: %d %s", res.status, res.body)
			}
		})
	}
}
