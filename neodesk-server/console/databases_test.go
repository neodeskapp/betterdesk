package console

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// What the wiring writes to the database, on SQLite and on PostgreSQL:
// sessions, the audit log, the panel access lookups of step 10 and the
// language choice of an account.
func TestPipelineOnBothDatabases(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, setup{db: db, trustProxy: "loopback", langDir: copyLang(t, "en", "pl", "de")})
		w.s.Router().Get("/api/probe", probe)
		w.s.Router().Get("/api/users/probe", probe)
		ctx := context.Background()

		viewer := w.agent()
		viewer.always = []string{"X-Forwarded-For", "203.0.113.5"}
		viewer.signIn("viewer")
		if res := viewer.get("/api/probe"); res.json(t)["user"] != float64(roleIDs["viewer"]) {
			t.Fatalf("the session was not read back: %s", res.body)
		}
		// Step 10 reads the account's policy: a viewer has no access to users
		if res := viewer.get("/api/users/probe"); res.status != 403 || !strings.Contains(res.body, "panel_access_denied") {
			t.Errorf("viewer on a users path: %d %s", res.status, res.body)
		}
		// Step 7 writes the audit log
		viewer.always = []string{"X-Forwarded-For", "203.0.113.6", "User-Agent", "probe/1"}
		viewer.get("/api/probe")
		id := strconv.FormatInt(roleIDs["viewer"], 10)
		if rows := w.auditRows(); len(rows) != 1 || rows[0] != "session_ip_change|IP change during session: loginIp=203.0.113.5 currentIp=203.0.113.6 ua=probe/1|203.0.113.6|"+id {
			t.Errorf("audit log = %q", rows)
		}

		// The i18n group writes the users row and the session
		if res := viewer.post("/api/i18n/set/pl", "{}"); res.status != 200 {
			t.Fatalf("set the language: %d %s", res.status, res.body)
		}
		var lang sql.NullString
		if err := db.QueryRow(ctx, "SELECT preferred_language FROM users WHERE id = ?", roleIDs["viewer"]).Scan(&lang); err != nil || lang.String != "pl" {
			t.Errorf("users.preferred_language = %q (%v), want pl", lang.String, err)
		}
		delete(viewer.cookies, "betterdesk_lang")
		if res := viewer.get("/api/probe", "Accept-Language", "de"); res.json(t)["lang"] != "pl" {
			t.Errorf("the stored session does not carry the choice: %s", res.body)
		}

		// A route that writes the audit log through Deps.LogAction
		admin := w.agent().signIn("server_admin")
		file := `{"_meta":{"code":"fr","name":"French","native":"Français","flag":"fr"},"nav":{"devices":"Appareils"}}`
		body, contentType := upload(t, "fr.json", "application/json", file)
		if res := admin.do("POST", "/api/i18n/upload", body, "Content-Type", contentType, "X-CSRF-Token", admin.csrf()); res.status != 200 {
			t.Fatalf("upload: %d %s", res.status, res.body)
		}
		rows := w.auditRows()
		if want := "language_uploaded|Language fr uploaded|127.0.0.1|" + strconv.FormatInt(roleIDs["server_admin"], 10); len(rows) != 2 || rows[1] != want {
			t.Errorf("audit log after the upload = %q, want a second row %q", rows, want)
		}
	})
}
