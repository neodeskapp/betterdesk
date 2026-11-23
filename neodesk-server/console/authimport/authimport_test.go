package authimport_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authimport"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// splitInstall builds the old console's split layout from the SQL text
// recorded from the real Node console (testdata/split), then lets
// the signal server create its tables in the main file and puts accounts
// and rows there as the server and the user sync left them, and runs the
// console's migration. It returns the store and the two paths.
func splitInstall(t *testing.T, goSide func(exec func(string, ...any))) (*store.DB, string, string) {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "db_v2.sqlite3")
	authPath := filepath.Join(dir, "auth.db")
	for _, c := range []struct{ path, dump string }{{mainPath, "main.sql"}, {authPath, "auth.sql"}} {
		text, err := os.ReadFile(filepath.Join("testdata", "split", c.dump))
		if err != nil {
			t.Fatal(err)
		}
		handle, err := sql.Open("sqlite", "file:"+c.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := handle.Exec(string(text)); err != nil {
			t.Fatalf("%s: %v", c.dump, err)
		}
		if _, err := handle.Exec(`PRAGMA journal_mode = WAL`); err != nil {
			t.Fatal(err)
		}
		_ = handle.Close()
	}
	sdb, err := store.OpenSQLite(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	storetest.GoServerMigrate(t, sdb)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := sdb.Exec(context.Background(), query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if goSide != nil {
		goSide(exec)
	}
	if err := sdb.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return sdb, mainPath, authPath
}

// serverSide is what the signal server holds in a split install: the
// admin, two accounts the user sync copied (with placeholder passwords,
// one with its name in another case), two of its own, an address book
// and a strategy assignment the console also has, and a metric whose id
// the console's metrics use too.
func serverSide(exec func(string, ...any)) {
	exec(`INSERT INTO users (id, username, password_hash, role, guid) VALUES
		(1, 'admin', 'pbkdf2-sha256$600000$go$admin', 'super_admin', ''),
		(2, 'bob', 'pbkdf2-sha256$600000$go$placeholder', 'viewer', 'guid-bob-go'),
		(3, 'dave', 'pbkdf2-sha256$600000$go$dave', 'operator', 'guid-dave-go'),
		(4, 'zed', 'pbkdf2-sha256$600000$go$zed', 'viewer', ''),
		(6, 'frank', 'pbkdf2-sha256$600000$go$frank', 'viewer', '')`)
	exec(`INSERT INTO address_books (username, ab_type, data) VALUES ('bob', 'personal', '{"go":true}')`)
	exec(`INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('device', 'PEER1', 'strat-go')`)
	exec(`INSERT INTO peer_metrics (id, peer_id, cpu_usage) VALUES (1, 'GOPEER', 99)`)
}

func query(t *testing.T, db *store.DB, q string, args ...any) string {
	t.Helper()
	rows, err := db.Query(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(values))
		for i, v := range values {
			switch x := v.(type) {
			case nil:
				parts[i] = "NULL"
			case []byte:
				parts[i] = string(x)
			default:
				parts[i] = fmt.Sprint(x)
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return strings.Join(out, "; ")
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", raw)
}

func options(authPath string) authimport.Options {
	return authimport.Options{
		AuthDBPath: authPath,
		Now:        func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) },
		Logf:       func(string, ...any) {},
	}
}

// TestImportMergesASplitInstall imports a split install and checks every
// rule: accounts matched by name keep the server's row and id with the
// console's role, profile, two-factor settings and password; accounts only in
// auth.db keep their id unless it is taken; rows follow the new ids;
// rows of a deleted account are left out; groups replace the migration's
// "Default" seed; what both files have keeps the server's row; sessions
// of accounts whose id changed end; the marker, the report and the
// snapshots are written and auth.db is not touched.
func TestImportMergesASplitInstall(t *testing.T) {
	sdb, mainPath, authPath := splitInstall(t, serverSide)
	before := fileHash(t, authPath)
	ctx := context.Background()
	report, err := authimport.Run(ctx, sdb, options(authPath))
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("nothing imported")
	}
	if fileHash(t, authPath) != before {
		t.Error("auth.db changed")
	}
	wantIDs := map[int64]int64{1: 1, 2: 7, 3: 2, 5: 3, 6: 8}
	if fmt.Sprint(report.UserIDs) != fmt.Sprint(wantIDs) {
		t.Errorf("account ids %v, want %v", report.UserIDs, wantIDs)
	}
	if report.UsersMerged != 3 || report.UsersAdded != 2 || report.PasswordsTaken != 3 || report.SessionsEnded != 2 {
		t.Errorf("report %+v", report)
	}

	checks := []struct{ name, q, want string }{
		{"accounts", `SELECT id, username, role, password_hash, totp_enabled, COALESCE(totp_secret, 'NULL'), COALESCE(email, ''), COALESCE(preferred_language, 'NULL'), guid, created_at, last_login FROM users ORDER BY id`,
			"1|admin|admin|$2b$10$adminhashfromtheconsole.........................|0|NULL||NULL|guid-admin-console|2024-01-01 00:00:00|2026-11-01 10:00:00; " +
				"2|bob|viewer|$2b$10$bobhash.........................................|0|NULL|bob@example.com|en|guid-bob-go|2024-03-01 00:00:00|; " +
				"3|dave|admin|$2b$10$davehash........................................|0|NULL||NULL|guid-dave-go|2024-04-01 00:00:00|2026-10-01 00:00:00; " +
				"4|zed|viewer|pbkdf2-sha256$600000$go$zed|0|||NULL||%s|; " +
				"6|frank|viewer|pbkdf2-sha256$600000$go$frank|0|||NULL||%s|; " +
				"7|alice|operator|$2b$10$alicehash.......................................|1|JBSWY3DPEHPK3PXP|alice@example.com|de|guid-alice-console|2024-02-01 00:00:00|2026-11-02 10:00:00; " +
				"8|eve|viewer|$2b$10$evehash.........................................|0|NULL|eve@example.com|pl||2024-05-01 00:00:00|NULL"},
		{"user groups", `SELECT id, guid, name, team_id FROM user_groups ORDER BY id`, "1|ug-default|Default|; 2|ug-ops|ops|team-1"},
		{"device groups", `SELECT id, guid, name, source_type FROM device_groups ORDER BY id`, "1|dg-default|Default|manual; 2|dg-servers|servers|tag"},
		{"members", `SELECT user_group_id, user_id FROM user_group_members ORDER BY 1, 2`, "1|3; 2|2; 2|7"},
		{"device access", `SELECT device_group_id, user_id FROM device_group_user_access ORDER BY 1, 2`, "2|7; 2|8"},
		{"group access", `SELECT device_group_id, user_group_id FROM device_group_user_group_access`, "2|2"},
		{"device members", `SELECT device_group_id, peer_id FROM device_group_members ORDER BY 1, 2`, "1|PEER2; 2|PEER1"},
		{"folders", `SELECT f.name, a.device_id FROM device_folder_assignments a JOIN folders f ON f.id = a.folder_id ORDER BY 2`, "Office|PEER1; Lab|PEER2"},
		{"strategies", `SELECT guid, user_group_guid, device_group_guid, permissions FROM strategies`, `strat-1|ug-ops|dg-servers|{"x":1}`},
		{"assignments", `SELECT target_type, target_key, strategy_guid FROM strategy_assignments ORDER BY 1, 2`, "device|PEER1|strat-go; user|guid-alice-console|strat-1; user|guid-dave-go|strat-1"},
		{"address books", `SELECT username, ab_type, data FROM address_books ORDER BY 1, 2`, `alice|legacy|{"peers":[{"id":"PEER1"}]}; bob|personal|{"go":true}`},
		{"panel access", `SELECT subject_type, subject_id, resource_key, access_level FROM panel_access_policies ORDER BY 1, 2, 3`,
			"group|ug-ops|devices|write; user|7|devices|read; user|x2|tickets|read"},
		{"client features", `SELECT subject_id, feature_key, enabled FROM client_feature_policies ORDER BY 1`, "2|file_transfer|1; 8|chat|0"},
		{"settings", `SELECT key, value FROM settings WHERE key LIKE 'desktop%' OR key = 'smtp_host' ORDER BY key`,
			`desktop_layout_7|{"icons":[1]}; desktop_layout_default|keep; smtp_host|mail.example`},
		{"audit log", `SELECT COALESCE(user_id, 'NULL'), action FROM audit_log ORDER BY id`, "7|login; NULL|login; NULL|boot; 8|logout"},
		{"grants", `SELECT user_id, peer_id FROM user_peer_grants ORDER BY 1`, "3|PEER1; 8|PEER2"},
		{"reads", `SELECT user_id, notification_id FROM notification_reads ORDER BY 1`, "2|n-1; 7|n-2"},
		{"avatars", `SELECT user_id, version FROM user_avatars`, "7|v1"},
		{"tokens", `SELECT user_id, token FROM access_tokens`, "7|tok-alice"},
		{"tickets", `SELECT t.title, COUNT(c.id) FROM tickets t LEFT JOIN ticket_comments c ON c.ticket_id = t.id GROUP BY t.id ORDER BY t.id`, "Printer|1; VPN|1"},
		{"attachments", `SELECT ticket_id, filename FROM ticket_attachments`, "1|log.txt"},
		{"alerts", `SELECT r.name, h.message FROM alert_history h JOIN alert_rules r ON r.id = h.rule_id`, "offline|gone"},
		{"metrics", `SELECT id, peer_id, cpu_usage FROM peer_metrics ORDER BY id`, "1|GOPEER|99; 2|PEER1|10; 3|PEER1|20"},
		{"client audit", `SELECT (SELECT COUNT(*) FROM audit_connections), (SELECT COUNT(*) FROM audit_files), (SELECT COUNT(*) FROM audit_alarms)`, "1|1|1"},
		{"tenants", `SELECT tenant_id, user_id FROM tenant_users ORDER BY 2`, "1|2; 1|7"},
		{"bundles", `SELECT bundle_id, COALESCE(created_by, 'NULL') FROM agent_bundles ORDER BY id`, "b-1|7; b-2|NULL"},
		{"sessions", `SELECT sid, is_active, COALESCE(end_reason, 'NULL'), COALESCE(ended_at, 'NULL') FROM panel_sessions ORDER BY sid`,
			"sid-admin|1|NULL|NULL; sid-alice|0|revoked|%[1]d; sid-anon|1|NULL|NULL; sid-carol|0|revoked|%[1]d"},
		{"others", `SELECT (SELECT COUNT(*) FROM login_attempts), (SELECT COUNT(*) FROM account_lockouts), (SELECT value FROM branding_config WHERE key = 'appName'),
			(SELECT COUNT(*) FROM branding_profiles), (SELECT COUNT(*) FROM email_notification_log), (SELECT hostname FROM peer_sysinfo), (SELECT COUNT(*) FROM remote_commands)`,
			"1|1|ACME Desk|1|1|pc-1|1"},
		{"marker", `SELECT name, status, source_sha256 <> '' FROM neodesk_migrations`, authimport.MarkerName + "|complete|1"},
	}
	created := query(t, sdb, `SELECT created_at FROM users WHERE id = 4`)
	for _, c := range checks {
		want := c.want
		switch c.name {
		case "accounts":
			want = fmt.Sprintf(want, created, query(t, sdb, `SELECT created_at FROM users WHERE id = 6`))
		case "sessions":
			want = fmt.Sprintf(want, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC).UnixMilli())
		}
		if got := query(t, sdb, c.q); got != want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, want)
		}
	}
	if got := query(t, sdb, `SELECT COUNT(*) FROM pragma_foreign_key_check`); got != "0" {
		t.Errorf("%s rows point at nothing", got)
	}
	for _, path := range []string{report.TargetBackup, report.SourceBackup} {
		if filepath.Dir(path) != filepath.Join(filepath.Dir(mainPath), "backups") {
			t.Errorf("snapshot at %s", path)
		}
		snap, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		var users int
		if err := snap.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
			t.Errorf("snapshot %s: %v", path, err)
		}
		_ = snap.Close()
		if path == report.TargetBackup && users != 5 || path == report.SourceBackup && users != 5 {
			t.Errorf("snapshot %s has %d accounts", path, users)
		}
	}

	// The names of the server's -migrate-sqlite-auth, which its rollback
	// and the docs know
	if filepath.Base(report.TargetBackup) != "db_v2.sqlite3.pre-auth-consolidation-20261118T120000Z" ||
		filepath.Base(report.SourceBackup) != "auth.db.legacy-auth-20261118T120000Z" {
		t.Errorf("snapshots named %s and %s", report.TargetBackup, report.SourceBackup)
	}

	again, err := authimport.Run(ctx, sdb, options(authPath))
	if err != nil || again != nil {
		t.Errorf("a second run did something: %+v, %v", again, err)
	}
}

// TestImportIsAllOrNothing makes a row of auth.db collide with one of the
// main file in the middle of the import: nothing may be left of the
// import, no marker either, and a run after the clash is removed works.
func TestImportIsAllOrNothing(t *testing.T) {
	sdb, mainPath, authPath := splitInstall(t, func(exec func(string, ...any)) {
		serverSide(exec)
	})
	ctx := context.Background()
	if _, err := sdb.Exec(ctx, `INSERT INTO access_tokens (token, user_id, expires_at) VALUES ('tok-alice', 1, '2030-01-01')`); err != nil {
		t.Fatal(err)
	}
	beforeUsers := query(t, sdb, `SELECT * FROM users ORDER BY id`)
	beforeGroups := query(t, sdb, `SELECT * FROM user_groups ORDER BY id`)
	if _, err := authimport.Run(ctx, sdb, options(authPath)); err == nil || !strings.Contains(err.Error(), "access_tokens") {
		t.Fatalf("import went through a clash in access_tokens: %v", err)
	}
	if query(t, sdb, `SELECT * FROM users ORDER BY id`) != beforeUsers || query(t, sdb, `SELECT * FROM user_groups ORDER BY id`) != beforeGroups {
		t.Error("a failed import left changes")
	}
	if got := query(t, sdb, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'neodesk_migrations'`); got != "0" {
		t.Error("a failed import left the marker table")
	}
	if _, err := sdb.Exec(ctx, `DELETE FROM access_tokens`); err != nil {
		t.Fatal(err)
	}

	// A row of the main file that points at nothing stops the import too
	raw, err := sql.Open("sqlite", "file:"+mainPath+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO ticket_comments (ticket_id, author, body) VALUES (999, 'x', 'orphan')`); err != nil {
		t.Fatal(err)
	}
	orphanRun := options(authPath)
	orphanRun.Now = func() time.Time { return time.Date(2026, 11, 18, 12, 30, 0, 0, time.UTC) }
	if _, err := authimport.Run(ctx, sdb, orphanRun); err == nil || !strings.Contains(err.Error(), "ticket_comments point at missing rows of tickets") {
		t.Errorf("import went through rows that point at nothing: %v", err)
	}
	if _, err := raw.Exec(`DELETE FROM ticket_comments`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	opts := options(authPath)
	opts.Now = func() time.Time { return time.Date(2026, 11, 18, 13, 0, 0, 0, time.UTC) }
	if report, err := authimport.Run(ctx, sdb, opts); err != nil || report == nil {
		t.Fatalf("second try: %v", err)
	}
}

// TestImportOnlyWhenNeeded leaves the database alone without auth.db, in
// the mode "consolidated", on PostgreSQL and when the marker is there.
func TestImportOnlyWhenNeeded(t *testing.T) {
	ctx := context.Background()
	sdb, _, authPath := splitInstall(t, nil)
	for _, opts := range []authimport.Options{
		{AuthDBPath: filepath.Join(t.TempDir(), "auth.db")},
		{AuthDBPath: authPath, Mode: "Consolidated"},
		{},
	} {
		if report, err := authimport.Run(ctx, sdb, opts); report != nil || err != nil {
			t.Errorf("%+v: imported (%v)", opts, err)
		}
	}
	if got := query(t, sdb, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'neodesk_migrations'`); got != "0" {
		t.Error("marker written without an import")
	}
	if _, err := sdb.Exec(ctx, `CREATE TABLE neodesk_migrations (name TEXT PRIMARY KEY, status TEXT NOT NULL, source_sha256 TEXT NOT NULL DEFAULT '', completed_at TEXT NOT NULL DEFAULT (datetime('now')), details TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.Exec(ctx, `INSERT INTO neodesk_migrations (name, status) VALUES (?, 'complete')`, authimport.MarkerName); err != nil {
		t.Fatal(err)
	}
	if report, err := authimport.Run(ctx, sdb, options(authPath)); report != nil || err != nil {
		t.Errorf("imported with the marker in place (%v)", err)
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		if !db.IsPostgres() {
			return
		}
		if report, err := authimport.Run(ctx, db, options(authPath)); report != nil || err != nil {
			t.Errorf("imported into PostgreSQL (%v)", err)
		}
	})
	if got := authimport.FromEnv(func(string) string { return "" }, "/data").AuthDBPath; got != filepath.Join("/data", "auth.db") {
		t.Errorf("default path %s", got)
	}
	env := map[string]string{"AUTH_DB_PATH": "/x/a.db", "SQLITE_AUTH_DB_MODE": "legacy"}
	if got := authimport.FromEnv(func(k string) string { return env[k] }, "/data"); got.AuthDBPath != "/x/a.db" || got.Mode != "legacy" {
		t.Errorf("FromEnv = %+v", got)
	}
}

// changeAuthDB runs statements on auth.db before the import.
func changeAuthDB(t *testing.T, authPath string, statements ...string) {
	t.Helper()
	handle, err := sql.Open("sqlite", "file:"+authPath)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	for _, s := range statements {
		if _, err := handle.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// TestAnAccountInBothFilesTakesTheConsolesRoleAndProvider: the panel
// signed people in with the role and provider in auth.db, so an account in
// both files takes them from there, whichever way they differ: the
// server's super_admin "admin" is the console's admin, "dave" whom the
// console made an admin is one, "bob" whom the console demoted from admin
// to viewer stays a viewer and signs in through LDAP as the console said
// (BUGS RV127, DECISIONS A87).
func TestAnAccountInBothFilesTakesTheConsolesRoleAndProvider(t *testing.T) {
	sdb, _, authPath := splitInstall(t, func(exec func(string, ...any)) {
		serverSide(exec)
		exec(`UPDATE users SET role = 'admin', auth_provider = 'oidc' WHERE username = 'bob'`)
	})
	changeAuthDB(t, authPath, `UPDATE users SET auth_provider = 'ldap' WHERE username = 'bob'`)
	if _, err := authimport.Run(context.Background(), sdb, options(authPath)); err != nil {
		t.Fatal(err)
	}
	want := "1|admin|admin|local; 2|bob|viewer|ldap; 3|dave|admin|local; 4|zed|viewer|local; 6|frank|viewer|local; 7|alice|operator|local; 8|eve|viewer|local"
	if got := query(t, sdb, `SELECT id, username, role, auth_provider FROM users ORDER BY id`); got != want {
		t.Errorf("accounts after the import:\n got %s\nwant %s", got, want)
	}
}

// TestNamesInAnotherCaseMatchOnlyWhenThereIsNoOtherAccountOfThatName: the
// console told "ZED" and "zed" apart (its names are case-sensitive), so
// neither is matched to the server's "zed" by its name in lower case: the
// console's "zed" is the server's "zed" by its exact name and "ZED" is
// added as an account of its own, whatever their order in auth.db. Of
// "FRANK" and "Frank" neither has the exact name of the server's "frank",
// and nothing tells which of them the server's row stands for, so both are
// added and "frank" stays as it was. "Dave", the only account of that name
// in either case on both sides, is still the server's "dave".
func TestNamesInAnotherCaseMatchOnlyWhenThereIsNoOtherAccountOfThatName(t *testing.T) {
	sdb, _, authPath := splitInstall(t, serverSide)
	changeAuthDB(t, authPath,
		`INSERT INTO users (id, username, password_hash, role) VALUES (10, 'ZED', '$2b$10$upperzed', 'operator'), (11, 'zed', '$2b$10$lowerzed', 'viewer'),
			(12, 'FRANK', '$2b$10$upperfrank', 'admin'), (13, 'Frank', '$2b$10$titlefrank', 'operator')`)
	report, err := authimport.Run(context.Background(), sdb, options(authPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := report.UserIDs[5]; got != 3 {
		t.Errorf("Dave went to account %d, want dave (3)", got)
	}
	if got := report.UserIDs[11]; got != 4 {
		t.Errorf("zed went to account %d, want the server's zed (4)", got)
	}
	want := "4|zed|viewer|$2b$10$lowerzed; 10|ZED|operator|$2b$10$upperzed"
	if got := query(t, sdb, `SELECT id, username, role, password_hash FROM users WHERE lower(username) = 'zed' ORDER BY id`); got != want {
		t.Errorf("the zed accounts after the import:\n got %s\nwant %s", got, want)
	}
	want = "6|frank|viewer|pbkdf2-sha256$600000$go$frank; 12|FRANK|admin|$2b$10$upperfrank; 13|Frank|operator|$2b$10$titlefrank"
	if got := query(t, sdb, `SELECT id, username, role, password_hash FROM users WHERE lower(username) = 'frank' ORDER BY id`); got != want {
		t.Errorf("the frank accounts after the import:\n got %s\nwant %s", got, want)
	}
}
