package settings_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/settings"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// site is a console for the general settings: its folders are temporary,
// the API server is made up.
type site struct {
	*routetest.World
	API     *routetest.API
	Console string // the old console's folder: .env, .env.local
	Keys    string // the server's keys folder
}

func newSite(t *testing.T, db *store.DB, overrides routetest.Overrides, env map[string]string) *site {
	t.Helper()
	root := t.TempDir()
	s := &site{API: routetest.NewAPI(), Console: filepath.Join(root, "web-nodejs"), Keys: filepath.Join(root, "keys")}
	if err := os.MkdirAll(s.Console, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := routetest.Options{DB: db, API: s.API, Env: env, Config: func(cfg *config.Config) {
		cfg.ConsoleDir = s.Console
		cfg.Version = "4.5.6"
		cfg.KeysPath = s.Keys
		cfg.EnvPath = filepath.Join(s.Console, ".env")
		cfg.ComposePath = filepath.Join(root, "no-compose", "docker-compose.yml")
		cfg.DBPath = "/srv/db_v2.sqlite3"
		cfg.PubKeyPath = "/srv/id_ed25519.pub"
		cfg.APIKeyPath = filepath.Join(s.Keys, ".api_key")
		cfg.NodeEnv = "production"
	}}
	if overrides != nil {
		opts.Roles = overrides
	}
	s.World = routetest.New(t, opts)
	return s
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return out
}

func lastAudit(t *testing.T, w *routetest.World) string {
	t.Helper()
	rows := w.AuditRows()
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1]
}

// Every route but the liveness probe needs a session; the routes of the
// settings need server.config, the audit log audit.view, and the server's
// facts and addresses only a session. All of them are in the panel section
// settings, whose read and write permission is server.config: a role
// without it is refused by the panel guard first, also when a panel access
// row sets the section to read (the row is capped at the role's level).
// A role with server.config passes the guard, and the audit log still asks
// audit.view.
func TestGuards(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, routetest.Overrides{
			"viewer":       {"audit.view": false},
			"global_admin": {"server.config": true, "audit.view": false},
		}, nil)
		exec(t, db, "INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('user', '6', 'settings', 'read')")
		serverConfig := []string{
			"GET /api/settings/restart/pending", "POST /api/settings/restart/cancel", "POST /api/settings/restart/confirm",
			"POST /api/settings/restart/complete", "GET /api/settings/device-scope", "POST /api/settings/device-scope",
			"GET /api/settings/availability-history", "POST /api/settings/availability-history",
			"GET /api/settings/enrollment", "PUT /api/settings/enrollment", "GET /api/settings/ldap", "PUT /api/settings/ldap",
			"POST /api/settings/ldap/test", "GET /api/settings/client-sessions", "PUT /api/settings/client-sessions",
			"GET /api/settings/two-factor", "PUT /api/settings/two-factor",
			"GET /api/settings/oidc", "PUT /api/settings/oidc", "POST /api/settings/oidc/test",
			"GET /api/settings/advanced/files", "GET /api/settings/advanced/files/console-env",
			"PUT /api/settings/advanced/files/console-env", "POST /api/settings/advanced/restart",
			"GET /api/settings/connection-mode", "PUT /api/settings/connection-mode", "POST /api/settings/connection-mode/restart",
			"GET /api/settings/public-endpoints", "PUT /api/settings/public-endpoints",
			"GET /api/settings/email/smtp", "PUT /api/settings/email/smtp", "POST /api/settings/email/smtp/test",
		}
		denied := s.As("viewer")
		admin := s.As("admin")
		sectionDenied := `{"success":false,"error":"Panel access denied: settings","code":"panel_access_denied"}`
		for _, route := range serverConfig {
			method, path, _ := strings.Cut(route, " ")
			body := ""
			if method != http.MethodGet {
				body = "{}"
			}
			if res := s.SignedOut().Do(method, path, body); res.Status != http.StatusUnauthorized ||
				res.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
				t.Errorf("%s signed out: %d %s", route, res.Status, res.Body)
			}
			if method == http.MethodGet {
				if res := admin.Do(method, path, ""); res.Status == http.StatusForbidden || res.Status == http.StatusUnauthorized {
					t.Errorf("%s as admin: %d %s", route, res.Status, res.Body)
				}
			}
			if res := denied.Do(method, path, body); res.Status != http.StatusForbidden || res.Body != sectionDenied {
				t.Errorf("%s without server.config: %d %s, want the panel guard's refusal", route, res.Status, res.Body)
			}
		}
		// The audit trail belongs to the security audit section (PA3, F2).
		if res := denied.Do("GET", "/api/settings/audit", ""); res.Status != http.StatusForbidden ||
			res.Body != `{"success":false,"error":"Panel access denied: security-audit","code":"panel_access_denied"}` {
			t.Errorf("audit without audit.view and server.config: %d %s", res.Status, res.Body)
		}
		allowed := s.As("global_admin")
		if res := allowed.Do("GET", "/api/settings/audit", ""); res.Status != http.StatusForbidden ||
			res.Body != `{"success":false,"error":"Permission denied: audit.view"}` {
			t.Errorf("audit without audit.view: %d %s", res.Status, res.Body)
		}
		// What every account reads has no panel section (PA3, F1).
		for _, path := range []string{"/api/settings/info", "/api/settings/server-info"} {
			if res := denied.Do("GET", path, ""); res.Status != http.StatusOK {
				t.Errorf("%s as viewer: %d %s", path, res.Status, res.Body)
			}
			if res := allowed.Do("GET", path, ""); res.Status != http.StatusOK {
				t.Errorf("%s with a session: %d %s", path, res.Status, res.Body)
			}
		}
		if res := s.SignedOut().Do("GET", "/api/settings/restart-status", ""); res.Status != http.StatusOK ||
			res.Header.Get("Cache-Control") != "no-store, no-cache, must-revalidate, private" {
			t.Errorf("restart-status signed out: %d %q %s", res.Status, res.Header.Get("Cache-Control"), res.Body)
		}
	})
}

func TestRestartStatus(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	res := s.SignedOut().Do("GET", "/api/settings/restart-status", "")
	got := decode(t, res.Body)
	data := got["data"].(map[string]any)
	// uptime is whole seconds since the start, which on a busy machine
	// can be past the first second when the request arrives
	uptime, isNumber := data["uptime"].(float64)
	if data["status"] != "ok" || !strings.HasPrefix(data["cacheVersion"].(string), "4.5.6.") || !isNumber || uptime < 0 || uptime > 30 || uptime != float64(int(uptime)) {
		t.Errorf("liveness: %s", res.Body)
	}
	for _, query := range []string{"?job=a&token=b", "?job=a&job=b&token=t", "?job[x]=1&token=t"} {
		res := s.SignedOut().Do("GET", "/api/settings/restart-status"+query, "")
		if res.Status != http.StatusOK || res.Body != `{"success":true,"data":{"phase":"unknown","status":"unknown","ready":false}}` {
			t.Errorf("%s: %d %s", query, res.Status, res.Body)
		}
	}
	for _, query := range []string{"?job=a", "?job=a&token=", "?token=b"} {
		res := s.SignedOut().Do("GET", "/api/settings/restart-status"+query, "")
		if !strings.Contains(res.Body, `"status":"ok"`) {
			t.Errorf("%s: %s", query, res.Body)
		}
	}
	// A transaction file that is no object of the expected shape: the
	// status answers 503 with the error of the coordinator
	state := `{"id":"tx","token":"tok","phase":"pending","changes":5}`
	if err := os.WriteFile(filepath.Join(s.DataDir, ".restart-transaction.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	res = s.SignedOut().Do("GET", "/api/settings/restart-status?job=tx&token=tok", "")
	if res.Status != http.StatusServiceUnavailable || !strings.HasPrefix(res.Body, `{"success":false,"data":{"status":"starting","ready":false},"error":"`) {
		t.Errorf("broken transaction: %d %s", res.Status, res.Body)
	}
}

// The file editor's chain through the restart transaction, as the
// contract cases run it: a cancel removes a file the save created (BUGS
// SE1) and puts the backup of a file that was there back.
func TestAdvancedFileChain(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, nil)
		admin := s.As("admin")
		secret := filepath.Join(s.DataDir, ".session_secret")
		res := admin.Do("PUT", "/api/settings/advanced/files/console-session-secret", `{"content":"first-secret-0123456789abcdef0123\n"}`)
		got := decode(t, res.Body)
		data, _ := got["data"].(map[string]any)
		if res.Status != http.StatusOK || data["created"] != true || data["backupPath"] != nil || got["message"] != "settings.advanced_saved" {
			t.Fatalf("create: %d %s", res.Status, res.Body)
		}
		if keys := keyOrder(t, res.Body); keys != "success,data,message,restartRequired" {
			t.Errorf("keys %s", keys)
		}
		tx := got["restartRequired"].(map[string]any)["id"].(string)
		if !reflect.DeepEqual(data["restartRequired"], got["restartRequired"]) {
			t.Errorf("two transactions: %s", res.Body)
		}
		if row := lastAudit(t, s.World); row != "advanced_config_saved|Saved advanced config: "+secret+"|127.0.0.1|1" {
			t.Errorf("audit %s", row)
		}
		file, err := os.ReadFile(filepath.Join(s.DataDir, ".restart-transaction.json"))
		if err != nil || !strings.Contains(string(file), `"rollback": {
        "type": "advanced-file",
        "fileId": "console-session-secret",
        "backupPath": null,
        "created": true
      }`) {
			t.Errorf("transaction file: %v %s", err, file)
		}
		// The restart prompt names the file as the editor does, not by its
		// id (BUGS L405)
		if !strings.Contains(string(file), `"label": "settings.advanced_file_console-session-secret"`) {
			t.Errorf("transaction file without the file's name: %s", file)
		}

		// Another operator may not change the file into this transaction:
		// the answer is 409 and the file stays as it is (BUGS SE2)
		res = s.As("super_admin").Do("PUT", "/api/settings/advanced/files/console-session-secret", `{"content":"other-secret-0123456789abcdef0123\n"}`)
		if res.Status != http.StatusConflict || res.Body != `{"success":false,"error":"restart_transaction_owned"}` {
			t.Errorf("other operator: %d %s", res.Status, res.Body)
		}
		if content, _ := os.ReadFile(secret); string(content) != "first-secret-0123456789abcdef0123\n" {
			t.Errorf("content after the other operator: %q", content)
		}
		if entries, _ := os.ReadDir(s.DataDir); strings.Contains(fmt.Sprint(entries), ".session_secret.bak.") {
			t.Errorf("the refused write left a backup: %v", entries)
		}

		res = admin.Do("POST", "/api/settings/restart/cancel", `{"id":"`+tx+`"}`)
		if res.Status != http.StatusOK || res.Body != `{"success":true,"data":{"canceled":true,"id":"`+tx+`"}}` {
			t.Errorf("cancel of a new file: %d %s", res.Status, res.Body)
		}
		if _, err := os.Stat(secret); !os.IsNotExist(err) {
			t.Errorf("the file the save created is still there after the cancel: %v", err)
		}
		res = admin.Do("GET", "/api/settings/restart/pending", "")
		if res.Body != `{"success":true,"data":null}` || res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("pending after the cancel: %q %s", res.Header.Get("Cache-Control"), res.Body)
		}

		if err := os.WriteFile(secret, []byte("other-secret-0123456789abcdef0123\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res = admin.Do("PUT", "/api/settings/advanced/files/console-session-secret", `{"content":"second-secret-0123456789abcdef0123\n"}`)
		got = decode(t, res.Body)
		backup, _ := got["data"].(map[string]any)["backupPath"].(string)
		if !strings.HasPrefix(backup, secret+".bak.") {
			t.Fatalf("second save: %s", res.Body)
		}
		if row := lastAudit(t, s.World); row != "advanced_config_saved|Saved advanced config: "+secret+" (backup: "+backup+")|127.0.0.1|1" {
			t.Errorf("audit %s", row)
		}
		tx = got["restartRequired"].(map[string]any)["id"].(string)
		res = admin.Do("POST", "/api/settings/restart/cancel", `{"id":"`+tx+`"}`)
		if res.Body != `{"success":true,"data":{"canceled":true,"id":"`+tx+`"}}` {
			t.Errorf("cancel: %s", res.Body)
		}
		if row := lastAudit(t, s.World); row != "settings_restart_canceled|Canceled restart transaction "+tx+"|127.0.0.1|1" {
			t.Errorf("audit %s", row)
		}
		if content, _ := os.ReadFile(secret); string(content) != "other-secret-0123456789abcdef0123\n" {
			t.Errorf("content after the cancel: %q", content)
		}
		if res := admin.Do("GET", "/api/settings/restart/pending", ""); res.Body != `{"success":true,"data":null}` {
			t.Errorf("pending at the end: %s", res.Body)
		}
	})
}

// TestConnectionModeWaitsForAnotherOperatorsChange: while the admin's
// connection mode change waits for its restart, the super admin's save
// answers 409 and leaves the compose file as the admin's save wrote it
// (BUGS.md RV94).
func TestConnectionModeWaitsForAnotherOperatorsChange(t *testing.T) {
	if _, err := os.Stat("/etc/systemd/system/neodesk-server.service"); err == nil {
		t.Skip("this machine has the server's systemd unit")
	}
	s := newSite(t, nil, nil, nil)
	compose := filepath.Join(filepath.Dir(s.Console), "no-compose", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(compose), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compose, []byte("services:\n  server:\n    image: neodesk-server:local\n    environment:\n      - P2P_FIRST=Y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := s.As("admin").Do("PUT", "/api/settings/connection-mode", `{"mode":"relay_only"}`)
	if res.Status != http.StatusOK || !strings.Contains(res.Body, `"restartRequired":{`) {
		t.Fatalf("the admin's save: %d %s", res.Status, res.Body)
	}
	saved, err := os.ReadFile(compose)
	if err != nil || !strings.Contains(string(saved), "ALWAYS_USE_RELAY=Y") {
		t.Fatalf("the admin's save wrote %q (%v)", saved, err)
	}
	audits := len(s.AuditRows())
	superAdmin := s.As("super_admin")
	res = superAdmin.Do("PUT", "/api/settings/connection-mode", `{"mode":"p2p_first","p2p_fallback_ms":5000}`)
	if res.Status != http.StatusConflict || res.Body != `{"success":false,"error":"restart_transaction_owned"}` {
		t.Errorf("the super admin's save: %d %s", res.Status, res.Body)
	}
	if now, _ := os.ReadFile(compose); string(now) != string(saved) {
		t.Errorf("the refused save changed the compose file:\n%s\nwas:\n%s", now, saved)
	}
	if len(s.AuditRows()) != audits {
		t.Errorf("the refused save was audited: %s", lastAudit(t, s.World))
	}
	if res := superAdmin.Do("GET", "/api/settings/restart/pending", ""); res.Body != `{"success":true,"data":null}` {
		t.Errorf("the super admin's transaction: %s", res.Body)
	}
}

// Without a systemd unit or a compose file there is nothing to restart
// with: confirming fails the transaction with no_restart. Both routes that
// restart after one setting confirm the caller's transaction.
func TestConfirm(t *testing.T) {
	if _, err := os.Stat("/etc/systemd/system/neodesk-server.service"); err == nil {
		t.Skip("this machine has the server's systemd unit")
	}
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	for _, route := range []string{"/api/settings/advanced/restart", "/api/settings/connection-mode/restart"} {
		body := `{"fileId":"console-env"}`
		res := admin.Do("POST", route, body)
		if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, "settings.") {
			t.Errorf("%s without a transaction: %d %s", route, res.Status, res.Body)
		}
		res = admin.Do("PUT", "/api/settings/advanced/files/go-blocklist", `{"content":"x\n"}`)
		tx := decode(t, res.Body)["restartRequired"].(map[string]any)["id"].(string)
		res = admin.Do("POST", route, body)
		if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"no_restart"}` {
			t.Errorf("%s: %d %s", route, res.Status, res.Body)
		}
		res = admin.Do("POST", "/api/settings/restart/confirm", `{"id":"`+tx+`"}`)
		if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"no_restart"}` {
			t.Errorf("confirm: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("POST", "/api/settings/restart/complete", `{"id":"`+tx+`"}`); res.Status != http.StatusOK {
			t.Errorf("complete: %d %s", res.Status, res.Body)
		}
	}
	res := admin.Do("POST", "/api/settings/advanced/restart", `{"fileId":7}`)
	if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"settings.advanced_error_unknown"}` {
		t.Errorf("number file id: %d %s", res.Status, res.Body)
	}
	for _, path := range []string{"/api/settings/restart/cancel", "/api/settings/restart/confirm", "/api/settings/restart/complete"} {
		res := admin.Do("POST", path, `{"id":["a","b"]}`)
		if res.Status != http.StatusNotFound || res.Body != `{"success":false,"error":"restart_transaction_not_found"}` {
			t.Errorf("%s unknown: %d %s", path, res.Status, res.Body)
		}
	}
}

func TestAdvancedFileRefusals(t *testing.T) {
	s := newSite(t, nil, nil, map[string]string{"NEODESK_BUILD_ENV_FILE": ""})
	admin := s.As("admin")
	if err := os.MkdirAll(s.Keys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Keys, "blocklist.txt"), []byte("a\x00b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Console, ".env.local"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, id, body, want string }{
		{"GET", "nope", "", `404 {"success":false,"error":"settings.advanced_error_unknown"}`},
		{"GET", "console-env", "", `404 {"success":false,"error":"settings.advanced_error_not_found"}`},
		{"GET", "go-blocklist", "", `400 {"success":false,"error":"settings.advanced_error_binary"}`},
		{"GET", "console-env-local", "", `400 {"success":false,"error":"settings.advanced_error_not_file"}`},
		{"PUT", "nope", `{"content":"x"}`, `404 {"success":false,"error":"settings.advanced_error_unknown"}`},
		{"PUT", "console-env", `{"content":"x"}`, `404 {"success":false,"error":"settings.advanced_error_not_found"}`},
		{"PUT", "console-env-local", `{"content":"x"}`, `400 {"success":false,"error":"settings.advanced_error_not_file"}`},
		{"PUT", "go-blocklist", `{"content":5}`, `400 {"success":false,"error":"settings.advanced_error_invalid"}`},
		{"PUT", "go-blocklist", `["content"]`, `400 {"success":false,"error":"settings.advanced_error_invalid"}`},
		{"PUT", "go-blocklist", `{"content":"` + strings.Repeat("é", 512*1024/2+1) + `"}`, `400 {"success":false,"error":"settings.advanced_error_too_large"}`},
	} {
		res := admin.Do(c.method, "/api/settings/advanced/files/"+c.id, c.body)
		if strconv.Itoa(res.Status)+" "+res.Body != c.want {
			t.Errorf("%s %s: %d %.300s, want %s", c.method, c.id, res.Status, res.Body, c.want)
		}
	}
	// A file that needs no restart registers nothing
	if runtime.GOOS == "linux" {
		envFile := filepath.Join(t.TempDir(), "build.env")
		s2 := newSite(t, nil, nil, map[string]string{"NEODESK_BUILD_ENV_FILE": envFile})
		res := s2.As("admin").Do("PUT", "/api/settings/advanced/files/build-env", `{"content":"A=1\n"}`)
		if res.Status != http.StatusOK || !strings.HasSuffix(res.Body, `"created":true,"restartRequired":null},"message":"settings.advanced_saved","restartRequired":null}`) {
			t.Errorf("build env: %d %s", res.Status, res.Body)
		}
	}
	res := admin.Do("GET", "/api/settings/advanced/files", "")
	if res.Status != http.StatusOK || !strings.Contains(res.Body, `{"id":"console-env","path":"`+filepath.Join(s.Console, ".env")+`","category":"console","requiresRestart":"console","canCreate":false,"exists":false,"size":0,"mtime":null,"readable":false,"writable":false}`) {
		t.Errorf("list: %s", res.Body)
	}
}

func TestInfoAndServerInfo(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db, nil, nil)
		s.API.On("GET /api/health", http.StatusOK, `{"status":"ok","version":"9.9.9","uptime":"1m"}`)
		if _, err := db.Exec(context.Background(), `INSERT INTO peer (id, is_deleted, status_online, is_banned) VALUES ('a', `+db.Pick("0, 1, 0", "FALSE, TRUE, FALSE")+`), ('b', `+db.Pick("0, 0, 1", "FALSE, FALSE, TRUE")+`), ('c', `+db.Pick("1, 1, 1", "TRUE, TRUE, TRUE")+`)`); err != nil {
			t.Fatal(err)
		}
		// Every account reads the versions and the server's status (PA3,
		// F1); the rest is for server.config (BUGS RV98, A83)
		for _, role := range []string{"viewer", "operator", "global_admin"} {
			res := s.As(role).Do("GET", "/api/settings/info", "")
			if want := `{"success":true,"data":{"app":{"name":"NeoDesk Console","version":"4.5.6"},"goServer":{"status":"running","version":"9.9.9"}}}`; res.Status != http.StatusOK || res.Body != want {
				t.Errorf("%s: %d %s, want %s", role, res.Status, res.Body, want)
			}
		}
		res := s.As("server_admin").Do("GET", "/api/settings/info", "")
		if !strings.Contains(res.Body, `"paths":{"database":"/srv/db_v2.sqlite3"`) {
			t.Errorf("server_admin: %d %s", res.Status, res.Body)
		}
		res = s.As("admin").Do("GET", "/api/settings/info", "")
		got := decode(t, res.Body)
		data := got["data"].(map[string]any)
		if keys := keyOrder(t, rawField(t, res.Body, "data")); keys != "app,server,goServer,backend,paths,stats" {
			t.Errorf("keys %s", keys)
		}
		app := data["app"].(map[string]any)
		if app["name"] != "NeoDesk Console" || app["nodeVersion"] != runtime.Version() || app["env"] != "production" || app["version"] != "4.5.6" {
			t.Errorf("app %v", app)
		}
		server := data["server"].(map[string]any)
		host, _ := os.Hostname()
		platform := runtime.GOOS
		if platform == "windows" {
			platform = "win32"
		}
		if server["hostname"] != host || server["platform"] != platform {
			t.Errorf("server %v", server)
		}
		if runtime.GOARCH == "amd64" && server["arch"] != "x64" {
			t.Errorf("arch %v", server["arch"])
		}
		if goServer := data["goServer"].(map[string]any); goServer["status"] != "running" || goServer["version"] != "9.9.9" || goServer["backend"] != "neodesk" {
			t.Errorf("goServer %v", goServer)
		}
		if data["backend"] != "neodesk" || !reflect.DeepEqual(data["paths"], map[string]any{
			"database": "/srv/db_v2.sqlite3", "publicKey": "/srv/id_ed25519.pub", "apiKey": filepath.Join(s.Keys, ".api_key"),
		}) || !reflect.DeepEqual(data["stats"], map[string]any{"total": 2.0, "online": 1.0, "banned": 1.0, "offline": 1.0}) {
			t.Errorf("info %s", res.Body)
		}

		if err := os.MkdirAll(s.Keys, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Keys, ".api_key"), []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
			t.Fatal(err)
		}
		plain := s.As("admin").Do("GET", "/api/settings/server-info", "")
		asked := s.As("admin").Do("GET", "/api/settings/server-info?host=other.example.org", "")
		if plain.Status != http.StatusOK || plain.Body != asked.Body || strings.Contains(asked.Body, "other.example.org") ||
			!strings.Contains(plain.Body, `"api_key_masked":"0123****cdef","endpoint_sources":`) {
			t.Errorf("server-info: %s / %s", plain.Body, asked.Body)
		}
		if err := os.Remove(filepath.Join(s.Keys, ".api_key")); err != nil {
			t.Fatal(err)
		}
		if res := s.As("admin").Do("GET", "/api/settings/server-info", ""); !strings.Contains(res.Body, `"api_key_masked":"••••••••"`) {
			t.Errorf("server-info without a key: %s", res.Body)
		}
	})
}

// rawField is the JSON text of a member of the object in body.
func rawField(t *testing.T, body, name string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("not an object: %s", body)
	}
	return string(fields[name])
}

// The writes need server.config. The panel guard of the pipeline refuses
// every account without it first, so this test sends the requests to the
// group's route table alone.
func TestPermissionGuardsOfWrites(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	settings.Register(d)
	who := &access.Context{UserID: 6, HasUser: true, Role: "viewer", Loaded: true,
		Permissions: access.PermissionSet{"audit.view": {}, "settings.view": {}}}
	for _, route := range []string{
		"POST /api/settings/restart/cancel", "POST /api/settings/restart/confirm", "POST /api/settings/restart/complete",
		"POST /api/settings/device-scope", "POST /api/settings/availability-history", "PUT /api/settings/enrollment",
		"PUT /api/settings/ldap", "POST /api/settings/ldap/test", "PUT /api/settings/client-sessions",
		"PUT /api/settings/two-factor", "PUT /api/settings/oidc", "POST /api/settings/oidc/test", "PUT /api/settings/advanced/files/console-env",
		"POST /api/settings/advanced/restart", "PUT /api/settings/connection-mode",
		"POST /api/settings/connection-mode/restart", "PUT /api/settings/public-endpoints",
		"PUT /api/settings/email/smtp", "POST /api/settings/email/smtp/test",
	} {
		method, path, _ := strings.Cut(route, " ")
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r = kit.Begin(r, httpx.TrustProxy{}, nil)
		kit.SetAccess(r, who)
		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if recover() != nil {
					t.Errorf("%s: the handler ran", route)
				}
			}()
			if !d.Router.Serve(rec, r) {
				t.Errorf("%s: no route", route)
			}
		}()
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Permission denied: server.config") {
			t.Errorf("%s: %d %s", route, rec.Code, rec.Body)
		}
	}
}
