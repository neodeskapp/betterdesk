package console

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// tokenWorld is a console with probe routes behind the guards the token
// rules meet: a permission, a panel section (users, devices), the admin
// role, and a route that reports what it sees of the request.
func tokenWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, setup{})
	d := w.s.Deps()
	r := w.s.Router()
	seen := func(rw http.ResponseWriter, req *http.Request) {
		kit.JSON(rw, req, http.StatusOK, map[string]any{
			"user":          kit.UserID(req),
			"authorization": req.Header.Get("Authorization"),
			"cookie":        req.Header.Get("Cookie"),
		})
	}
	r.Get("/api/zz/seen", seen, d.RequireAuth)
	r.Post("/api/zz/seen", seen, d.RequireAuth)
	r.Get("/api/zz/audit-view", probe, d.RequireAuth, d.RequirePermission("audit.view"))
	r.Get("/api/zz/admin", probe, d.RequireAuth, d.RequireAdmin)
	r.Get("/api/users/zz-probe", probe, d.RequireAuth)
	r.Post("/api/tags/zz/probe", probe, d.RequireAuth)
	r.Get("/api/tags/zz/probe", probe, d.RequireAuth)
	r.Post("/api/users/zz-probe", probe, d.RequireAuth)
	return w
}

// makeToken makes a token as the role's test account in a browser session
// and returns its whole value and its public id.
func (w *world) makeToken(role, body string) (string, string) {
	w.t.Helper()
	res := w.agent().signIn(role).post("/api/panel/api-tokens", body)
	if res.status != http.StatusCreated {
		w.t.Fatalf("make a token as %s: %d %s", role, res.status, res.body)
	}
	data, _ := res.json(w.t)["data"].(map[string]any)
	value, _ := data["token"].(string)
	info, _ := data["api_token"].(map[string]any)
	id, _ := info["id"].(string)
	if value == "" || id == "" {
		w.t.Fatalf("no token in %s", res.body)
	}
	return value, id
}

// bearer is an agent without cookies that sends the token.
func (w *world) bearer(value string) *agent {
	a := w.agent()
	a.always = []string{"Authorization", "Bearer " + value}
	return a
}

func (a *agent) postJSON(target, body string, header ...string) answer {
	a.w.t.Helper()
	return a.do("POST", target, body, append([]string{"Content-Type", "application/json"}, header...)...)
}

const allAdmin = `["audit.view","blocklist.edit","branding.edit","billing.export","billing.manage","billing.reports","billing.view",
"cdap.command","cdap.files","cdap.terminal","cdap.view","chat.access","device.ban","device.change_id","device.connect",
"device.connection_mode","device.delete","device.edit","device.view","enrollment.approve","enrollment.manage","guest.create",
"mesh.files","mesh.power","mesh.terminal","metrics.view","org.create","org.delete","org.edit","org.manage_devices","org.manage_users",
"remote_target.connect","remote_target.delete","remote_target.edit","remote_target.test","remote_target.view",
"server.attestation","server.config","server.keys","user.create","user.delete","user.edit","user.view"]`

func code(t *testing.T, a answer) string {
	t.Helper()
	var out map[string]any
	_ = json.Unmarshal([]byte(a.body), &out)
	c, _ := out["code"].(string)
	return c
}

// A token is made in a browser session by an admin, shown once, kept as
// its hash, and starts with ndt_ so secret scanners find it.
func TestAPITokenIsMadeOnceAndKeptAsAHash(t *testing.T) {
	w := tokenWorld(t)
	value, id := w.makeToken("super_admin", `{"name":"ci","permissions":["audit.view"],"expires_in_days":30}`)
	if _, ok := admintoken.Parse(value); !ok || !strings.HasPrefix(value, "ndt_"+id+"_") {
		t.Errorf("token %q is not ndt_<id>_<secret>", value)
	}
	var stored, hash string
	if err := w.db.QueryRow(context.Background(), "SELECT public_id, token_hash FROM admin_tokens").Scan(&stored, &hash); err != nil {
		t.Fatal(err)
	}
	if stored != id || hash == "" || strings.Contains(hash, value[len("ndt_"+id+"_"):]) {
		t.Errorf("row: id %q hash %q", stored, hash)
	}
	list := w.agent().signIn("super_admin").get("/api/panel/api-tokens")
	if list.status != 200 || strings.Contains(list.body, value) || strings.Contains(list.body, hash) || !strings.Contains(list.body, `"hint":"ndt_`+id+`_…"`) {
		t.Errorf("the list shows the token or its hash, or no hint: %s", list.body)
	}
	for _, row := range w.auditRows() {
		if strings.Contains(row, value) {
			t.Errorf("audit entry holds the token: %s", row)
		}
	}
	if !strings.Contains(strings.Join(w.auditRows(), "\n"), "api_token_created|API token ci (ndt_"+id+"_…): read only, audit.view;") {
		t.Errorf("no audit entry for the new token: %v", w.auditRows())
	}
}

func TestOnlyAdminsInABrowserMakeTokens(t *testing.T) {
	w := tokenWorld(t)
	for _, role := range []string{"operator", "viewer", "guest_issuer"} {
		res := w.agent().signIn(role).post("/api/panel/api-tokens", `{"name":"x","permissions":["device.view"],"expires_in_days":1}`)
		if res.status != 403 {
			t.Errorf("%s made a token: %d %s", role, res.status, res.body)
		}
	}
	// A token never makes another one, full and read-write as it may be
	value, _ := w.makeToken("super_admin", `{"name":"full","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	res := w.bearer(value).postJSON("/api/panel/api-tokens", `{"name":"child","permissions":["audit.view"],"expires_in_days":1}`)
	if res.status != 403 || code(t, res) != "token_not_allowed" {
		t.Errorf("a token made a token: %d %s", res.status, res.body)
	}
}

// What a token is given can never exceed what its creator holds, and the
// fields are checked.
func TestATokenGetsOnlyItsCreatorsPermissions(t *testing.T) {
	w := tokenWorld(t)
	a := w.agent().signIn("server_admin")
	for body, field := range map[string]string{
		`{"name":"x","permissions":["user.create"],"expires_in_days":1}`:                   "permissions",
		`{"name":"x","permissions":["no.such"],"expires_in_days":1}`:                       "permissions",
		`{"name":"x","permissions":[],"expires_in_days":1}`:                                "permissions",
		`{"name":"","permissions":["audit.view"],"expires_in_days":1}`:                     "name",
		`{"name":"x","permissions":["audit.view"]}`:                                        "expires_at",
		`{"name":"x","permissions":["audit.view"],"expires_in_days":400}`:                  "expires_at",
		`{"name":"x","permissions":["audit.view"],"expires_at":"2001-01-01"}`:              "expires_at",
		`{"name":"x","permissions":["audit.view"],"expires_in_days":1,"allow_list":["x"]}`: "allow_list",
	} {
		res := a.post("/api/panel/api-tokens", body)
		var out map[string]any
		_ = json.Unmarshal([]byte(res.body), &out)
		if res.status != 400 || out["field"] != field {
			t.Errorf("%s: %d %s, want 400 on %s", body, res.status, res.body, field)
		}
	}
	if res := a.post("/api/panel/api-tokens", `{"name":"ok","permissions":["audit.view","server.keys"],"expires_in_days":1}`); res.status != 201 {
		t.Errorf("permissions it holds: %d %s", res.status, res.body)
	}
	// server.config reaches the server's files and secrets: only together
	// with every other permission of the creator
	res := a.post("/api/panel/api-tokens", `{"name":"cfg","permissions":["audit.view","server.config"],"expires_in_days":1}`)
	if res.status != 400 || !strings.Contains(res.body, "server.config only together with all of your permissions") {
		t.Errorf("server.config without the rest: %d %s", res.status, res.body)
	}
	if res := a.post("/api/panel/api-tokens", `{"name":"all","permissions":`+serverAdminAll+`,"expires_in_days":1}`); res.status != 201 {
		t.Errorf("server.config with all of them: %d %s", res.status, res.body)
	}
}

const serverAdminAll = `["audit.view","blocklist.edit","device.view","enrollment.manage","metrics.view","remote_target.view","server.attestation","server.config","server.keys","user.view"]`

// A token is read only as a Bearer header: not from the address, a cookie
// or the API key header.
func TestATokenIsOnlyABearerHeader(t *testing.T) {
	w := tokenWorld(t)
	value, _ := w.makeToken("super_admin", `{"name":"x","permissions":["audit.view"],"expires_in_days":1}`)
	if res := w.bearer(value).get("/api/zz/audit-view"); res.status != 200 {
		t.Fatalf("Bearer header: %d %s", res.status, res.body)
	}
	for name, res := range map[string]answer{
		"query":     w.agent().get("/api/zz/audit-view?access_token=" + value),
		"token":     w.agent().get("/api/zz/audit-view?token=" + value),
		"cookie":    w.agent().get("/api/zz/audit-view", "Cookie", "token="+value),
		"x-api-key": w.agent().get("/api/zz/audit-view", "X-API-Key", value),
		"basic":     w.agent().get("/api/zz/audit-view", "Authorization", "Basic "+value),
	} {
		if res.status != 401 {
			t.Errorf("token in the %s: %d %s", name, res.status, res.body)
		}
	}
}

// The route behind sees neither the token's header nor the cookies, and
// a session cookie that came along signs nobody in.
func TestATokenRequestCarriesNoHeaderOrCookieOn(t *testing.T) {
	w := tokenWorld(t)
	value, _ := w.makeToken("global_admin", `{"name":"x","permissions":["audit.view"],"read_only":false,"expires_in_days":1}`)
	admin := w.agent().signIn("super_admin")
	admin.always = []string{"Authorization", "Bearer " + value}
	res := admin.do("POST", "/api/zz/seen", "{}", "Content-Type", "application/json")
	out := res.json(t)
	if res.status != 200 || out["user"] != float64(roleIDs["global_admin"]) || out["authorization"] != "" || out["cookie"] != "" {
		t.Errorf("with a super admin's cookie and no CSRF token: %d %s", res.status, res.body)
	}
	if res.header.Get("Set-Cookie") != "" {
		t.Errorf("a token's answer sets cookies: %v", res.header.Values("Set-Cookie"))
	}
}

func TestATokenIsCappedToItsPermissionsAndSections(t *testing.T) {
	w := tokenWorld(t)
	value, _ := w.makeToken("super_admin", `{"name":"x","permissions":["device.view"],"read_only":false,"expires_in_days":1}`)
	a := w.bearer(value)
	// A permission the account has and the token was not given
	if res := a.get("/api/zz/audit-view"); res.status != 403 || code(t, res) != "token_permission" {
		t.Errorf("audit.view without it in the token: %d %s", res.status, res.body)
	}
	// The users section needs user.view to read
	if res := a.get("/api/users/zz-probe"); res.status != 403 || code(t, res) != "token_permission" {
		t.Errorf("users section: %d %s", res.status, res.body)
	}
	// Devices: read with device.view, write needs device.edit
	if res := a.get("/api/tags/zz/probe"); res.status != 200 {
		t.Errorf("devices read: %d %s", res.status, res.body)
	}
	if res := a.postJSON("/api/tags/zz/probe", "{}"); res.status != 403 || code(t, res) != "token_permission" {
		t.Errorf("devices write: %d %s", res.status, res.body)
	}
}

func TestATokenLosesWhatItsCreatorLoses(t *testing.T) {
	w := tokenWorld(t)
	value, _ := w.makeToken("global_admin", `{"name":"x","permissions":["audit.view","user.view"],"expires_in_days":1}`)
	a := w.bearer(value)
	if res := a.get("/api/users/zz-probe"); res.status != 200 {
		t.Fatalf("before: %d %s", res.status, res.body)
	}
	// The account becomes a viewer: no user.view any more
	if _, err := w.db.Exec(context.Background(), "UPDATE users SET role = 'viewer' WHERE id = ?", roleIDs["global_admin"]); err != nil {
		t.Fatal(err)
	}
	if res := a.get("/api/users/zz-probe"); res.status != 403 {
		t.Errorf("after the creator lost user.view: %d %s", res.status, res.body)
	}
	if res := a.get("/api/zz/audit-view"); res.status != 200 {
		t.Errorf("audit.view, which a viewer keeps: %d %s", res.status, res.body)
	}
	// Disabled, then deleted: the token is refused
	if _, err := w.db.Exec(context.Background(), "UPDATE users SET disabled = 1 WHERE id = ?", roleIDs["global_admin"]); err != nil {
		t.Fatal(err)
	}
	if res := a.get("/api/zz/audit-view"); res.status != 401 || code(t, res) != "token_invalid" {
		t.Errorf("creator disabled: %d %s", res.status, res.body)
	}
	if _, err := w.db.Exec(context.Background(), "DELETE FROM users WHERE id = ?", roleIDs["global_admin"]); err != nil {
		t.Fatal(err)
	}
	if res := a.get("/api/zz/audit-view"); res.status != 401 {
		t.Errorf("creator deleted: %d %s", res.status, res.body)
	}
}

func TestARevokedOrExpiredTokenIsRefused(t *testing.T) {
	w := tokenWorld(t)
	value, id := w.makeToken("super_admin", `{"name":"x","permissions":["audit.view"],"expires_in_days":1}`)
	other, otherID := w.makeToken("super_admin", `{"name":"y","permissions":["audit.view"],"expires_in_days":1}`)
	admin := w.agent().signIn("super_admin")
	if res := admin.do("DELETE", "/api/panel/api-tokens/"+id, "", "X-CSRF-Token", admin.csrf()); res.status != 200 || !strings.Contains(res.body, `"state":"revoked"`) {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	if res := w.bearer(value).get("/api/zz/audit-view"); res.status != 401 || code(t, res) != "token_invalid" || res.header.Get("WWW-Authenticate") == "" {
		t.Errorf("revoked: %d %s", res.status, res.body)
	}
	if _, err := w.db.Exec(context.Background(), "UPDATE admin_tokens SET expires_at = ? WHERE public_id = ?", time.Now().Add(-time.Minute).UnixMilli(), otherID); err != nil {
		t.Fatal(err)
	}
	if res := w.bearer(other).get("/api/zz/audit-view"); res.status != 401 {
		t.Errorf("expired: %d %s", res.status, res.body)
	}
	audit := strings.Join(w.auditRows(), "\n")
	for _, want := range []string{"api_token_revoked|API token x (ndt_" + id + "_…) of e2e_super_admin revoked", "api_token_refused|ndt_" + id + "_… refused: revoked", "api_token_refused|ndt_" + otherID + "_… refused: expired"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit lacks %q:\n%s", want, audit)
		}
	}
}

func TestRevokingOthersTokensIsForSuperAdmins(t *testing.T) {
	w := tokenWorld(t)
	_, id := w.makeToken("global_admin", `{"name":"x","permissions":["audit.view"],"expires_in_days":1}`)
	server := w.agent().signIn("server_admin")
	if res := server.do("DELETE", "/api/panel/api-tokens/"+id, "", "X-CSRF-Token", server.csrf()); res.status != 404 {
		t.Errorf("another admin revoked it: %d %s", res.status, res.body)
	}
	if res := server.get("/api/panel/api-tokens?all=true"); res.status != 403 {
		t.Errorf("server admin listed all tokens: %d %s", res.status, res.body)
	}
	super := w.agent().signIn("super_admin")
	if res := super.get("/api/panel/api-tokens?all=true"); res.status != 200 || !strings.Contains(res.body, id) {
		t.Errorf("super admin's list of all: %d %s", res.status, res.body)
	}
	if res := super.do("DELETE", "/api/panel/api-tokens/"+id, "", "X-CSRF-Token", super.csrf()); res.status != 200 {
		t.Errorf("super admin revokes it: %d %s", res.status, res.body)
	}
}

func TestATokenOnlyWorksFromItsAllowList(t *testing.T) {
	w := tokenWorld(t)
	far, _ := w.makeToken("super_admin", `{"name":"far","permissions":["audit.view"],"expires_in_days":1,"allow_list":["203.0.113.0/24"]}`)
	near, _ := w.makeToken("super_admin", `{"name":"near","permissions":["audit.view"],"expires_in_days":1,"allow_list":["127.0.0.1","2001:db8::/32"]}`)
	if res := w.bearer(far).get("/api/zz/audit-view"); res.status != 401 {
		t.Errorf("from an address outside the list: %d %s", res.status, res.body)
	}
	if res := w.bearer(near).get("/api/zz/audit-view"); res.status != 200 {
		t.Errorf("from an address on the list: %d %s", res.status, res.body)
	}
}

func TestAReadOnlyTokenOnlyReads(t *testing.T) {
	w := tokenWorld(t)
	read, _ := w.makeToken("super_admin", `{"name":"r","permissions":["device.view","device.edit"],"expires_in_days":1}`)
	write, _ := w.makeToken("super_admin", `{"name":"w","permissions":["device.view","device.edit"],"read_only":false,"expires_in_days":1}`)
	if res := w.bearer(read).get("/api/tags/zz/probe"); res.status != 200 {
		t.Errorf("read-only token reads: %d %s", res.status, res.body)
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if res := w.bearer(read).do(method, "/api/tags/zz/probe", "{}", "Content-Type", "application/json"); res.status != 403 || code(t, res) != "token_read_only" {
			t.Errorf("read-only token %s: %d %s", method, res.status, res.body)
		}
	}
	if res := w.bearer(write).postJSON("/api/tags/zz/probe", "{}"); res.status != 200 {
		t.Errorf("read-write token writes: %d %s", res.status, res.body)
	}
}

// Role checks and changes to accounts and access need a token that holds
// every permission of its account.
func TestOnlyAFullTokenPassesRoleChecksAndChangesAccounts(t *testing.T) {
	w := tokenWorld(t)
	capped, _ := w.makeToken("super_admin", `{"name":"c","permissions":["user.view","user.create","user.edit"],"read_only":false,"expires_in_days":1}`)
	full, _ := w.makeToken("super_admin", `{"name":"f","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	if res := w.bearer(capped).get("/api/zz/admin"); res.status != 403 || code(t, res) != "token_not_full" {
		t.Errorf("capped token, admin route: %d %s", res.status, res.body)
	}
	if res := w.bearer(full).get("/api/zz/admin"); res.status != 200 {
		t.Errorf("full token, admin route: %d %s", res.status, res.body)
	}
	for _, path := range []string{"/api/users/zz-probe", "/api/users", "/api/panel/access/users/1", "/api/panel/role-permissions", "/api/panel/user-groups", "/api/settings/oidc"} {
		if res := w.bearer(capped).postJSON(path, "{}"); res.status != 403 || code(t, res) != "token_not_full" {
			t.Errorf("capped token POST %s: %d %s", path, res.status, res.body)
		}
	}
	// Sign-in and access settings are account changes too
	for _, c := range []struct{ method, path string }{{"PUT", "/api/settings/two-factor"}, {"POST", "/api/settings/device-scope"}, {"PUT", "/api/settings/client-sessions"}, {"PUT", "/api/settings/enrollment"}} {
		if res := w.bearer(capped).do(c.method, c.path, "{}", "Content-Type", "application/json"); res.status != 403 || code(t, res) != "token_not_full" {
			t.Errorf("capped token %s %s: %d %s", c.method, c.path, res.status, res.body)
		}
	}
	// Role checks inside the handlers: tenants, and every account's tokens
	if res := w.bearer(capped).postJSON("/api/tenants", `{"name":"evil"}`); res.status != 403 {
		t.Errorf("capped super admin token makes a tenant: %d %s", res.status, res.body)
	}
	if res := w.bearer(capped).get("/api/panel/api-tokens?all=true"); res.status != 403 {
		t.Errorf("capped token lists every account's tokens: %d %s", res.status, res.body)
	}
	_, other := w.makeToken("global_admin", `{"name":"other","permissions":["audit.view"],"expires_in_days":1}`)
	if res := w.bearer(capped).do("DELETE", "/api/panel/api-tokens/"+other, ""); res.status != 404 {
		t.Errorf("capped token revokes another account's token: %d %s", res.status, res.body)
	}
	if res := w.bearer(full).get("/api/panel/api-tokens?all=true"); res.status != 200 || !strings.Contains(res.body, other) {
		t.Errorf("full super admin token lists every account's tokens: %d %s", res.status, res.body)
	}
	// All but server.config: past the reports section, not full
	most, _ := w.makeToken("super_admin", `{"name":"m","permissions":`+strings.Replace(allAdmin, `"server.config",`, "", 1)+`,"read_only":false,"expires_in_days":1}`)
	if res := w.bearer(most).postJSON("/api/reports/save", `{}`); res.status != 403 {
		t.Errorf("capped super admin token saves a report: %d %s", res.status, res.body)
	}
	if res := w.bearer(full).postJSON("/api/reports/save", `{}`); res.status == 403 {
		t.Errorf("full super admin token saves a report: %d %s", res.status, res.body)
	}
	// A super admin sees every account's CDAP Studio flows; a capped token
	// only its own account's
	if _, err := w.db.Exec(context.Background(),
		"INSERT INTO studio_flows (id, name, description, flow_json, status, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		"zz-flow", "Someone else's flow", "", "{}", "draft", "someone-else", "2026-11-23T00:00:00.000Z", "2026-11-23T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	if res := w.bearer(capped).get("/api/cdap-studio/flows"); strings.Contains(res.body, "zz-flow") {
		t.Errorf("capped super admin token lists another account's flow: %d %s", res.status, res.body)
	}
	if res := w.bearer(capped).get("/api/cdap-studio/flows/zz-flow"); res.status != 404 {
		t.Errorf("capped super admin token reads another account's flow: %d %s", res.status, res.body)
	}
	if res := w.bearer(full).get("/api/cdap-studio/flows"); !strings.Contains(res.body, "zz-flow") {
		t.Errorf("full super admin token lists every flow: %d %s", res.status, res.body)
	}
	if res := w.bearer(full).postJSON("/api/users/zz-probe", "{}"); res.status != 200 {
		t.Errorf("full token changes accounts: %d %s", res.status, res.body)
	}
	// A server admin's full token is its own permissions, not a super admin's
	serverFull, _ := w.makeToken("server_admin", `{"name":"s","permissions":`+serverAdminAll+`,"read_only":false,"expires_in_days":1}`)
	if res := w.bearer(serverFull).get("/api/zz/admin"); res.status != 200 {
		t.Errorf("server admin's full token, admin route: %d %s", res.status, res.body)
	}
}

// The server's secrets (the full backup and restore, the file browser,
// the advanced settings files, where scheduled backups go) need a
// read-write token that carries every permission of its account, also to
// read; server.config itself goes from a token that stopped being full.
func TestTheServersSecretsNeedAFullReadWriteToken(t *testing.T) {
	w := tokenWorld(t)
	fullWrite, _ := w.makeToken("super_admin", `{"name":"fw","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	fullRead, _ := w.makeToken("super_admin", `{"name":"fr","permissions":`+allAdmin+`,"expires_in_days":1}`)
	server, _ := w.makeToken("server_admin", `{"name":"s","permissions":`+serverAdminAll+`,"read_only":false,"expires_in_days":1}`)
	secrets := []struct{ method, path string }{
		{"GET", "/api/settings/backup"}, {"POST", "/api/settings/restore"},
		{"GET", "/api/server-management/files?path=/"}, {"GET", "/api/server-management/files/read?path=/etc/hostname"},
		{"GET", "/api/settings/advanced/files"}, {"GET", "/api/settings/advanced/files/console-session-secret"},
		{"PUT", "/api/settings/advanced/files/console-env"},
		{"PUT", "/api/settings/backup/schedule"}, {"POST", "/api/settings/backup/schedule/run"}, {"POST", "/api/settings/backup/destination/test"},
	}
	for _, c := range secrets {
		if c.method == "GET" {
			if res := w.bearer(fullRead).get(c.path); res.status != 403 || code(t, res) != "token_not_full" {
				t.Errorf("full read-only token %s %s: %d %s", c.method, c.path, res.status, res.body)
			}
		}
		if res := w.bearer(fullWrite).do(c.method, c.path, "{}", "Content-Type", "application/json"); res.status == 403 && strings.Contains(res.body, "token") {
			t.Errorf("full read-write token %s %s: %d %s", c.method, c.path, res.status, res.body)
		}
	}
	// Reading the schedule is no secret
	if res := w.bearer(fullRead).get("/api/settings/backup/schedule"); res.status == 403 {
		t.Errorf("full read-only token reads the schedule: %d %s", res.status, res.body)
	}
	// The server admin gains permissions: its token is no longer full,
	// loses server.config and with it the settings and the secrets
	if res := w.bearer(server).get("/api/settings/backup/schedule"); res.status != 200 {
		t.Fatalf("before: %d %s", res.status, res.body)
	}
	if _, err := w.db.Exec(context.Background(), "UPDATE users SET role = 'super_admin' WHERE id = ?", roleIDs["server_admin"]); err != nil {
		t.Fatal(err)
	}
	if res := w.bearer(server).get("/api/settings/backup/schedule"); res.status != 403 {
		t.Errorf("settings without server.config: %d %s", res.status, res.body)
	}
	if res := w.bearer(server).get("/api/server-management/files/read?path=/etc/hostname"); res.status != 403 || code(t, res) != "token_not_full" {
		t.Errorf("file browser: %d %s", res.status, res.body)
	}
	audit := strings.Join(w.auditRows(), "\n")
	if !strings.Contains(audit, "api_token_refused|") || !strings.Contains(audit, "the server's secrets") {
		t.Errorf("no refusal in the audit log:\n%s", audit)
	}
}

// A token never changes its own account, also not through the account
// routes by its id: a stolen token must not lock its owner out.
func TestATokenNeverChangesItsOwnAccount(t *testing.T) {
	w := tokenWorld(t)
	full, _ := w.makeToken("super_admin", `{"name":"f","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	own := strconv.FormatInt(roleIDs["super_admin"], 10)
	for _, c := range []struct{ method, path string }{
		{"PATCH", "/api/users/" + own}, {"POST", "/api/users/" + own + "/reset-password"}, {"DELETE", "/api/users/" + own},
	} {
		if res := w.bearer(full).do(c.method, c.path, `{"newPassword":"Other-pass-123"}`, "Content-Type", "application/json"); res.status != 403 || code(t, res) != "token_not_allowed" {
			t.Errorf("%s %s: %d %s", c.method, c.path, res.status, res.body)
		}
	}
	// Another account goes on to the route (which then decides)
	if res := w.bearer(full).postJSON("/api/users/"+strconv.FormatInt(roleIDs["viewer"], 10)+"/reset-password", `{}`); code(t, res) == "token_not_allowed" {
		t.Errorf("another account: %d %s", res.status, res.body)
	}
}

// A read-only token that tries to write leaves an audit entry.
func TestRefusalsOfAValidTokenAreAudited(t *testing.T) {
	w := tokenWorld(t)
	value, id := w.makeToken("super_admin", `{"name":"r","permissions":["device.view"],"expires_in_days":1}`)
	w.bearer(value).postJSON("/api/tags/zz/probe", "{}")
	w.bearer(value).get("/api/zz/audit-view")
	audit := strings.Join(w.auditRows(), "\n")
	if !strings.Contains(audit, "api_token_refused|ndt_"+id+"_… refused: read-only token, POST /api/tags/zz/probe") {
		t.Errorf("no audit entry for the refused write:\n%s", audit)
	}
}

func TestSomeRoutesAreNeverOpenToTokens(t *testing.T) {
	w := tokenWorld(t)
	full, _ := w.makeToken("super_admin", `{"name":"f","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	a := w.bearer(full)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/auth/password"}, {"POST", "/api/auth/logout"}, {"POST", "/api/auth/totp/disable"},
		{"GET", "/api/auth/sessions"}, {"POST", "/api/users/me/profile"}, {"GET", "/api/keys/api?show=true"},
		{"GET", "/api/keys/api?show=TRUE"}, {"POST", "/api/auth/login"},
	} {
		if res := a.do(c.method, c.path, "{}", "Content-Type", "application/json"); res.status != 403 || code(t, res) != "token_not_allowed" {
			t.Errorf("%s %s: %d %s", c.method, c.path, res.status, res.body)
		}
	}
	if res := a.get("/api/auth/verify"); res.status == 403 {
		t.Errorf("GET /api/auth/verify: %d %s", res.status, res.body)
	}
}

func TestATokensWritesAreAudited(t *testing.T) {
	w := tokenWorld(t)
	value, id := w.makeToken("super_admin", `{"name":"x","permissions":["device.view","device.edit"],"read_only":false,"expires_in_days":1}`)
	w.bearer(value).get("/api/tags/zz/probe")
	w.bearer(value).postJSON("/api/tags/zz/probe", `{"secret":"`+value+`"}`)
	audit := strings.Join(w.auditRows(), "\n")
	if strings.Count(audit, "api_token_write|") != 1 || !strings.Contains(audit, "api_token_write|ndt_"+id+"_… POST /api/tags/zz/probe -> 200|127.0.0.1|2") {
		t.Errorf("one write entry with the token, method, path, status, address and creator:\n%s", audit)
	}
	if strings.Contains(audit, value) {
		t.Errorf("the audit log holds the token:\n%s", audit)
	}
	// Last use is noted
	list := w.agent().signIn("super_admin").get("/api/panel/api-tokens")
	if !strings.Contains(list.body, `"last_used_ip":"127.0.0.1"`) || strings.Contains(list.body, `"last_used_at":null`) {
		t.Errorf("last use not noted: %s", list.body)
	}
}

func TestTokenFailuresAreRateLimited(t *testing.T) {
	w := tokenWorld(t)
	value, _ := w.makeToken("super_admin", `{"name":"x","permissions":["audit.view"],"expires_in_days":1}`)
	limited, err := admintoken.New(context.Background(), admintoken.Options{DB: w.db, FailureLimit: 3, RequestLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	w.s.tokens = limited
	// Requests per token
	for i := 0; i < 5; i++ {
		if res := w.bearer(value).get("/api/zz/audit-view"); res.status != 200 {
			t.Fatalf("request %d: %d %s", i, res.status, res.body)
		}
	}
	if res := w.bearer(value).get("/api/zz/audit-view"); res.status != 429 || res.header.Get("Retry-After") == "" || code(t, res) != "token_rate_limited" {
		t.Errorf("request over the token's limit: %d %s", res.status, res.body)
	}
	// Failures per address: then the address's bad tokens get 429, but a
	// good token still works (another client cannot lock it out)
	w.s.tokens, _ = admintoken.New(context.Background(), admintoken.Options{DB: w.db, FailureLimit: 3})
	bad := value[:len(value)-1] + "x"
	if bad == value {
		bad = value[:len(value)-1] + "y"
	}
	for i := 0; i < 3; i++ {
		if res := w.bearer(bad).get("/api/zz/audit-view"); res.status != 401 {
			t.Fatalf("bad token %d: %d %s", i, res.status, res.body)
		}
	}
	if res := w.bearer(bad).get("/api/zz/audit-view"); res.status != 429 || code(t, res) != "token_rate_limited" {
		t.Errorf("bad token after the address's failures: %d %s", res.status, res.body)
	}
	if res := w.bearer(value).get("/api/zz/audit-view"); res.status != 200 {
		t.Errorf("good token after the address's failures: %d %s", res.status, res.body)
	}
}

func TestCurrentTellsTheTokenAndWhatItMayDo(t *testing.T) {
	w := tokenWorld(t)
	value, id := w.makeToken("global_admin", `{"name":"me","permissions":["audit.view","user.view"],"expires_in_days":1}`)
	res := w.bearer(value).get("/api/panel/api-tokens/current")
	if res.status != 200 || !strings.Contains(res.body, `"id":"`+id+`"`) || !strings.Contains(res.body, `"username":"e2e_global_admin"`) ||
		!strings.Contains(res.body, `"permissions":["audit.view","user.view"],"full":false`) {
		t.Errorf("current: %d %s", res.status, res.body)
	}
	if res := w.agent().signIn("super_admin").get("/api/panel/api-tokens/current"); res.status != 404 {
		t.Errorf("current in a browser: %d %s", res.status, res.body)
	}
}
