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

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
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
	if hasPassword && !r.Old && strings.HasPrefix(hash, "$2") {
		r.Verifies, _ = authn.CheckPassword(ctx, password, hash)
	}
	return r
}

func TestResetPasswordLikeNode(t *testing.T) {
	var cases []resetCase
	readVectors(t, "resetpassword", &cases)
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
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
	dsn := os.Getenv(storetest.PostgresEnv)
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
	if ok, _ := authn.CheckPassword(context.Background(), "Secret-123", hash); !ok || !strings.HasPrefix(hash, "$2b$12$") {
		t.Errorf("the first line without its line end is not the password: %s", hash[:7])
	}
}
