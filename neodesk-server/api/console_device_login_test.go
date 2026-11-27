package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// consoleLoginWorld is an API server with a console in the process at
// https://desk.example and an account to allow requests with.
type consoleLoginWorld struct {
	srv      *Server
	database db.Database
	user     *db.User
	now      time.Time
}

func newConsoleLoginWorld(t *testing.T) *consoleLoginWorld {
	t.Helper()
	t.Setenv("PANEL_PUBLIC_URL", "")
	t.Setenv("PUBLIC_URL", "")
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("console-device-login-test-secret", time.Hour))
	w := &consoleLoginWorld{srv: srv, database: database, now: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	srv.now = func() time.Time { return w.now }
	srv.SetConsoleAddress(func(string) string { return "https://desk.example" })
	w.user = &db.User{Username: "erin", PasswordHash: mustHash(t, "erin-pass-1"), Role: auth.RoleOperator}
	if err := database.CreateUser(w.user); err != nil {
		t.Fatal(err)
	}
	return w
}

// start begins a console sign-in for device id and uuid, from the
// listener, and returns the poll code and the request id from the URL.
func (w *consoleLoginWorld) start(t *testing.T, id, uuid string) (code, request string) {
	t.Helper()
	status, body := listenerCall(t, w.srv, http.MethodPost, "/api/oidc/auth",
		`{"op":"NeoDesk","id":"`+id+`","uuid":"`+uuid+`","deviceInfo":{"name":"Erin's laptop","os":"Windows 11","type":"client"}}`, nil)
	var out struct {
		Code string `json:"code"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.Code == "" {
		t.Fatalf("start: %d %s", status, body)
	}
	request, ok := strings.CutPrefix(out.URL, "https://desk.example/authorize-device/")
	if !ok || request == "" {
		t.Fatalf("start: the URL is %q", out.URL)
	}
	return out.Code, request
}

func (w *consoleLoginWorld) poll(t *testing.T, code, id, uuid string) (int, string) {
	t.Helper()
	q := url.Values{"code": {code}, "id": {id}, "uuid": {uuid}}
	return listenerCall(t, w.srv, http.MethodGet, "/api/oidc/auth-query?"+q.Encode(), "", nil)
}

func (w *consoleLoginWorld) decide(t *testing.T, request, verb string, account int64) (int, string) {
	t.Helper()
	return asConsole(t, w.srv, http.MethodPost, "/api/console-device-login/"+url.PathEscape(request)+"/"+verb,
		`{"account":`+strconv.FormatInt(account, 10)+`}`)
}

const waitingBody = `{"error":"No authed oidc is found","message":"Authorization in progress"}`

// TestConsoleLoginHandsTheTokenOutOnceAfterAllow: a request waits until
// the console allows it, then the next poll of the same device gets the
// access token of the account that allowed it, and the one after that
// finds nothing.
func TestConsoleLoginHandsTheTokenOutOnceAfterAllow(t *testing.T) {
	w := newConsoleLoginWorld(t)
	code, request := w.start(t, "DEVERIN1", "uuid-erin-1")
	if code == request {
		t.Fatal("the poll code is the request id")
	}
	if status, body := w.poll(t, code, "DEVERIN1", "uuid-erin-1"); status != http.StatusOK || body != waitingBody {
		t.Fatalf("poll before Allow: %d %s", status, body)
	}

	status, body := asConsole(t, w.srv, http.MethodGet, "/api/console-device-login/"+request, "")
	var view consoleLoginView
	if err := json.Unmarshal([]byte(body), &view); status != http.StatusOK || err != nil {
		t.Fatalf("read the request: %d %s", status, body)
	}
	if view.State != "pending" || view.DeviceID != "DEVERIN1" || view.DeviceName != "Erin's laptop" || view.DeviceOS != "Windows 11" ||
		view.IP != "203.0.113.7" || view.CreatedAt != "2026-11-27T10:00:00Z" || view.ExpiresAt != "2026-11-27T10:05:00Z" {
		t.Errorf("the request as the page shows it: %+v", view)
	}

	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK || !strings.Contains(body, `"state":"allowed"`) {
		t.Fatalf("approve: %d %s", status, body)
	}
	status, body = w.poll(t, code, "DEVERIN1", "uuid-erin-1")
	var got struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
		User        struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil || got.Type != "access_token" || got.AccessToken == "" || got.User.Name != "erin" {
		t.Fatalf("poll after Allow: %d %s", status, body)
	}
	if status, body := listenerCall(t, w.srv, http.MethodGet, "/api/currentUser", "", bearer(got.AccessToken)); status != http.StatusOK || !strings.Contains(body, `"name":"erin"`) {
		t.Errorf("the token does not sign in: %d %s", status, body)
	}
	if status, body := w.poll(t, code, "DEVERIN1", "uuid-erin-1"); status != http.StatusOK || strings.Contains(body, "access_token") || !strings.Contains(body, "expired") {
		t.Errorf("second poll after Allow: %d %s", status, body)
	}
}

// TestConsoleLoginPollIsBoundToTheDevice: a poll with another device's id
// or uuid, or without them, gets no token and leaves the token for the
// device that asked.
func TestConsoleLoginPollIsBoundToTheDevice(t *testing.T) {
	w := newConsoleLoginWorld(t)
	code, request := w.start(t, "DEVERIN1", "uuid-erin-1")
	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK {
		t.Fatalf("approve: %d %s", status, body)
	}
	for _, c := range []struct{ id, uuid string }{
		{"DEVERIN1", "uuid-other"}, {"DEVOTHER", "uuid-erin-1"}, {"", ""}, {"DEVERIN1", ""}, {"", "uuid-erin-1"},
	} {
		if status, body := w.poll(t, code, c.id, c.uuid); status != http.StatusUnauthorized || strings.Contains(body, "access_token") {
			t.Errorf("poll as %q/%q: %d %s", c.id, c.uuid, status, body)
		}
	}
	if status, body := w.poll(t, code, "DEVERIN1", "uuid-erin-1"); status != http.StatusOK || !strings.Contains(body, "access_token") {
		t.Errorf("poll of the device that asked: %d %s", status, body)
	}
}

// TestConsoleLoginSecretsDoNotSwap: the request id in the browser's
// address can't poll, and the poll code can't decide or read the request.
func TestConsoleLoginSecretsDoNotSwap(t *testing.T) {
	w := newConsoleLoginWorld(t)
	code, request := w.start(t, "DEVERIN1", "uuid-erin-1")
	if status, body := w.decide(t, code, "approve", w.user.ID); status != http.StatusNotFound {
		t.Errorf("approve with the poll code: %d %s", status, body)
	}
	if status, body := asConsole(t, w.srv, http.MethodGet, "/api/console-device-login/"+url.PathEscape(code), ""); status != http.StatusNotFound {
		t.Errorf("read with the poll code: %d %s", status, body)
	}
	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK {
		t.Fatalf("approve: %d %s", status, body)
	}
	for _, guess := range []string{request, "nd-" + request} {
		if status, body := w.poll(t, guess, "DEVERIN1", "uuid-erin-1"); strings.Contains(body, "access_token") {
			t.Errorf("poll with %q: %d %s", guess, status, body)
		}
	}
	if status, body := w.poll(t, code, "DEVERIN1", "uuid-erin-1"); !strings.Contains(body, "access_token") {
		t.Errorf("poll with the code: %d %s", status, body)
	}
}

// TestConsoleLoginIsDecidedOnce: a second Allow, a Deny after Allow and an
// Allow after Deny are refused with the state; a denied request tells
// the app once and hands out no token.
func TestConsoleLoginIsDecidedOnce(t *testing.T) {
	w := newConsoleLoginWorld(t)
	_, request := w.start(t, "DEVERIN1", "uuid-erin-1")
	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK {
		t.Fatalf("approve: %d %s", status, body)
	}
	for _, verb := range []string{"approve", "deny"} {
		if status, body := w.decide(t, request, verb, w.user.ID); status != http.StatusConflict || body != `{"error":"not_pending","state":"allowed"}` {
			t.Errorf("%s after Allow: %d %s", verb, status, body)
		}
	}

	code, request := w.start(t, "DEVERIN2", "uuid-erin-2")
	if status, body := w.decide(t, request, "deny", w.user.ID); status != http.StatusOK || !strings.Contains(body, `"state":"declined"`) {
		t.Fatalf("deny: %d %s", status, body)
	}
	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusConflict || body != `{"error":"not_pending","state":"declined"}` {
		t.Errorf("approve after Deny: %d %s", status, body)
	}
	if status, body := w.poll(t, code, "DEVERIN2", "uuid-erin-2"); status != http.StatusOK || body != `{"error":"Sign-in was declined in the browser"}` {
		t.Errorf("poll after Deny: %d %s", status, body)
	}
	if status, body := w.poll(t, code, "DEVERIN2", "uuid-erin-2"); status != http.StatusOK || strings.Contains(body, "access_token") || body == waitingBody {
		t.Errorf("second poll after Deny: %d %s", status, body)
	}
}

// TestConsoleLoginExpires: after five minutes the request can't be read,
// decided or polled, and an allowed one hands out no token.
func TestConsoleLoginExpires(t *testing.T) {
	w := newConsoleLoginWorld(t)
	code, request := w.start(t, "DEVERIN1", "uuid-erin-1")
	w.now = w.now.Add(consoleLoginLifetime)
	if status, body := asConsole(t, w.srv, http.MethodGet, "/api/console-device-login/"+request, ""); status != http.StatusNotFound {
		t.Errorf("read after five minutes: %d %s", status, body)
	}
	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusNotFound {
		t.Errorf("approve after five minutes: %d %s", status, body)
	}
	if status, body := w.poll(t, code, "DEVERIN1", "uuid-erin-1"); status != http.StatusOK || body != `{"error":"`+consoleLoginExpiredMsg+`"}` {
		t.Errorf("poll after five minutes: %d %s", status, body)
	}

	code, request = w.start(t, "DEVERIN2", "uuid-erin-2")
	w.now = w.now.Add(consoleLoginLifetime - time.Second)
	if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK {
		t.Fatalf("approve in time: %d %s", status, body)
	}
	w.now = w.now.Add(time.Second)
	if status, body := w.poll(t, code, "DEVERIN2", "uuid-erin-2"); strings.Contains(body, "access_token") {
		t.Errorf("poll of an allowed request after five minutes: %d %s", status, body)
	}
}

// TestConsoleLoginChecksTheAccountAtHandout: an account disabled, due to
// set up two-factor sign-in or due to change its password between Allow
// and the poll gets the desktop password sign-in's refusal, not a token.
// The same accounts can't allow a request either.
func TestConsoleLoginChecksTheAccountAtHandout(t *testing.T) {
	w := newConsoleLoginWorld(t)
	rules := map[string]db.AccountSignInRules{}
	w.srv.db = rulesDB{Database: w.database, rules: rules}
	for _, c := range []struct {
		name   string
		set    func(on bool)
		answer string
	}{
		{"disabled", func(on bool) {
			if err := w.database.SetUserDisabled(w.user.ID, on); err != nil {
				t.Fatal(err)
			}
		}, `{"code":"account_disabled","error":"This account is disabled"}`},
		{"two-factor setup due", func(on bool) {
			if err := w.database.SetConfig(configRequireTwoFactor, strconv.FormatBool(on)); err != nil {
				t.Fatal(err)
			}
		}, `{"code":"totp_setup_required","error":"Two-factor sign-in is required for this account. Sign in to the web console once to set it up."}`},
		{"password change due", func(on bool) {
			rules["erin"] = db.AccountSignInRules{MustChangePassword: on}
		}, `{"code":"password_change_required","error":"This account must choose a new password. Sign in to the web console once to set it."}`},
	} {
		code, request := w.start(t, "DEVERIN1", "uuid-erin-1")
		if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK {
			t.Fatalf("%s: approve: %d %s", c.name, status, body)
		}
		c.set(true)
		if status, body := w.poll(t, code, "DEVERIN1", "uuid-erin-1"); status != http.StatusForbidden || body != c.answer {
			t.Errorf("%s at handout: %d %s", c.name, status, body)
		}
		_, request = w.start(t, "DEVERIN1", "uuid-erin-1")
		if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusConflict || !strings.Contains(body, `"error":"account_refused"`) {
			t.Errorf("%s: approve: %d %s", c.name, status, body)
		}
		c.set(false)
		if status, body := w.decide(t, request, "approve", w.user.ID); status != http.StatusOK {
			t.Errorf("%s: approve once it is fixed: %d %s", c.name, status, body)
		}
	}
}

// TestConsoleLoginStart: the console sign-in starts without an outside
// identity provider, needs the device id and uuid, refuses without a
// console address, and the routes that read and decide answer only in
// the process.
func TestConsoleLoginStart(t *testing.T) {
	w := newConsoleLoginWorld(t)
	if status, body := listenerCall(t, w.srv, http.MethodPost, "/api/oidc/auth", `{"op":"NeoDesk","id":"","uuid":"u"}`, nil); status != http.StatusBadRequest {
		t.Errorf("start without a device id: %d %s", status, body)
	}
	if status, body := listenerCall(t, w.srv, http.MethodPost, "/api/oidc/auth", `{"op":"neodesk","id":"D","uuid":"u"}`, nil); status != http.StatusBadRequest || body != `{"error":"OIDC is not enabled"}` {
		t.Errorf("another op without an identity provider: %d %s", status, body)
	}
	_, request := w.start(t, "DEVERIN1", "uuid-erin-1")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/console-device-login/" + request},
		{http.MethodPost, "/api/console-device-login/" + request + "/approve"},
		{http.MethodPost, "/api/console-device-login/" + request + "/deny"},
	} {
		if status, body := listenerCall(t, w.srv, c.method, c.path, `{"account":1}`, map[string]string{"X-API-Key": testAPIKey}); status != http.StatusNotFound {
			t.Errorf("%s %s from the listener: %d %s", c.method, c.path, status, body)
		}
	}

	w.srv.SetConsoleAddress(nil)
	if status, body := listenerCall(t, w.srv, http.MethodPost, "/api/oidc/auth", `{"op":"NeoDesk","id":"D","uuid":"u"}`, nil); status != http.StatusServiceUnavailable || body != `{"error":"`+consoleLoginNoConsole+`"}` {
		t.Errorf("start without a console: %d %s", status, body)
	}
	t.Setenv("PANEL_PUBLIC_URL", "https://panel.example/")
	status, body := listenerCall(t, w.srv, http.MethodPost, "/api/oidc/auth", `{"op":"NeoDesk","id":"D","uuid":"u"}`, nil)
	if status != http.StatusOK || !strings.Contains(body, `"url":"https://panel.example/authorize-device/`) {
		t.Errorf("start with PANEL_PUBLIC_URL: %d %s", status, body)
	}
}

// TestConsoleLoginCap: once maxConsoleLogins requests wait, a start is
// refused until one expires.
func TestConsoleLoginCap(t *testing.T) {
	var ls consoleLogins
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	for i := 0; i < maxConsoleLogins; i++ {
		if _, err := ls.start(consoleLogin{deviceID: "D", uuid: "u"}, now); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := ls.start(consoleLogin{deviceID: "D", uuid: "u"}, now.Add(time.Minute)); err != errConsoleLoginTooMany {
		t.Errorf("start over the cap: %v", err)
	}
	if _, err := ls.start(consoleLogin{deviceID: "D", uuid: "u"}, now.Add(consoleLoginLifetime)); err != nil {
		t.Errorf("start once the others expired: %v", err)
	}
}

// TestConsoleLoginBase: the console's address comes from the single
// sign-on settings or the environment before the console in the process,
// which gets the host the app reached the server at; a loopback host
// counts only for an app on this machine.
func TestConsoleLoginBase(t *testing.T) {
	t.Setenv("PANEL_PUBLIC_URL", "")
	t.Setenv("PUBLIC_URL", "")
	srv, database := newInternalServer(t, nil)
	var asked string
	srv.SetConsoleAddress(func(host string) string {
		asked = host
		if host == "" {
			return ""
		}
		return "http://" + host + ":3375"
	})
	req := func(host, remote string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "http://"+host+"/api/oidc/auth", nil)
		r.Host, r.RemoteAddr = host, remote
		return r
	}
	if got := srv.consoleLoginBase(req("desk.example:21114", "203.0.113.7:1")); got != "http://desk.example:3375" || asked != "desk.example" {
		t.Errorf("from the request's host: %q (asked %q)", got, asked)
	}
	if got := srv.consoleLoginBase(req("127.0.0.1:21114", "203.0.113.7:1")); got != "" {
		t.Errorf("loopback host from outside: %q", got)
	}
	if got := srv.consoleLoginBase(req("localhost:21114", "127.0.0.1:1")); got != "http://localhost:3375" {
		t.Errorf("loopback host from this machine: %q", got)
	}
	if err := database.SetConfig("oidc.panel_url", "https://sso.example/"); err != nil {
		t.Fatal(err)
	}
	if got := srv.consoleLoginBase(req("desk.example", "203.0.113.7:1")); got != "https://sso.example" {
		t.Errorf("with Console address set: %q", got)
	}
}
