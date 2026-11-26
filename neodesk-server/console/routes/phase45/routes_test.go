package phase45_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// Pairing a generic agent: one that waits for approval is approved under
// its name, a known one is renamed, and the device goes into the folder.
// A failure of the API server is answered with the API server's own
// answer.
func TestActivate(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI().
			On("GET /api/enrollment/pending", 200, `{"devices":[{"device_id":"agent-1"},{"device_id":7}],"count":2}`).
			On("POST /api/enrollment/approve/agent-1", 200, `{"approved":true}`).
			On("PATCH /api/peers/agent-2", 200, `{"id":"agent-2"}`).
			On("PATCH /api/peers/agent-3", 404, `{"error":"peer not found"}`)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		ctx := context.Background()
		folder, err := db.Insert(ctx, "INSERT INTO folders (name) VALUES ('Front desk')")
		if err != nil {
			t.Fatal(err)
		}
		admin := w.As("admin")
		activate := func(body string) routetest.Answer { return admin.Do("POST", "/api/agent-pairing/activate", body) }

		res := activate(`{"device_id":" agent-1 ","display_name":" Desk ","folder_id":"` + itoa(folder) + `x"}`)
		if res.Status != http.StatusOK || res.Body != `{"success":true,"device_id":"agent-1"}` {
			t.Errorf("waiting agent: %d %s", res.Status, res.Body)
		}
		want := []string{"GET /api/enrollment/pending", `POST /api/enrollment/approve/agent-1 {"display_name":"Desk","sync_mode":"standard","tags":""}`}
		if calls := api.Calls(); !reflect.DeepEqual(calls, want) {
			t.Errorf("calls %q, want %q", calls, want)
		}
		var assigned int64
		if err := db.QueryRow(ctx, "SELECT folder_id FROM device_folder_assignments WHERE device_id = 'agent-1'").Scan(&assigned); err != nil || assigned != folder {
			t.Errorf("folder of agent-1: %d %v", assigned, err)
		}

		res = activate(`{"device_id":"agent-2","display_name":12}`)
		want = []string{"GET /api/enrollment/pending", `PATCH /api/peers/agent-2 {"display_name":"12","note":"12"}`}
		if calls := api.Calls(); res.Status != http.StatusOK || !reflect.DeepEqual(calls, want) {
			t.Errorf("known agent: %d %s, calls %q", res.Status, res.Body, calls)
		}
		if res := activate(`{"device_id":"agent-3"}`); res.Status != http.StatusBadGateway || res.Body != `{"success":false,"error":"peer not found"}` {
			t.Errorf("unknown agent: %d %s", res.Status, res.Body)
		}
		api.Calls()
		for body, want := range map[string]string{
			`{}`:                    `{"success":false,"error":"invalid_device_id"}`,
			`{"device_id":"ab"}`:    `{"success":false,"error":"invalid_device_id"}`,
			`{"device_id":"a b c"}`: `{"success":false,"error":"invalid_device_id"}`,
		} {
			if res := activate(body); res.Status != http.StatusBadRequest || res.Body != want {
				t.Errorf("%s: %d %s", body, res.Status, res.Body)
			}
		}
		if calls := api.Calls(); len(calls) != 0 {
			t.Errorf("a refused device id reached the API server: %q", calls)
		}
		// A device id that cannot be made text: Node never answered
		if res := activate(`{"device_id":{"toString":1}}`); res.Status != http.StatusInternalServerError {
			t.Errorf("device id that cannot be text: %d %s", res.Status, res.Body)
		}
		if res := w.As("server_admin").Do("POST", "/api/agent-pairing/activate", `{"device_id":"agent-2"}`); res.Status != http.StatusForbidden {
			t.Errorf("server_admin: %d %s", res.Status, res.Body)
		}
		audit := strings.Join(w.AuditRows(), "\n")
		if !strings.Contains(audit, "generic_agent_activated|Activated generic Support Agent agent-1|127.0.0.1|1") ||
			!strings.Contains(audit, "generic_agent_activated|Activated generic Support Agent agent-2|") || strings.Contains(audit, "agent-3") {
			t.Errorf("audit log:\n%s", audit)
		}
	})
}

// RV97: an account that holds enrollment.approve through an override but
// has a limited device scope renames only a device it may see, and puts a
// device only into a folder that is open to it. An agent that waits for
// approval is in nobody's scope yet.
func TestActivateKeepsToTheDeviceScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI().
			On("GET /api/enrollment/pending", 200, `{"devices":[{"device_id":"agent-new"}],"count":1}`).
			On("POST /api/enrollment/approve/agent-new", 200, `{"approved":true}`).
			On("GET /api/peers/agent-closed", 200, `{"id":"agent-closed"}`).
			On("GET /api/peers/agent-open", 200, `{"id":"agent-open"}`).
			On("PATCH /api/peers/agent-closed", 200, `{"id":"agent-closed"}`).
			On("PATCH /api/peers/agent-open", 200, `{"id":"agent-open"}`)
		langs := func(cfg *config.Config) { cfg.LangDir = filepath.Join("..", "..", "static", "files", "lang") }
		w := routetest.New(t, routetest.Options{DB: db, API: api, Config: langs,
			Roles: routetest.Overrides{"operator": {"enrollment.approve": true}}})
		admin, operator := w.As("admin"), w.As("operator")
		for _, folder := range []string{`{"name":"Closed","allowed_users":["e2e_admin"]}`, `{"name":"Mine","allowed_users":["e2e_operator"]}`} {
			if res := admin.Do("POST", "/api/folders", folder); res.Status != http.StatusOK {
				t.Fatalf("folder %s: %d %s", folder, res.Status, res.Body)
			}
		}
		if res := admin.Do("POST", "/api/folders/1/devices", `{"deviceIds":["agent-closed"]}`); res.Status != http.StatusOK {
			t.Fatalf("assign: %d %s", res.Status, res.Body)
		}
		api.Calls()
		activate := func(body string) routetest.Answer { return operator.Do("POST", "/api/agent-pairing/activate", body) }
		noDevice := `{"success":false,"error":"You don't have access to this device."}`
		noFolder := `{"success":false,"error":"You don't have permission to do this. Ask an administrator."}`

		if res := activate(`{"device_id":"agent-closed","display_name":"Mine now"}`); res.Status != http.StatusForbidden || res.Body != noDevice {
			t.Errorf("rename a device out of scope: %d %s", res.Status, res.Body)
		}
		if res := activate(`{"device_id":"agent-open","display_name":"Desk","folder_id":1}`); res.Status != http.StatusForbidden || res.Body != noFolder {
			t.Errorf("move into a closed folder: %d %s", res.Status, res.Body)
		}
		if res := activate(`{"device_id":"agent-new","display_name":"Desk","folder_id":"1"}`); res.Status != http.StatusForbidden || res.Body != noFolder {
			t.Errorf("approve into a closed folder: %d %s", res.Status, res.Body)
		}
		for _, call := range api.Calls() {
			if strings.HasPrefix(call, "PATCH") || strings.HasPrefix(call, "POST") {
				t.Errorf("a refused activation reached the API server: %s", call)
			}
		}
		if got := rowsOf(t, db, "SELECT device_id FROM device_folder_assignments ORDER BY device_id"); got != "agent-closed" {
			t.Errorf("folder assignments after the refusals: %q", got)
		}

		if res := activate(`{"device_id":"agent-new","display_name":"Desk","folder_id":2}`); res.Status != http.StatusOK {
			t.Errorf("approve into its own folder: %d %s", res.Status, res.Body)
		}
		if res := activate(`{"device_id":"agent-open","display_name":"Desk"}`); res.Status != http.StatusOK {
			t.Errorf("rename a device in scope: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("POST", "/api/agent-pairing/activate", `{"device_id":"agent-closed","folder_id":1}`); res.Status != http.StatusOK {
			t.Errorf("the admin: %d %s", res.Status, res.Body)
		}
		if got := rowsOf(t, db, "SELECT device_id || ':' || folder_id FROM device_folder_assignments ORDER BY device_id"); got != "agent-closed:1,agent-new:2" {
			t.Errorf("folder assignments: %q", got)
		}
	})
}

// rowsOf reads one text column per row, joined with commas.
func rowsOf(t *testing.T, db *store.DB, query string) string {
	t.Helper()
	res, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer res.Close()
	var out []string
	for res.Next() {
		var s string
		if err := res.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

func itoa(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A file of a cached release goes out as an attachment, with what a
// static file has: its type, validators and ranges.
func TestDownload(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"FILE_ACCESS_RATE_LIMIT_MAX": "5"}})
		// The installer list counts against the file limit too (upstream
		// 3.5.264, CodeQL 365)
		if res := w.SignedOut().Do("GET", "/api/portal/installers", ""); res.Body != `{"success":true,"release":null,"reason":"no_release_imported","installers":[]}` ||
			res.Header.Get("RateLimit-Limit") != "5" || res.Header.Get("RateLimit-Remaining") != "4" {
			t.Errorf("installers without a cache: %s, limit %q, remaining %q", res.Body, res.Header.Get("RateLimit-Limit"), res.Header.Get("RateLimit-Remaining"))
		}
		dir := filepath.Join(w.DataDir, "modules", "neodesk-support-releases", "support-v1.0.0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "agent.zip"), []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		visitor := w.SignedOut()
		res := visitor.Do("GET", "/downloads/generic-agent/support-v1.0.0/agent.zip", "")
		if res.Status != http.StatusOK || res.Body != "0123456789" ||
			res.Header.Get("Content-Disposition") != `attachment; filename="agent.zip"` ||
			res.Header.Get("Content-Type") != "application/zip" || res.Header.Get("Accept-Ranges") != "bytes" || res.Header.Get("ETag") == "" {
			t.Fatalf("download: %d %v %q", res.Status, res.Header, res.Body)
		}
		if part := visitor.Do("GET", "/downloads/generic-agent/support-v1.0.0/agent.zip", "", "Range", "bytes=2-4"); part.Status != http.StatusPartialContent || part.Body != "234" {
			t.Errorf("range: %d %q", part.Status, part.Body)
		}
		if again := visitor.Do("GET", "/downloads/generic-agent/support-v1.0.0/agent.zip", "", "If-None-Match", res.Header.Get("ETag")); again.Status != http.StatusNotModified {
			t.Errorf("with its ETag: %d", again.Status)
		}
		missing := visitor.Do("GET", "/downloads/generic-agent/support-v1.0.0/other.zip", "")
		if missing.Status != http.StatusNotFound || missing.Body != "Agent release asset not found" || missing.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("missing: %d %v %q", missing.Status, missing.Header, missing.Body)
		}
		// Downloads and the installer list share the file limit: five a
		// minute here
		if limited := visitor.Do("GET", "/downloads/generic-agent/support-v1.0.0/agent.zip", ""); limited.Status != http.StatusTooManyRequests {
			t.Errorf("sixth file request in a minute: %d %s", limited.Status, limited.Body)
		}
		installers := visitor.Do("GET", "/api/portal/installers", "")
		if installers.Status != http.StatusTooManyRequests || installers.Body != `{"success":false,"error":"Too many file requests. Please try again later."}` {
			t.Errorf("installers over the file limit: %d %s", installers.Status, installers.Body)
		}
	})
}

// A release cache without a manifest is reported as incomplete.
func TestInstallersOfAnIncompleteCache(t *testing.T) {
	w := routetest.New(t, routetest.Options{DB: storetest.Migrated(t)})
	dir := filepath.Join(w.DataDir, "modules", "neodesk-support-releases", "support-v1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.zip"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	installers := w.SignedOut().Do("GET", "/api/portal/installers", "")
	if installers.Status != http.StatusOK || installers.Body != `{"success":true,"release":null,"reason":"release_cache_incomplete","installers":[]}` {
		t.Errorf("installers without a manifest: %d %s", installers.Status, installers.Body)
	}
}

// A web address saved as the profile picture is fetched and stored by the
// console, never linked; one the console may not fetch from is reported,
// and the save still succeeds. Every failed fetch, a private address
// included, is reported with one code (RV10).
func TestProfilePicture(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := routetest.New(t, routetest.Options{DB: db}).As("admin")
		save := func(body string) string { return admin.Do("PUT", "/api/users/me/profile", body).Body }
		if got := save(`{"first_name":"Alice","avatar_url":"http://127.0.0.1/x.png"}`); got != `{"success":true,"avatar":"failed","avatar_error":"fetch_failed"}` {
			t.Errorf("loopback address: %s", got)
		}
		if got := save(`{"first_name":"Alice","avatar_url":"http://192.168.0.10/x.png"}`); got != `{"success":true,"avatar":"failed","avatar_error":"fetch_failed"}` {
			t.Errorf("private address: %s", got)
		}
		if got := save(`{"first_name":"Alice","avatar_url":""}`); got != `{"success":true,"avatar":"skipped"}` {
			t.Errorf("emptied address without a picture: %s", got)
		}
		if got := save(`{"first_name":"Alice"}`); got != `{"success":true,"avatar":"unchanged"}` {
			t.Errorf("without an address: %s", got)
		}
	})
}

// A save changes the fields the body has and keeps the others: saving the
// phone keeps the names, a field sent as null is emptied, and a body with
// none of the fields, {} or a list, changes nothing (BUGS C3). This
// console has no email settings, so another address is refused (A272)
// and the address stays; the fields beside it are saved.
func TestSaveProfileKeepsFieldsTheBodyLeavesOut(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if _, err := db.Exec(context.Background(), `UPDATE users SET email = 'grace@example.test' WHERE id = ?`, routetest.RoleIDs["admin"]); err != nil {
			t.Fatal(err)
		}
		admin := w.As("admin")
		profile := func() string { return admin.Do("GET", "/api/users/me/profile", "").Body }
		full := `{"first_name":"Grace","last_name":"Hopper","email":"grace@example.test","phone":"+1 555","role_display":"Rear admiral"}`
		if res := admin.Do("PUT", "/api/users/me/profile", full); res.Status != http.StatusOK {
			t.Fatalf("save the whole profile: %d %s", res.Status, res.Body)
		}
		const refused = `{"success":false,"error":"users.email_change_needs_mail","code":"email_change_needs_mail"}`
		for _, c := range []struct{ body, answer, want string }{
			{`{"phone":"555"}`, "", `"first_name":"Grace","last_name":"Hopper","email":"grace@example.test","phone":"555","role_display":"Rear admiral"`},
			{`{"last_name":null,"role_display":""}`, "", `"first_name":"Grace","last_name":"","email":"grace@example.test","phone":"555","role_display":""`},
			{`{}`, "", `"first_name":"Grace","last_name":"","email":"grace@example.test","phone":"555","role_display":""`},
			{`[{"first_name":"x"}]`, "", `"first_name":"Grace","last_name":"","email":"grace@example.test","phone":"555","role_display":""`},
			{`{"email":5,"other":"x","phone":"556"}`, refused, `"first_name":"Grace","last_name":"","email":"grace@example.test","phone":"556","role_display":""`},
		} {
			res := admin.Do("PUT", "/api/users/me/profile", c.body)
			if c.answer == "" && (res.Status != http.StatusOK || res.Body != `{"success":true,"avatar":"unchanged"}`) {
				t.Errorf("save %s: %d %s", c.body, res.Status, res.Body)
			}
			if c.answer != "" && (res.Status != http.StatusConflict || res.Body != c.answer) {
				t.Errorf("save %s: %d %s, want 409 %s", c.body, res.Status, res.Body, c.answer)
			}
			if got := profile(); !strings.Contains(got, c.want) {
				t.Errorf("after saving %s: %s, want %s", c.body, got, c.want)
			}
		}
	})
}

// Without the template table (a database the console has not migrated
// yet), enrollment finds no token, as Node's getAgentTemplateByToken does,
// and the list answers 500 with the database's message. A new template
// gets the generic 500, without the database's text (UM3).
func TestTemplatesWithoutTheirTable(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if _, err := db.Exec(context.Background(), "DROP TABLE agent_templates"); err != nil {
			t.Fatal(err)
		}
		if res := w.SignedOut().Do("POST", "/api/nd/enroll", `{"enrollment_token":"tok","device_id":"dev"}`); res.Status != http.StatusUnauthorized || res.Body != `{"success":false,"error":"invalid_token"}` {
			t.Errorf("enroll: %d %s", res.Status, res.Body)
		}
		res := w.As("admin").Do("GET", "/api/agent-templates", "")
		if res.Status != http.StatusInternalServerError || !strings.HasPrefix(res.Body, `{"success":false,"error":"`) || !strings.HasSuffix(res.Body, `","templates":[]}`) || !strings.Contains(res.Body, "agent_templates") {
			t.Errorf("list: %d %s", res.Status, res.Body)
		}
		res = w.As("admin").Do("POST", "/api/agent-templates", `{"name":"Front desk"}`)
		if res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("create: %d %s", res.Status, res.Body)
		}
	})
}

// A name that is taken is name_exists on both databases (UM3).
func TestTemplateNameTaken(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := routetest.New(t, routetest.Options{DB: db}).As("admin")
		if res := admin.Do("POST", "/api/agent-templates", `{"name":"Front desk"}`); res.Status != http.StatusCreated {
			t.Fatalf("first: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("POST", "/api/agent-templates", `{"name":"Front desk","description":"again"}`); res.Status != http.StatusConflict || res.Body != `{"success":false,"error":"name_exists"}` {
			t.Errorf("taken: %d %s", res.Status, res.Body)
		}
	})
}
