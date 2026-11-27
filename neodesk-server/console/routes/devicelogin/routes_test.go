package devicelogin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console"
	consoleconfig "github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/security"
)

var users = map[string]int64{"erin": 11, "finn": 12}

// world is a console with its API server on one database, the API's
// listener as the desktop app reaches it, and a browser per account.
type world struct {
	t     *testing.T
	db    *store.DB
	gdb   db.Database
	front *httptest.Server
	api   *api.Server
}

func newWorld(t *testing.T, sdb *store.DB) *world {
	t.Helper()
	t.Setenv("PANEL_PUBLIC_URL", "")
	t.Setenv("PUBLIC_URL", "")
	ctx := context.Background()
	for name, id := range users {
		if _, err := sdb.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', 'operator')", id, name); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	gdb := storetest.GoServer(t, sdb)
	apiSrv := api.New(config.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
	apiSrv.SetBlocklist(security.NewBlocklist())
	apiSrv.SetJWTManager(auth.NewJWTManager("device-login-test-secret-0123456789", time.Hour))
	apiSrv.SetConsoleAddress(func(string) string { return "https://desk.example" })

	data := t.TempDir()
	env := map[string]string{"SESSION_SECRET": "device-login-secret-0123456789abcdef0123456789abcdef"}
	ccfg := &consoleconfig.Config{
		Host: "127.0.0.1", NodeEnv: "development", DataDir: data,
		UploadsDir: filepath.Join(data, "uploads"), LangDir: filepath.Join("..", "..", "static", "files", "lang"),
		WebUIDir: filepath.Join(data, "no-build"), DefaultLanguage: "en",
		Getenv: func(key string) string { return env[key] },
	}
	s, err := console.New(ctx, console.Options{Config: ccfg, Store: sdb, API: apiSrv.InternalHandler()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	d := s.Deps()
	s.Router().Get("/__test/signin/{name}", func(rw http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		sess := kit.Session(r)
		_ = sess.Regenerate()
		if _, err := sess.Establish(session.User{ID: users[name], Username: name, Role: "operator"}, d.Sessions.ClientMeta(r, "")); err != nil {
			t.Errorf("establish: %v", err)
		}
		kit.OK(rw, r, nil)
	})
	s.Router().Get("/__test/token", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"token": kit.CSRFToken(r)})
	})
	w := &world{t: t, db: sdb, gdb: gdb, front: httptest.NewServer(s), api: apiSrv}
	t.Cleanup(w.front.Close)
	return w
}

type answer struct {
	status int
	body   string
}

func (a answer) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(a.body), &out); err != nil {
		t.Fatalf("not JSON: %v: %s", err, a.body)
	}
	return out
}

// browser keeps one account's cookies and CSRF token.
type browser struct {
	w       *world
	cookies map[string]string
	token   string
}

func (w *world) browser(name string) *browser {
	b := &browser{w: w, cookies: map[string]string{}}
	if name != "" {
		if res := b.do(http.MethodGet, "/__test/signin/"+name, ""); res.status != http.StatusOK {
			w.t.Fatalf("sign in %s: %d %s", name, res.status, res.body)
		}
		b.token, _ = b.do(http.MethodGet, "/__test/token", "").json(w.t)["token"].(string)
	}
	return b
}

func (b *browser) do(method, path, body string) answer {
	b.w.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, b.w.front.URL+path, reader)
	if err != nil {
		b.w.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && b.token != "" {
		req.Header.Set("X-CSRF-Token", b.token)
	}
	req.Header.Set("Accept", "application/json")
	var pairs []string
	for k, v := range b.cookies {
		pairs = append(pairs, k+"="+v)
	}
	if len(pairs) > 0 {
		req.Header.Set("Cookie", strings.Join(pairs, "; "))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.w.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	for _, c := range res.Cookies() {
		b.cookies[c.Name] = c.Value
	}
	return answer{status: res.StatusCode, body: string(raw)}
}

// app sends a request to the API server's listener as the desktop app.
func (w *world) app(method, path, body string) answer {
	w.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.4:50000"
	rec := httptest.NewRecorder()
	w.api.ListenerHandler().ServeHTTP(rec, req)
	return answer{status: rec.Code, body: strings.TrimSpace(rec.Body.String())}
}

// start begins a sign-in as the desktop app and returns the poll code and
// the request id of the page's address.
func (w *world) start(id, uuid string) (code, request string) {
	w.t.Helper()
	res := w.app(http.MethodPost, "/api/oidc/auth", `{"op":"NeoDesk","id":"`+id+`","uuid":"`+uuid+`","deviceInfo":{"name":"Front desk","os":"Linux","type":"client"}}`)
	out := res.json(w.t)
	code, _ = out["code"].(string)
	link, _ := out["url"].(string)
	request, ok := strings.CutPrefix(link, "https://desk.example/authorize-device/")
	if res.status != http.StatusOK || code == "" || !ok {
		w.t.Fatalf("start: %d %s", res.status, res.body)
	}
	return code, request
}

func (w *world) poll(code, id, uuid string) answer {
	w.t.Helper()
	q := url.Values{"code": {code}, "id": {id}, "uuid": {uuid}}
	return w.app(http.MethodGet, "/api/oidc/auth-query?"+q.Encode(), "")
}

func (w *world) audits(action string) []string {
	w.t.Helper()
	rows, err := w.db.Query(context.Background(), `SELECT COALESCE(user_id, 0), COALESCE(details, '') FROM audit_log WHERE action = ? ORDER BY id`, action)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var who int64
		var details string
		if err := rows.Scan(&who, &details); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, details)
		if who != users["erin"] {
			w.t.Errorf("%s entry by account %d", action, who)
		}
	}
	return out
}

// TestAllowSignsTheComputerInAsTheAccount: the page reads the request
// with the computer's details and the account it signs in as; Allow
// binds it to that account, writes the audit entry, and the app's next
// poll gets a token for it.
func TestAllowSignsTheComputerInAsTheAccount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		code, request := w.start("DEVFRONT1", "uuid-front-1")
		erin := w.browser("erin")
		got := erin.do(http.MethodGet, "/api/device-login/"+request, "").json(t)
		if got["state"] != "pending" || got["device_name"] != "Front desk" || got["device_os"] != "Linux" || got["device_id"] != "DEVFRONT1" ||
			got["ip"] != "198.51.100.4" || got["account"] != "erin" || got["created_at"] == "" {
			t.Fatalf("the request: %v", got)
		}
		res := erin.do(http.MethodPost, "/api/device-login/"+request+"/approve", `{}`)
		if res.status != http.StatusOK || res.json(t)["state"] != "allowed" {
			t.Fatalf("approve: %d %s", res.status, res.body)
		}
		poll := w.poll(code, "DEVFRONT1", "uuid-front-1")
		var token struct {
			Type string `json:"type"`
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		}
		if err := json.Unmarshal([]byte(poll.body), &token); err != nil || token.Type != "access_token" || token.User.Name != "erin" {
			t.Fatalf("poll: %d %s", poll.status, poll.body)
		}
		if entries := w.audits("console_device_login_allowed"); len(entries) != 1 ||
			!strings.Contains(entries[0], "Front desk (NeoDesk ID DEVFRONT1, Linux, asked from 198.51.100.4)") {
			t.Errorf("audit entries: %q", entries)
		}
		if again := erin.do(http.MethodPost, "/api/device-login/"+request+"/deny", `{}`); again.status != http.StatusNotFound {
			t.Errorf("deny after the token went out: %d %s", again.status, again.body)
		}
	})
}

// TestDenyAndDecidedRequests: Deny tells the app it was declined and
// writes its audit entry; a second decision is refused with the state;
// an unknown request reads as expired.
func TestDenyAndDecidedRequests(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	code, request := w.start("DEVFRONT1", "uuid-front-1")
	erin := w.browser("erin")
	if res := erin.do(http.MethodPost, "/api/device-login/"+request+"/deny", `{}`); res.status != http.StatusOK || res.json(t)["state"] != "declined" {
		t.Fatalf("deny: %d %s", res.status, res.body)
	}
	res := erin.do(http.MethodPost, "/api/device-login/"+request+"/approve", `{}`)
	if out := res.json(t); res.status != http.StatusConflict || out["code"] != "already_decided" || out["state"] != "declined" {
		t.Errorf("approve after Deny: %d %s", res.status, res.body)
	}
	if poll := w.poll(code, "DEVFRONT1", "uuid-front-1"); poll.body != `{"error":"Sign-in was declined in the browser"}` {
		t.Errorf("poll after Deny: %d %s", poll.status, poll.body)
	}
	if entries := w.audits("console_device_login_denied"); len(entries) != 1 || !strings.HasPrefix(entries[0], "Denied the desktop sign-in of Front desk") {
		t.Errorf("audit entries: %q", entries)
	}
	if entries := w.audits("console_device_login_allowed"); len(entries) != 0 {
		t.Errorf("an Allow entry after a refused Allow: %q", entries)
	}
	for _, id := range []string{"no-such-request", strings.Repeat("a", 65), "a.b"} {
		if out := erin.do(http.MethodGet, "/api/device-login/"+url.PathEscape(id), "").json(t); out["state"] != "expired" || out["device_id"] != "" {
			t.Errorf("unknown request %q: %v", id, out)
		}
		if res := erin.do(http.MethodPost, "/api/device-login/"+url.PathEscape(id)+"/approve", `{}`); res.status != http.StatusNotFound {
			t.Errorf("approve unknown request %q: %d %s", id, res.status, res.body)
		}
	}
}

// TestDecidingNeedsASessionAndTheCSRFToken: signed out, the routes ask
// for a session; a write without the CSRF token stops at the gate and
// leaves the request open; the poll code is no request id.
func TestDecidingNeedsASessionAndTheCSRFToken(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	code, request := w.start("DEVFRONT1", "uuid-front-1")
	if res := w.browser("").do(http.MethodGet, "/api/device-login/"+request, ""); res.status != http.StatusUnauthorized || strings.Contains(res.body, "Front desk") {
		t.Errorf("read signed out: %d %s", res.status, res.body)
	}
	if res := w.browser("").do(http.MethodPost, "/api/device-login/"+request+"/approve", `{}`); res.status != http.StatusForbidden && res.status != http.StatusUnauthorized {
		t.Errorf("approve signed out: %d %s", res.status, res.body)
	}
	erin := w.browser("erin")
	token := erin.token
	erin.token = ""
	if res := erin.do(http.MethodPost, "/api/device-login/"+request+"/approve", `{}`); res.status != http.StatusForbidden {
		t.Errorf("approve without the CSRF token: %d %s", res.status, res.body)
	}
	erin.token = token
	if res := erin.do(http.MethodPost, "/api/device-login/"+url.PathEscape(code)+"/approve", `{}`); res.status != http.StatusNotFound {
		t.Errorf("approve with the poll code: %d %s", res.status, res.body)
	}
	if out := erin.do(http.MethodGet, "/api/device-login/"+request, "").json(t); out["state"] != "pending" {
		t.Errorf("the request after the refused writes: %v", out)
	}
	if poll := w.poll(code, "DEVFRONT1", "uuid-front-1"); !strings.Contains(poll.body, "No authed oidc is found") {
		t.Errorf("poll after the refused writes: %d %s", poll.status, poll.body)
	}
}

// TestAllowOfAnAccountThatMayNotSignIn: an account that must set up
// two-factor sign-in gets its reason, and the request stays open for
// when it did.
func TestAllowOfAnAccountThatMayNotSignIn(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	_, request := w.start("DEVFRONT1", "uuid-front-1")
	if err := w.gdb.SetConfig("require_two_factor", "true"); err != nil {
		t.Fatal(err)
	}
	erin := w.browser("erin")
	res := erin.do(http.MethodPost, "/api/device-login/"+request+"/approve", `{}`)
	if out := res.json(t); res.status != http.StatusConflict || out["code"] != "totp_setup_required" {
		t.Fatalf("approve: %d %s", res.status, res.body)
	}
	if out := erin.do(http.MethodGet, "/api/device-login/"+request, "").json(t); out["state"] != "pending" {
		t.Errorf("the request after a refused Allow: %v", out)
	}
}
