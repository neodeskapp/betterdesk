package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The two-way test of the archive format. An install is made up (a
// database with the console's and the server's tables and rows of every
// kind, uploads, keys, the .env), and both the Go service and the Node
// console's backup service make a full backup of it. The two archives
// must hold the same entries in the same order with the same bytes (the
// times aside). Then each archive is restored by both programs into fresh
// installs, and the four results are compared table by table and file by
// file. It needs node and web-nodejs (the dev image has both) and runs on
// SQLite and, when a server is configured, on PostgreSQL.
//
// With BACKUP_RECORD_FIXTURES=1 the Node archives and what the Node
// restore made of them are written to testdata/node-archives, which
// TestNodeArchiveFixtures replays without Node.

var webNodeJS = func() string {
	if dir := os.Getenv("WEB_NODEJS"); dir != "" {
		return dir
	}
	return "/repo/web-nodejs"
}()

func needNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	if _, err := os.Stat(filepath.Join(webNodeJS, "node_modules", "better-sqlite3")); err != nil {
		t.Skip("web-nodejs with its modules is not there")
	}
	if _, err := os.Stat(filepath.Join(webNodeJS, "services", "backupService.js")); err != nil {
		t.Skip("the Node console's backup service is gone")
	}
}

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

// fakeGoServer answers what goserver.json says, as the Node script's stub
// does.
type fakeGoServer struct{ data *goapi.Object }

func loadFakeGoServer(t *testing.T) fakeGoServer {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "nodeoracle", "goserver.json"))
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
		Store:        in.db,
		GoAPI:        loadFakeGoServer(t),
		DataDir:      in.dataDir,
		EnvPath:      in.envFile,
		KeysPath:     in.keysPath,
		PubKeyPath:   filepath.Join(in.keysPath, "id_ed25519.pub"),
		APIKeyPath:   filepath.Join(in.keysPath, ".api_key"),
		DBPath:       filepath.Join(in.keysPath, "db_v2.sqlite3"),
		AppVersion:   appVersion,
		ChatFilesDir: filepath.Join(in.consoleDir, "data", "chat-files"),
	})
}

// node runs the oracle script and decodes what it prints.
func (in *install) node(t *testing.T, mode string, extra map[string]any, out any) {
	t.Helper()
	spec := map[string]any{
		"dbType": in.dialect, "dbPath": in.dbPath, "databaseUrl": in.dsn,
		"dataDir": in.dataDir, "keysPath": in.keysPath, "envFile": in.envFile,
	}
	for k, v := range extra {
		spec[k] = v
	}
	raw, _ := json.Marshal(spec)
	specFile := filepath.Join(t.TempDir(), "spec.json")
	if err := os.WriteFile(specFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join("testdata", "nodeoracle", "roundtrip.js"), mode, specFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", mode, err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), out); err != nil {
		t.Fatalf("node %s printed %q: %v", mode, stdout, err)
	}
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

// readmeLinesNodeLacks are the lines of README.txt that tell where the
// parts go that Node never packed. An archive of Node's holds none of
// these parts, so the entries stay the same when the seed has none.
var readmeLinesNodeLacks = []string{
	// CH2: the chat files
	"     console/chat-files/*    -> <consoleDir>/data/chat-files/",
}

func compareArchives(t *testing.T, goArchive, nodeArchive []byte) {
	t.Helper()
	gos, nodes := tarEntries(t, goArchive), tarEntries(t, nodeArchive)
	var gn, nn []string
	for _, e := range gos {
		gn = append(gn, e.name)
	}
	for _, e := range nodes {
		nn = append(nn, e.name)
	}
	if strings.Join(gn, ",") != strings.Join(nn, ",") {
		t.Fatalf("entries differ:\n go:   %q\n node: %q", gn, nn)
	}
	for i := range gos {
		g, n := gos[i], nodes[i]
		gd, nd := g.data, n.data
		switch g.name {
		case "manifest.json", "README.txt":
			// the time of the backup differs; everything else must not
			gd = timeStamp.ReplaceAll(gd, []byte("<time>"))
			nd = timeStamp.ReplaceAll(nd, []byte("<time>"))
		}
		if g.name == "README.txt" {
			for _, line := range readmeLinesNodeLacks {
				gd = bytes.Replace(gd, []byte(line+"\n"), nil, 1)
			}
		}
		if !bytes.Equal(gd, nd) {
			// An archive holds secrets: the failure names sizes and the
			// first differing offset, never the content.
			at := 0
			for at < len(gd) && at < len(nd) && gd[at] == nd[at] {
				at++
			}
			t.Errorf("entry %s differs: %d and %d bytes, first difference at byte %d", g.name, len(gd), len(nd), at)
		}
		if g.name != "manifest.json" && g.name != "README.txt" && !bytes.Equal(g.header, n.header) {
			t.Errorf("entry %s: tar headers differ", g.name)
		}
	}
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

func nodeOptions(o RestoreOptions) map[string]any {
	return map[string]any{"restoreDatabase": o.Database, "restoreUploads": o.Uploads, "restoreSecrets": o.Secrets, "restoreEnv": o.Env, "restoreGoDb": o.GoDB}
}

func TestArchivesRoundTripWithNode(t *testing.T) {
	needNode(t)
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			src := newInstall(t, dialect)
			src.seed(t)

			var made struct {
				Filename   string `json:"filename"`
				AppVersion string `json:"appVersion"`
			}
			nodeFile := filepath.Join(t.TempDir(), "node.tar.gz")
			src.node(t, "make", map[string]any{"out": nodeFile}, &made)
			nodeArchive, err := os.ReadFile(nodeFile)
			if err != nil {
				t.Fatal(err)
			}
			goArchive, goName, err := src.service(t, made.AppVersion).CreateFullBackup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(goName, "betterdesk-backup-") || len(goName) != len(made.Filename) {
				t.Errorf("file name %q, node %q", goName, made.Filename)
			}
			compareArchives(t, goArchive, nodeArchive)
			if t.Failed() {
				return
			}
			want := snapshot(t, src.db)
			wantFiles := src.files(t)

			type outcome struct {
				result string
				tables map[string][]string
				files  map[string]string
			}
			restore := func(archive []byte, byNode bool) outcome {
				dst := newInstall(t, dialect)
				var result string
				if byNode {
					file := filepath.Join(t.TempDir(), "in.tar.gz")
					if err := os.WriteFile(file, archive, 0o600); err != nil {
						t.Fatal(err)
					}
					var got struct {
						Result json.RawMessage `json:"result"`
						Error  string          `json:"error"`
					}
					dst.node(t, "restore", map[string]any{"in": file, "opts": nodeOptions(allParts)}, &got)
					if got.Error != "" {
						t.Fatalf("node restore: %s", got.Error)
					}
					result = canonJSON(t, got.Result)
				} else {
					r, err := dst.service(t, made.AppVersion).RestoreFullBackup(ctx, archive, allParts)
					if err != nil {
						t.Fatalf("go restore: %v", err)
					}
					result = restoreResultJSON(r)
				}
				return outcome{result, snapshot(t, dst.db), dst.files(t)}
			}
			outcomes := map[string]outcome{
				"node archive, go restore":   restore(nodeArchive, false),
				"node archive, node restore": restore(nodeArchive, true),
				"go archive, node restore":   restore(goArchive, true),
				"go archive, go restore":     restore(goArchive, false),
			}
			base := outcomes["node archive, node restore"]
			t.Logf("node restore result: %s", base.result)
			for label, o := range outcomes {
				if o.result != base.result {
					t.Errorf("%s: result %s, node restore of the node archive %s", label, o.result, base.result)
				}
				diffSnapshots(t, label, base.tables, o.tables)
				for name, content := range wantFiles {
					if strings.HasPrefix(name, "keys/db_v2") {
						continue
					}
					if o.files[name] != content {
						t.Errorf("%s: file %s is %q, want %q", label, name, o.files[name], content)
					}
				}
			}
			// What the restore cannot bring back as it was, on both sides:
			// the tables with a warning; on SQLite the settings (an integer
			// in a text column comes back as "7.0", broken UTF-8 as U+FFFD)
			// and an integer above 2^53, which the dump holds as a
			// JavaScript number (BUGS.md BK2).
			// On PostgreSQL every time keeps its milliseconds only: the dump
			// holds JavaScript dates (BUGS.md BK3).
			lossy := map[string]bool{}
			if dialect == "sqlite" {
				lossy["settings"], lossy["device_telemetry"] = true, true
			}
			for table, rows := range want {
				if strings.Contains(base.result, "Table "+table+":") || lossy[table] {
					continue
				}
				if dialect == "postgres" {
					rows = append([]string(nil), rows...)
					for i := range rows {
						rows[i] = microseconds.ReplaceAllStringFunc(rows[i], toMilliseconds)
					}
				}
				if strings.Join(rows, "\n") != strings.Join(base.tables[table], "\n") {
					t.Errorf("table %s did not come back as it was:\n  %q\n  %q", table, rows, base.tables[table])
				}
			}

			if os.Getenv("BACKUP_RECORD_FIXTURES") == "1" {
				recordFixture(t, dialect, nodeArchive, base.result, base.tables)
			}
		})
	}
}

func fixtureDir() string { return filepath.Join("testdata", "node-archives") }

func recordFixture(t *testing.T, dialect string, archive []byte, result string, tables map[string][]string) {
	t.Helper()
	if err := os.MkdirAll(fixtureDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir(), dialect+".tar.gz"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"result": json.RawMessage(result), "tables": tables}, "", " ")
	if err := os.WriteFile(filepath.Join(fixtureDir(), dialect+".restored.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Archives the Node console made, restored by Go: the database must hold
// what the Node console's own restore put there, table by table.
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
			r, err := dst.service(t, "0.0.0").RestoreFullBackup(context.Background(), archive, allParts)
			if err != nil {
				t.Fatal(err)
			}
			if got := restoreResultJSON(r); got != canonJSON(t, want.Result) {
				t.Errorf("result %s, want %s", got, want.Result)
			}
			got := snapshot(t, dst.db)
			for table, rows := range want.Tables {
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

var microseconds = regexp.MustCompile(`\d\d:\d\d:\d\d\.\d+[+-]\d\d`)

// toMilliseconds cuts the fraction of a PostgreSQL time to three digits,
// written as PostgreSQL writes it (no zeros at the end).
func toMilliseconds(s string) string {
	dot := strings.IndexByte(s, '.')
	zone := strings.IndexAny(s[dot:], "+-") + dot
	frac := s[dot+1 : zone]
	if len(frac) > 3 {
		frac = frac[:3]
	}
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return s[:dot] + s[zone:]
	}
	return s[:dot+1] + frac + s[zone:]
}

var timeStamp = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z`)
