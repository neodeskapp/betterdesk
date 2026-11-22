package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/db"
)

type resetRow struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Prefix   string `json:"prefix"`
	Length   int    `json:"length"`
	Old      bool   `json:"old"`
	Verifies bool   `json:"verifies"`
}

type resetState struct {
	Exists  bool       `json:"exists"`
	Table   *string    `json:"table"`
	Error   string     `json:"error"`
	Columns []string   `json:"columns"`
	Rows    []resetRow `json:"rows"`
}

type resetCase struct {
	Name     string            `json:"name"`
	Database string            `json:"database"`
	Setup    string            `json:"setup"`
	DB       string            `json:"db"`
	Env      map[string]string `json:"env"`
	Dotenv   *string           `json:"dotenv"`
	Argv     []string          `json:"argv"`
	CwdData  bool              `json:"cwdData"`
	MakeDirs []string          `json:"makeDirs"`
	Status   int               `json:"status"`
	Stdout   string            `json:"stdout"`
	Stderr   string            `json:"stderr"`
	State    resetState        `json:"state"`
}

// The passwords the oracle used, by name.
var resetPasswords = map[string]string{
	"plain":   "Neu-Passw0rd",
	"short":   "short",
	"six":     "sixsix",
	"accent5": "abécd",
	"accent6": "abécde",
	"emoji6":  "a\U0001F600bcd",
	"emoji4":  "\U0001F600\U0001F600",
	"long":    strings.Repeat("x", 80),
	"long72":  strings.Repeat("y", 71) + "éz",
	"spaces":  " pass word 1 ",
	"quotes":  "p@$$\"w'rd`",
}

// The users table of the signal server on SQLite (db/sqlite.go), as the
// oracle made it.
const serverUsersTable = `CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'viewer',
			auth_provider TEXT NOT NULL DEFAULT 'local',
			totp_secret TEXT DEFAULT '',
			totp_enabled INTEGER DEFAULT 0,
			totp_recovery_codes TEXT DEFAULT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			last_login TEXT DEFAULT ''
		)`

// resetRun is one replayed run: the arguments the script had become the
// user name, the password goes to standard input.
func resetRun(t *testing.T, argv []string, env map[string]string, cwd, root string) (int, string, string) {
	t.Helper()
	real := make([]string, len(argv))
	for i, a := range argv {
		if p, ok := resetPasswords[a]; ok {
			real[i] = p
		} else {
			real[i] = a
		}
	}
	stdin := ""
	var args []string
	if len(real) > 0 {
		stdin = real[0] + "\n"
		args = real[1:]
	}
	var out, errOut bytes.Buffer
	e := Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut, Getenv: func(k string) string { return env[k] }}
	status := resetPassword(args, e, resetOptions{
		random:   rand.Reader,
		terminal: func(io.Reader) bool { return false },
		exists: func(p string) bool {
			// Only what the test made: an installed console elsewhere
			// on this machine is not looked at
			if filepath.IsAbs(p) && !strings.HasPrefix(p, root) {
				return false
			}
			_, err := os.Stat(p)
			return err == nil
		},
		cwd: cwd,
	})
	return status, out.String(), errOut.String()
}

func resetPasswordOf(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	p, ok := resetPasswords[argv[0]]
	return p, ok
}

func compareReset(t *testing.T, c resetCase, status int, stdout, stderr string, got resetState, oldHash string) {
	t.Helper()
	if status != c.Status {
		t.Errorf("%s: exit %d, Node %d (stderr %q)", c.Name, status, c.Status, stderr)
	}
	if stdout != c.Stdout {
		t.Errorf("%s: stdout\n got %q\nwant %q", c.Name, stdout, c.Stdout)
	}
	if strings.HasPrefix(c.Stderr, "Usage:") {
		if !strings.HasPrefix(stderr, "Usage: betterdesk-server reset-password") {
			t.Errorf("%s: stderr %q, want the usage", c.Name, stderr)
		}
	} else if stderr != c.Stderr {
		t.Errorf("%s: stderr\n got %q\nwant %q", c.Name, stderr, c.Stderr)
	}
	want := c.State
	if got.Exists != want.Exists || got.Error != want.Error || (want.Table == nil) != (got.Table == nil) ||
		(want.Table != nil && *want.Table != *got.Table) || strings.Join(got.Columns, ",") != strings.Join(want.Columns, ",") {
		t.Errorf("%s: database\n got %+v\nwant %+v", c.Name, got, want)
		return
	}
	if len(got.Rows) != len(want.Rows) {
		t.Errorf("%s: rows %+v, Node %+v", c.Name, got.Rows, want.Rows)
		return
	}
	for i, w := range want.Rows {
		g := got.Rows[i]
		// The old hash was made with another cost in each run
		if w.Old {
			w.Prefix = g.Prefix
		}
		if g != w {
			t.Errorf("%s: row %d %+v, Node %+v", c.Name, i, g, w)
		}
	}
}

func describeRow(ctx context.Context, username, role, hash, oldHash, password string, hasPassword bool) resetRow {
	r := resetRow{Username: username, Role: role, Prefix: hash[:min(7, len(hash))], Length: len(hash), Old: hash == oldHash}
	// The old hash is for another password: no need to check it
	if hasPassword && !r.Old {
		r.Verifies, _ = authn.CheckPassword(ctx, password, hash)
	}
	return r
}

// serverStart is the users table a server start makes in an empty
// database: the signal server's migration, then the console's (main.go
// and startConsole in console_start.go).
type serverStart struct {
	// sqliteTable is the table's SQL on SQLite
	sqliteTable string
	// postgresColumns are its columns on PostgreSQL, in order; empty
	// without a PostgreSQL to test on
	postgresColumns []string
}

// startServerSchema migrates an empty database at dsn as a server start
// does.
func startServerSchema(t *testing.T, dsn string) {
	t.Helper()
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	var console *store.DB
	if pg, ok := database.(*db.PostgresDB); ok {
		console = store.OpenPostgres(pg.Pool())
	} else if console, err = store.OpenSQLite(dsn); err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	if err := console.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newServerStart(t *testing.T, pgDSN string) serverStart {
	t.Helper()
	file := filepath.Join(t.TempDir(), "start.sqlite3")
	startServerSchema(t, file)
	var s serverStart
	sdb, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	if err := sdb.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'").Scan(&s.sqliteTable); err != nil {
		t.Fatal(err)
	}
	if pgDSN != "" {
		dsn, schema, drop := freshSchema(t, pgDSN, "bd_reset_start")
		defer drop()
		startServerSchema(t, dsn)
		s.postgresColumns = postgresColumns(t, pgDSN, schema)
	}
	return s
}

// freshSchema makes an empty PostgreSQL schema and answers a DSN whose
// search_path is it.
func freshSchema(t *testing.T, dsn, prefix string) (string, string, func()) {
	t.Helper()
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("%s_%d", prefix, os.Getpid())
	for _, statement := range []string{"DROP SCHEMA IF EXISTS " + schema + " CASCADE", "CREATE SCHEMA " + schema} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), schema, func() {
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	}
}

func postgresColumns(t *testing.T, dsn, schema string) []string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	rows, err := admin.Query("SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'users' ORDER BY ordinal_position", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		columns = append(columns, name)
	}
	return columns
}

// resetHashLength is the length of the API server's password hash:
// "pbkdf2-sha256$600000$", 32 hex digits of salt, "$", 64 of hash.
const resetHashLength = 118

// resetFix turns a recorded run into what reset-password answers since
// bug fixes changed it on purpose, and names the bugs (none for a run no
// fix changed).
func resetFix(c *resetCase, start serverStart) []string {
	bugs := []string{}
	if bug := resetFixRun(c, start); bug != "" {
		bugs = append(bugs, bug)
	}
	// S12: the new hash is the API server's PBKDF2 hash, where the script
	// wrote bcrypt with cost 12 ("$2b$12$", 60 letters)
	if c.Status == 0 {
		c.Stdout = strings.Replace(c.Stdout, "Hash type: bcrypt, length: 60\n",
			fmt.Sprintf("Hash type: pbkdf2-sha256, length: %d\n", resetHashLength), 1)
		for i, row := range c.State.Rows {
			if !row.Old {
				c.State.Rows[i].Prefix, c.State.Rows[i].Length = "pbkdf2-", resetHashLength
			}
		}
		bugs = append(bugs, "S12")
	}
	return bugs
}

// resetFixRun is the part of resetFix that changes how a run ends.
func resetFixRun(c *resetCase, start serverStart) string {
	// BN5: a password under 6 characters is refused before the database
	// is chosen or opened: nothing is printed about it and nothing made
	if password, ok := resetPasswordOf(c.Argv); ok && utf16Length(password) < 6 {
		c.Status, c.Stdout, c.Stderr = 1, "", "Password must be at least 6 characters\n"
		switch c.Setup {
		case "none":
			c.State = resetState{}
		case "emptyfile":
			c.State = resetState{Exists: true}
		}
		return "BN5"
	}
	// BN6: a database without a users table gets the one a server start
	// makes, where the script made a table of its own
	if (c.Setup == "none" || c.Setup == "emptyfile") && c.Status == 0 {
		if c.Database == "postgres" {
			c.State.Columns = start.postgresColumns
		} else {
			table := start.sqliteTable
			c.State.Table = &table
		}
		return "BN6"
	}
	// BN7: the database is the one the server opens, DB_URL first; the
	// script took DB_PATH first. Here DB_URL names a folder that is not
	// there, and the accounts in DB_PATH's file keep their passwords
	if dbURL, dbPath := c.Env["DB_URL"], c.Env["DB_PATH"]; dbURL != "" && dbPath != "" && dbURL != dbPath && c.Status == 0 {
		c.Status, c.Stdout = 1, "Auth database (SQLite): "+dbURL+"\n"
		c.Stderr = "Error: Cannot open database because the directory does not exist\n"
		for i := range c.State.Rows {
			c.State.Rows[i].Old, c.State.Rows[i].Verifies, c.State.Rows[i].Length = true, false, 60
		}
		return "BN7"
	}
	return ""
}

func TestResetPasswordLikeNode(t *testing.T) {
	var cases []resetCase
	readVectors(t, "resetpassword", &cases)
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
	dsn := os.Getenv(storetest.PostgresEnv)
	start := newServerStart(t, dsn)
	fixed := map[string]int{}
	for i := range cases {
		for _, bug := range resetFix(&cases[i], start) {
			fixed[bug]++
		}
	}
	t.Logf("runs that differ from Node by a fix: %v", fixed)
	// The hash the accounts had: cheap, it is only compared
	old, err := bcrypt.GenerateFromPassword([]byte("the-old-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := string(old)
	consoleUsers, err := os.ReadFile(filepath.Join("..", "console", "store", "schema", "sqlite", "users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	pgUsers, err := os.ReadFile(filepath.Join("..", "console", "store", "schema", "postgres", "users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	replayed := map[string]int{}
	for _, c := range cases {
		switch c.Database {
		case "sqlite":
			replaySQLiteReset(t, c, string(consoleUsers), oldHash)
		case "postgres":
			if dsn == "" {
				continue
			}
			replayPostgresReset(t, c, dsn, string(pgUsers), oldHash)
		}
		replayed[c.Database]++
	}
	if dsn == "" {
		t.Logf("no %s: the PostgreSQL runs are left out", storetest.PostgresEnv)
	}
	t.Logf("replayed %v", replayed)
}

func replaySQLiteReset(t *testing.T, c resetCase, consoleUsers, oldHash string) {
	ctx := context.Background()
	root := t.TempDir()
	put := func(s string) string { return strings.ReplaceAll(s, "{ROOT}", root) }
	consoleDir, work := filepath.Join(root, "console"), filepath.Join(root, "work")
	for _, dir := range append([]string{consoleDir, work}, c.MakeDirs...) {
		if err := os.MkdirAll(put(dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if c.CwdData {
		if err := os.Mkdir(filepath.Join(work, "data"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := put(c.DB)
	setupResetSQLite(t, file, c.Setup, consoleUsers, oldHash)
	if c.Dotenv != nil {
		if err := os.WriteFile(filepath.Join(consoleDir, ".env"), []byte(put(*c.Dotenv)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"CONSOLE_DIR": consoleDir}
	for k, v := range c.Env {
		env[k] = put(v)
	}
	t.Chdir(work)
	status, stdout, stderr := resetRun(t, c.Argv, env, work, root)
	password, hasPassword := resetPasswordOf(c.Argv)
	state := resetState{}
	if _, err := os.Stat(file); err == nil {
		state.Exists = true
		db, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		var table string
		err = db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'").Scan(&table)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			state.Error = sqliteMessage(err)
		default:
			state.Table = &table
			rows, err := db.QueryContext(ctx, "SELECT username, role, password_hash FROM users ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			state.Rows = []resetRow{}
			for rows.Next() {
				var username, role, hash string
				if err := rows.Scan(&username, &role, &hash); err != nil {
					t.Fatal(err)
				}
				state.Rows = append(state.Rows, describeRow(ctx, username, role, hash, oldHash, password, hasPassword))
			}
			_ = rows.Close()
		}
		_ = db.Close()
	}
	compareReset(t, c, status, strings.ReplaceAll(stdout, root, "{ROOT}"), strings.ReplaceAll(stderr, root, "{ROOT}"), state, oldHash)
}

func setupResetSQLite(t *testing.T, file, kind, consoleUsers, oldHash string) {
	t.Helper()
	if kind == "nodir" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "none":
		return
	case "emptyfile":
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	case "notadb":
		if err := os.WriteFile(file, []byte("this is not a database, it is long enough to have a header..........................."), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	table := consoleUsers
	if kind == "server" {
		table = serverUsersTable
	}
	for _, statement := range []string{table,
		fmt.Sprintf("INSERT INTO users (username, password_hash, role) VALUES ('admin', '%s', 'admin')", oldHash),
		fmt.Sprintf("INSERT INTO users (username, password_hash, role) VALUES ('operator1', '%s', 'operator')", oldHash),
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func replayPostgresReset(t *testing.T, c resetCase, dsn, pgUsers, oldHash string) {
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := fmt.Sprintf("bd_reset_go_%d", os.Getpid())
	if _, err := admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	if c.Setup == "console" {
		for _, statement := range []string{"SET search_path TO " + schema, pgUsers,
			fmt.Sprintf("INSERT INTO users (username, password_hash, role) VALUES ('admin', '%s', 'admin'), ('operator1', '%s', 'operator')", oldHash, oldHash),
		} {
			if _, err := admin.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	work := t.TempDir()
	status, stdout, stderr := resetRun(t, c.Argv, map[string]string{"DB_TYPE": "postgres", "DATABASE_URL": u.String(), "CONSOLE_DIR": work}, work, work)
	password, hasPassword := resetPasswordOf(c.Argv)
	state := resetState{}
	var exists *string
	if err := admin.QueryRowContext(ctx, "SELECT to_regclass($1)::text", schema+".users").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != nil {
		state.Exists = true
		rows, err := admin.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'users' ORDER BY ordinal_position", schema)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			_ = rows.Scan(&name)
			state.Columns = append(state.Columns, name)
		}
		_ = rows.Close()
		rows, err = admin.QueryContext(ctx, "SELECT username, role, password_hash FROM "+schema+".users ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		state.Rows = []resetRow{}
		for rows.Next() {
			var username, role, hash string
			_ = rows.Scan(&username, &role, &hash)
			state.Rows = append(state.Rows, describeRow(ctx, username, role, hash, oldHash, password, hasPassword))
		}
		_ = rows.Close()
	}
	compareReset(t, c, status, stdout, stderr, state, oldHash)
}

// runReset runs reset-password for the account admin with a password on
// a database the environment names.
func runReset(t *testing.T, env map[string]string, cwd string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	e := Env{Stdin: strings.NewReader("Neu-Passw0rd\n"), Stdout: &out, Stderr: &out, Getenv: func(k string) string { return env[k] }}
	status := resetPassword([]string{"admin"}, e, resetOptions{random: rand.Reader, terminal: func(io.Reader) bool { return false },
		exists: func(p string) bool { _, err := os.Stat(p); return err == nil }, cwd: cwd})
	return status, out.String()
}

// Without a users table, reset-password sets the database up as a server
// start does, so the account is in the table the server and the console
// make themselves, with every column the console reads (BN6).
func TestResetPasswordMakesTheUsersTableOfAServerStart(t *testing.T) {
	dsn := os.Getenv(storetest.PostgresEnv)
	start := newServerStart(t, dsn)
	root := t.TempDir()
	file := filepath.Join(root, "db_v2.sqlite3")
	if status, out := runReset(t, map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}, root); status != 0 {
		t.Fatalf("SQLite: %d %q", status, out)
	}
	sdb, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var table string
	if err := sdb.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'").Scan(&table); err != nil {
		t.Fatal(err)
	}
	var language sql.NullString
	if err := sdb.QueryRow("SELECT preferred_language FROM users WHERE username = 'admin' AND role = 'admin'").Scan(&language); err != nil {
		t.Errorf("SQLite: the account has no preferred_language: %v", err)
	}
	_ = sdb.Close()
	if table != start.sqliteTable {
		t.Errorf("SQLite users table\n got %s\nwant %s", table, start.sqliteTable)
	}
	// The server starts on it as on its own
	startServerSchema(t, file)

	if dsn == "" {
		t.Logf("no %s: PostgreSQL is left out", storetest.PostgresEnv)
		return
	}
	pgDSN, schema, drop := freshSchema(t, dsn, "bd_reset_table")
	defer drop()
	if status, out := runReset(t, map[string]string{"DB_TYPE": "postgres", "DATABASE_URL": pgDSN, "CONSOLE_DIR": root}, root); status != 0 {
		t.Fatalf("PostgreSQL: %d %q", status, out)
	}
	if got := postgresColumns(t, dsn, schema); strings.Join(got, ",") != strings.Join(start.postgresColumns, ",") {
		t.Errorf("PostgreSQL users columns\n got %v\nwant %v", got, start.postgresColumns)
	}
	startServerSchema(t, pgDSN)
}

// reset-password writes into the database the server opens: DATABASE_URL
// first, by its scheme (L70), then DB_URL of an older install (BN7); then
// the console's keys.
func TestResetPasswordUsesTheServersDatabase(t *testing.T) {
	root := t.TempDir()
	serverFile, consoleFile := filepath.Join(root, "server.sqlite3"), filepath.Join(root, "console.sqlite3")
	accountIn := func(file string) bool {
		sdb, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
		if err != nil {
			return false
		}
		defer sdb.Close()
		var n int
		return sdb.QueryRow("SELECT count(*) FROM users WHERE username = 'admin'").Scan(&n) == nil && n == 1
	}
	// DATABASE_URL wins over DB_URL and DB_PATH; a file in DB_URL wins over
	// DB_PATH; DB_TYPE=sqlite keeps an older install on its file where
	// DATABASE_URL still names PostgreSQL
	for _, env := range []map[string]string{
		{"DATABASE_URL": serverFile, "DB_URL": consoleFile, "DB_PATH": consoleFile},
		{"DATABASE_URL": "sqlite://" + serverFile, "DB_TYPE": "sqlite", "DB_PATH": consoleFile},
		{"DB_URL": serverFile, "DB_PATH": consoleFile},
		{"DB_URL": serverFile, "DB_TYPE": "sqlite", "DATABASE_URL": "postgres://nobody@127.0.0.1:1/none"},
	} {
		env["CONSOLE_DIR"] = root
		if status, out := runReset(t, env, root); status != 0 || !strings.Contains(out, "Auth database (SQLite): "+serverFile+"\n") {
			t.Errorf("%v: %d %q", env, status, out)
		}
		if !accountIn(serverFile) || accountIn(consoleFile) {
			t.Errorf("%v: the account is not in DB_URL's file alone", env)
		}
	}

	dsn := os.Getenv(storetest.PostgresEnv)
	if dsn == "" {
		t.Logf("no %s: PostgreSQL is left out", storetest.PostgresEnv)
		return
	}
	// A postgres:// DATABASE_URL or DB_URL is PostgreSQL, without DB_TYPE
	// and next to a DB_PATH
	pgDSN, schema, drop := freshSchema(t, dsn, "bd_reset_url")
	defer drop()
	for _, env := range []map[string]string{
		{"DATABASE_URL": pgDSN, "DB_URL": consoleFile},
		{"DB_URL": pgDSN},
		{"DB_URL": strings.Replace(pgDSN, "postgres://", "postgresql://", 1), "DB_PATH": consoleFile},
	} {
		env["CONSOLE_DIR"] = root
		if status, out := runReset(t, env, root); status != 0 || !strings.HasPrefix(out, "Auth database (PostgreSQL): ") {
			t.Errorf("%v: %d %q", env, status, out)
		}
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	var n int
	if err := admin.QueryRow("SELECT count(*) FROM " + schema + ".users WHERE username = 'admin'").Scan(&n); err != nil || n != 1 {
		t.Errorf("PostgreSQL account: %d %v", n, err)
	}
	if _, err := os.Stat(consoleFile); err == nil {
		t.Error("a file named by DB_PATH was made")
	}
}

// A password that is too short is refused before anything is opened: the
// folder stays empty (BN5).
func TestResetPasswordShortPasswordOpensNothing(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "db_v2.sqlite3")
	for _, password := range []string{"short", "abécd", "\U0001F600\U0001F600", ""} {
		var out, errOut bytes.Buffer
		env := map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}
		e := Env{Stdin: strings.NewReader(password + "\n"), Stdout: &out, Stderr: &errOut, Getenv: func(k string) string { return env[k] }}
		status := resetPassword([]string{"admin"}, e, resetOptions{random: rand.Reader, terminal: func(io.Reader) bool { return false },
			exists: func(p string) bool { _, err := os.Stat(p); return err == nil }, cwd: root})
		if status != 1 || out.String() != "" || errOut.String() != "Password must be at least 6 characters\n" {
			t.Errorf("%q: %d, stdout %q, stderr %q", password, status, out.String(), errOut.String())
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%q: the folder holds %v", password, entries)
		}
	}
}

// The password comes from standard input only: a second argument, a flag
// and a terminal are refused before anything is opened, and the password
// is never printed.
func TestResetPasswordTakesThePasswordFromStandardInputOnly(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "db.sqlite3")
	env := map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}
	run := func(args []string, stdin string, terminal bool) (int, string) {
		var out bytes.Buffer
		e := Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &out, Getenv: func(k string) string { return env[k] }}
		status := resetPassword(args, e, resetOptions{random: rand.Reader, terminal: func(io.Reader) bool { return terminal },
			exists: func(p string) bool { _, err := os.Stat(p); return err == nil }, cwd: root})
		return status, out.String()
	}
	for _, c := range []struct {
		args     []string
		stdin    string
		terminal bool
	}{
		{[]string{"admin", "Secret-123"}, "Secret-123\n", false},
		{[]string{"--delete-all"}, "Secret-123\n", false},
		{[]string{"admin"}, "Secret-123\n", true},
		{nil, "", false},
	} {
		status, out := run(c.args, c.stdin, c.terminal)
		if status != 1 || !strings.Contains(out, "Usage: betterdesk-server reset-password") {
			t.Errorf("%v (terminal %v): %d %q", c.args, c.terminal, status, out)
		}
		if _, err := os.Stat(file); err == nil {
			t.Fatalf("%v: the database was opened", c.args)
		}
	}
	status, out := run([]string{"admin"}, "Secret-123\r\nsecond line\n", false)
	if status != 0 || strings.Contains(out, "Secret-123") {
		t.Fatalf("%d %q", status, out)
	}
	db, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE username = 'admin'").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := authn.CheckPassword(context.Background(), "Secret-123", hash); !ok {
		t.Errorf("the first line without its line end is not the password: %s", hash[:7])
	}
}

// reset-password hashes the password as the API server does, so neither
// sign-in re-hashes it and both accept it (S12).
func TestResetPasswordHashesAsTheAPIServer(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "db_v2.sqlite3")
	status, out := runReset(t, map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}, root)
	if status != 0 {
		t.Fatalf("%d %q", status, out)
	}
	db, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE username = 'admin'").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if prefix := fmt.Sprintf("pbkdf2-sha256$%d$", auth.PasswordIterations); !strings.HasPrefix(hash, prefix) || len(hash) != resetHashLength {
		t.Errorf("hash %.21s... of length %d, want %s... of length %d", hash, len(hash), prefix, resetHashLength)
	}
	if !auth.VerifyPassword(hash, "Neu-Passw0rd") || auth.NeedsRehash(hash) {
		t.Error("the API server does not take the hash as its own")
	}
	if ok, _ := authn.CheckPassword(context.Background(), "Neu-Passw0rd", hash); !ok {
		t.Error("the console's sign-in does not take the hash")
	}
	if want := fmt.Sprintf("Hash type: pbkdf2-sha256, length: %d\n", resetHashLength); !strings.Contains(out, want) {
		t.Errorf("output %q, want %q in it", out, want)
	}
}
