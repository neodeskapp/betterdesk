package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// Made-up installs for the tests of this package: a database with the
// console's and the server's tables and rows of every kind, uploads, keys
// and the .env, on SQLite and, when a server is configured, on PostgreSQL.
//
// testdata/node-archives holds two full backups the Node console made of
// such an install and what its own restore made of them.
// TestNodeArchiveFixtures restores them with Go and compares.

// install is one made-up installation.
type install struct {
	dialect  string
	db       *store.DB
	dbPath   string // SQLite file
	dsn      string // PostgreSQL database
	dataDir  string
	keysPath string
	envFile  string
	// consoleDir is CONSOLE_DIR, where the chat files and the ticket
	// attachments are kept (below data/).
	consoleDir string
}

func randomName(prefix string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// newInstall makes an empty install with the schema of both programs: a
// copy of a template that is prepared once per test run.
func newInstall(t *testing.T, dialect string) *install {
	t.Helper()
	root := t.TempDir()
	in := &install{
		dialect:    dialect,
		dataDir:    filepath.Join(root, "data"),
		keysPath:   filepath.Join(root, "keys"),
		envFile:    filepath.Join(root, "console.env"),
		consoleDir: filepath.Join(root, "console"),
	}
	for _, dir := range []string{in.dataDir, in.keysPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if dialect == "sqlite" {
		in.dbPath = filepath.Join(in.keysPath, "db_v2.sqlite3")
		tpl, err := os.ReadFile(sqliteTemplate(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(in.dbPath, tpl, 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := store.OpenSQLite(in.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		in.db = db
		if _, err := db.Inspect(context.Background()); err != nil {
			t.Fatal(err)
		}
	} else {
		in.db, in.dsn = postgresDatabase(t, postgresTemplate(t))
		if _, err := in.db.Inspect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

// The templates: one prepared database per kind for the whole run.
var templates struct {
	mu       sync.Mutex
	sqlite   string
	postgres string
}

func sqliteTemplate(t *testing.T) string {
	t.Helper()
	templates.mu.Lock()
	defer templates.mu.Unlock()
	if templates.sqlite != "" {
		return templates.sqlite
	}
	dir, err := os.MkdirTemp("", "backup-template-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "db_v2.sqlite3")
	db, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	storetest.Prepare(t, db, storetest.GoServerFirst)
	if _, err := db.Exec(context.Background(), `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	templates.sqlite = path
	return path
}

func postgresTemplate(t *testing.T) string {
	t.Helper()
	templates.mu.Lock()
	defer templates.mu.Unlock()
	if templates.postgres != "" {
		return templates.postgres
	}
	dsn := os.Getenv(storetest.PostgresEnv)
	if dsn == "" {
		t.Skipf("%s is not set", storetest.PostgresEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := randomName("bk_tpl_")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	templates.postgres = name
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenPostgres(pool)
	storetest.Prepare(t, db, storetest.GoServerFirst)
	_ = db.Close()
	pool.Close()
	return name
}

// TestMain removes the templates after the run.
func TestMain(m *testing.M) {
	code := m.Run()
	if templates.sqlite != "" {
		_ = os.RemoveAll(filepath.Dir(templates.sqlite))
	}
	if templates.postgres != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if admin, err := pgxpool.New(ctx, os.Getenv(storetest.PostgresEnv)); err == nil {
			_, _ = admin.Exec(ctx, "DROP DATABASE "+templates.postgres+" WITH (FORCE)")
			admin.Close()
		}
		cancel()
	}
	os.Exit(code)
}

// postgresDatabase makes a database of its own (the Node console works
// in the schema public only) as a copy of the template, and drops it at
// the end.
func postgresDatabase(t *testing.T, template string) (*store.DB, string) {
	t.Helper()
	dsn := os.Getenv(storetest.PostgresEnv)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := randomName("bk_")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+template); err != nil {
		admin.Close()
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenPostgres(pool)
	t.Cleanup(func() {
		_ = db.Close()
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(c, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	return db, u.String()
}

func mustExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// seed fills an install with rows of every kind and with its files.
func (in *install) seed(t *testing.T) {
	t.Helper()
	db := in.db
	pg := in.dialect == "postgres"
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, role, totp_secret, totp_enabled, totp_recovery_codes, email, created_at)
		VALUES (50, 'seed_admin', '$2b$10$abcdefghijklmnopqrstuv', 'admin', 'JBSWY3DPEHPK3PXP', ?, '["AAAA-BBBB","CCCC-DDDD"]', 'säd😀@example.org', ?)`,
		db.Pick("1", "true"), db.Pick("2026-11-10 01:05:50", "2026-11-10 01:05:50.694123+00"))
	mustExec(t, db, `INSERT INTO users (id, username, password_hash, role) VALUES (51, 'second', 'pbkdf2-sha256$600000$s$h', 'viewer')`)
	for k, v := range map[string]string{
		"scheduled_backup_smb":   `{"host":"nas","share":"s","password":"hunter2"}`,
		"backup_retention_count": "5",
		"ctl":                    "a\x01b\tc\u2028d<>&\"q\"",
		"desktop_layout_1":       `{"b":1,"2":2,"a":[1,2]}`,
	} {
		mustExec(t, db, `INSERT INTO settings (key, value) VALUES (?, ?)`, k, v)
	}
	if !pg {
		// An integer in a text column comes back from a restore as "7.0":
		// the old driver bound every number as a double.
		mustExec(t, db, `INSERT INTO settings (key, value) VALUES ('typed', 7)`)
		mustExec(t, db, `INSERT INTO settings (key, value) VALUES ('broken_utf8', CAST(x'61ff62' AS TEXT))`)
		mustExec(t, db, `INSERT INTO device_telemetry (device_id, memory_total_bytes, cpu_usage_percent) VALUES ('900100001', 9007199254740993, 0.1)`)
		mustExec(t, db, `INSERT INTO dlp_policies (name, rules, created_at) VALUES ('p1', '[{"a":1}]', '2026-11-10T01:05:50.930Z')`)
	} else {
		mustExec(t, db, `INSERT INTO device_telemetry (device_id, memory_total_bytes, cpu_usage_percent) VALUES ('900100001', 9007199254740993, 0.1)`)
		// a list in a jsonb column: the old restore sent it as an array
		// literal, which PostgreSQL refuses (BUGS.md BK8)
		mustExec(t, db, `INSERT INTO dlp_policies (name, rules, created_at) VALUES ('p1', '[{"a":1}]', '2026-11-10 01:05:50.930001+00')`)
		mustExec(t, db, `INSERT INTO saved_reports (title, report_type, filters, payload) VALUES ('r', 'x', '{"z":1,"1":[true,null,1.5e300]}', '{}')`)
	}
	mustExec(t, db, `INSERT INTO folders (name, color, icon, sort_order) VALUES ('Büro 😀', '#ffffff', 'folder', 3)`)
	mustExec(t, db, `INSERT INTO peers (id, uuid, pk, hostname) VALUES ('900100001', 'u-1', ?, 'host <a>')`, []byte{0, 1, 2, 0xff, 0xfe})
	mustExec(t, db, `INSERT INTO peer (id, uuid, pk, info) VALUES ('900100001', 'u-1', ?, ?)`, []byte{9, 8, 0}, `{"hostname":"h","2":"two","os":"linux"}`)
	mustExec(t, db, `INSERT INTO user_avatars (user_id, mime_type, data, version, source) VALUES (50, 'image/png', ?, 'v1', 'profile')`, []byte("\x89PNG\r\n\x1a\n\x00\x00"))
	mustExec(t, db, `INSERT INTO peer_sysinfo (peer_id, hostname, cpu_freq_ghz, memory_gb, displays) VALUES ('900100001', 'h', 3.3, 15.6, '[{"w":1920}]')`)
	mustExec(t, db, `INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (50, 'login', 'tab\there', '127.0.0.1')`)
	mustExec(t, db, `INSERT INTO strategies (guid, name, permissions) VALUES ('g-1', 'rule', '{"view":true}')`)
	if !pg {
		mustExec(t, db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	}

	files := map[string]string{
		filepath.Join(in.dataDir, ".session_secret"):             "session-secret-value",
		filepath.Join(in.dataDir, "uploads", "logo-0011.png"):    "\x89PNG logo",
		filepath.Join(in.dataDir, "uploads", "bg", "nested.jpg"): "nested background",
		filepath.Join(in.dataDir, "uploads", "zz-ä.txt"):         "umlaut name",
		filepath.Join(in.keysPath, "id_ed25519"):                 "private-key-bytes",
		filepath.Join(in.keysPath, "id_ed25519.pub"):             "public-key-bytes",
		filepath.Join(in.keysPath, ".api_key"):                   "api-key-bytes",
		filepath.Join(in.keysPath, "mesh_agent_server.pem"):      "-----BEGIN CERTIFICATE-----\n",
		in.envFile: "SESSION_SECRET=x\nPORT=5000\n",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeGoServer answers what testdata/goserver.json says, as the stub of
// the API server answered when the Node archives were made.
type fakeGoServer struct{ data *goapi.Object }

func loadFakeGoServer(t *testing.T) fakeGoServer {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "goserver.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := goapi.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return fakeGoServer{v.(*goapi.Object)}
}

func (f fakeGoServer) GetAllPeers(context.Context, bool) []*goapi.Object {
	var out []*goapi.Object
	for _, p := range f.data.Get("peers").([]any) {
		out = append(out, p.(*goapi.Object))
	}
	return out
}
func (f fakeGoServer) GetBlocklist(context.Context) *goapi.Object { return f.data.Object("blocklist") }
func (f fakeGoServer) GetAuditEvents(context.Context, any) *goapi.Object {
	return f.data.Object("auditEvents")
}
func (f fakeGoServer) GetHealth(context.Context) *goapi.Object { return f.data.Object("serverHealth") }

func (in *install) service(t *testing.T, appVersion string) *Service {
	return New(Options{
		Store:          in.db,
		GoAPI:          loadFakeGoServer(t),
		DataDir:        in.dataDir,
		EnvPath:        in.envFile,
		KeysPath:       in.keysPath,
		PubKeyPath:     filepath.Join(in.keysPath, "id_ed25519.pub"),
		APIKeyPath:     filepath.Join(in.keysPath, ".api_key"),
		DBPath:         filepath.Join(in.keysPath, "db_v2.sqlite3"),
		AppVersion:     appVersion,
		ChatFilesDir:   filepath.Join(in.consoleDir, "data", "chat-files"),
		AttachmentsDir: filepath.Join(in.consoleDir, "data", "attachments"),
	})
}

// snapshot is every table of a database as sorted rows of typed values.
func snapshot(t *testing.T, db *store.DB) map[string][]string {
	t.Helper()
	ctx := context.Background()
	out := map[string][]string{}
	if db.IsPostgres() {
		names, err := readStrings(ctx, db, `SELECT table_name FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			rows, err := readStrings(ctx, db, `SELECT t::text FROM `+quoteIdent(n)+` t`)
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(rows)
			out[n] = rows
		}
		return out
	}
	names, err := readStrings(ctx, db, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		cols, err := sqliteColumns(ctx, db, n)
		if err != nil {
			t.Fatal(err)
		}
		exprs := make([]string, len(cols))
		for i, c := range cols {
			exprs[i] = "quote(+" + quoteIdent(c) + ")"
		}
		rows, err := readStrings(ctx, db, `SELECT `+strings.Join(exprs, " || '|' || ")+` FROM `+quoteIdent(n))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(rows)
		out[n] = rows
	}
	return out
}

// files lists the files of an install's folders with their content.
func (in *install) files(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{in.dataDir, in.keysPath} {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || strings.HasPrefix(info.Name(), "db_v2.sqlite3") {
				return nil
			}
			data, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(filepath.Dir(root), path)
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		})
	}
	if data, err := os.ReadFile(in.envFile); err == nil {
		out[".env"] = string(data)
	}
	return out
}

func diffSnapshots(t *testing.T, label string, a, b map[string][]string) {
	t.Helper()
	for table, rows := range a {
		other := b[table]
		if strings.Join(rows, "\n") != strings.Join(other, "\n") {
			t.Errorf("%s: table %s differs:\n  %q\n  %q", label, table, rows, other)
		}
	}
	for table := range b {
		if _, ok := a[table]; !ok {
			t.Errorf("%s: table %s only in the second", label, table)
		}
	}
}

// tarEntries is the entries of an archive with their raw headers, the
// time and the header checksum taken out.
type tarEntry struct {
	name   string
	header []byte
	data   []byte
}

func tarEntries(t *testing.T, archive []byte) []tarEntry {
	t.Helper()
	tar, err := gunzip(archive, DefaultMaxArchiveBytes)
	if err != nil {
		t.Fatal(err)
	}
	var out []tarEntry
	for off := 0; off+blockSize <= len(tar); {
		h := append([]byte(nil), tar[off:off+blockSize]...)
		if allZero(h) {
			break
		}
		size := int(parseOctal(h[124:136]))
		copy(h[136:156], bytes.Repeat([]byte{' '}, 20))
		name := cutAtNUL(string(h[0:100]))
		if p := cutAtNUL(string(h[345:500])); p != "" {
			name = p + "/" + name
		}
		off += blockSize
		out = append(out, tarEntry{name: name, header: h, data: tar[off : off+size]})
		off += size + int(padToBlock(int64(size)))
	}
	return out
}

func restoreResultJSON(r RestoreResult) string {
	o := branding.NewObject()
	o.Set("restored", toAny(r.Restored))
	o.Set("skipped", toAny(r.Skipped))
	o.Set("warnings", toAny(r.Warnings))
	o.Set("requiresRestart", r.RequiresRestart)
	return branding.Stringify(o)
}

func toAny(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

func canonJSON(t *testing.T, raw []byte) string {
	t.Helper()
	v, err := branding.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return branding.Stringify(v)
}

var allParts = RestoreOptions{Database: true, Uploads: true, Secrets: true, Env: true}

func fixtureDir() string { return filepath.Join("testdata", "node-archives") }

// refusedByNodeOnly are the warnings of Node's restore, by dialect, for
// tables Go restores (BUGS.md BK8: the list in dlp_policies.rules goes in
// as JSON text).
var refusedByNodeOnly = map[string][]string{
	"postgres": {"Table dlp_policies: invalid input syntax for type json"},
}

// resultOfGo is a result of Node's restore as Go's restore of the same
// archive answers: without the warnings of refusedByNodeOnly, and with
// each of those tables counted in "database (N tables)".
func resultOfGo(t *testing.T, dialect, result string) string {
	t.Helper()
	v, err := branding.ParseJSON([]byte(result))
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*branding.Object)
	warnings, _ := o.Get("warnings")
	var kept []any
	more := 0
	for _, w := range warnings.([]any) {
		if slices.Contains(refusedByNodeOnly[dialect], w.(string)) {
			more++
			continue
		}
		kept = append(kept, w)
	}
	if kept == nil {
		kept = []any{}
	}
	o.Set("warnings", kept)
	restored, _ := o.Get("restored")
	list := restored.([]any)
	var n int
	if _, err := fmt.Sscanf(list[0].(string), "database (%d tables)", &n); err == nil {
		list[0] = fmt.Sprintf("database (%d tables)", n+more)
	}
	return branding.Stringify(o)
}

// fixtureRestoreTime is the clock of the restores of the Node archives.
var fixtureRestoreTime = time.Unix(1795000000, 0)

// fixtureTables are the rows Go restores from the Node archives otherwise
// than Node's own restore did, on purpose, by dialect and table.
var fixtureTables = map[string]map[string][]string{
	"sqlite": {
		// RV181: the server's users.credentials_changed_at, after last_login,
		// holds the time of the restore for every account the database did
		// not have, so that no sign-in token from before the restore works;
		// users.disabled after it (L107) is off for every account a Node
		// archive holds
		"users": {
			`50|'seed_admin'|'$2b$10$abcdefghijklmnopqrstuv'|'admin'|'local'|'JBSWY3DPEHPK3PXP'|1|'["AAAA-BBBB","CCCC-DDDD"]'|'2026-11-10 01:05:50'|''|1795000000|0|0|''|NULL|''|''|'säd😀@example.org'|''|''|''`,
			`51|'second'|'pbkdf2-sha256$600000$s$h'|'viewer'|'local'|''|0|NULL|'2026-11-18 14:58:36'|''|1795000000|0|0|''|NULL|''|''|''|''|''|''`,
		},
	},
	"postgres": {
		// BK8: Node's restore refused dlp_policies
		"dlp_policies": {`(1,p1,"","",log,"",t,"[{""a"": 1}]","2026-11-10 01:05:50.93+00","2026-11-18 14:58:46.584+00")`},
		// RV181 and L107, as on SQLite
		"users": {
			`(50,seed_admin,$2b$10$abcdefghijklmnopqrstuv,admin,local,JBSWY3DPEHPK3PXP,t,"[""AAAA-BBBB"",""CCCC-DDDD""]","2026-11-10 01:05:50.694+00",,1795000000,f,f,"",,"","",säd😀@example.org,"","","")`,
			`(51,second,pbkdf2-sha256$600000$s$h,viewer,local,"",f,,"2026-11-18 14:58:46.491+00",,1795000000,f,f,"",,"","","","","","")`,
		},
	},
}

// withoutShareLoginRow is the settings rows Node restored without the
// scheduled backup's network share login, which Go leaves out (L123).
func withoutShareLoginRow(rows []string) []string {
	var out []string
	for _, row := range rows {
		if !strings.Contains(row, shareLoginSetting) {
			out = append(out, row)
		}
	}
	return out
}

// Archives the Node console made, restored by Go: the database must hold
// what the Node console's own restore put there, table by table, but for
// refusedByNodeOnly and fixtureTables.
func TestNodeArchiveFixtures(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			archive, err := os.ReadFile(filepath.Join(fixtureDir(), dialect+".tar.gz"))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(fixtureDir(), dialect+".restored.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want struct {
				Result json.RawMessage     `json:"result"`
				Tables map[string][]string `json:"tables"`
			}
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			dst := newInstall(t, dialect)
			svc := dst.service(t, "0.0.0")
			svc.o.Now = func() time.Time { return fixtureRestoreTime }
			r, err := svc.RestoreFullBackup(context.Background(), archive, allParts)
			if err != nil {
				t.Fatal(err)
			}
			wantResult := resultOfGo(t, dialect, canonJSON(t, want.Result))
			if got := restoreResultJSON(r); got != wantResult {
				t.Errorf("result %s, want %s", got, wantResult)
			}
			got := snapshot(t, dst.db)
			for table, rows := range want.Tables {
				if fixed, ok := fixtureTables[dialect][table]; ok {
					rows = fixed
				}
				if table == "settings" {
					rows = withoutShareLoginRow(rows)
				}
				if strings.Join(rows, "\n") != strings.Join(got[table], "\n") {
					t.Errorf("table %s:\n  got  %q\n  want %q", table, got[table], rows)
				}
			}
			for name, content := range map[string]string{
				"data/uploads/logo-0011.png": "\x89PNG logo",
				"data/uploads/bg/nested.jpg": "nested background",
				"data/.session_secret":       "session-secret-value",
				"keys/id_ed25519":            "private-key-bytes",
				"keys/mesh_agent_server.pem": "-----BEGIN CERTIFICATE-----\n",
				".env":                       "SESSION_SECRET=x\nPORT=5000\n",
			} {
				if files := dst.files(t); files[name] != content {
					t.Errorf("file %s is %q, want %q", name, files[name], content)
				}
			}
		})
	}
}
