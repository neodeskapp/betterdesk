package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	// The SQLite driver ("sqlite")
	_ "modernc.org/sqlite"

	"github.com/unitronix/betterdesk-server/console/authn"
	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/db"
)

const resetUsage = "Usage: betterdesk-server reset-password [username]   (the new password is the first line of standard input)"

// resetOptions are what reset-password reads besides Env. Tests replace
// them.
type resetOptions struct {
	// random is the source of the bcrypt salt.
	random io.Reader
	// terminal reports whether standard input is a terminal.
	terminal func(io.Reader) bool
	// exists reports whether a path exists (the data folder search).
	exists func(string) bool
	// cwd is the working folder.
	cwd string
}

// ResetPassword is "reset-password [username]", what reset-password.js
// did: it sets the password of an account, or makes an admin account of
// that name when there is none (the name is "admin" when not given), on
// the database the server and its console use: DB_URL as the server reads
// it (PostgreSQL for a postgres:// URL, else a SQLite file), else DB_TYPE
// postgres with DATABASE_URL, else the SQLite file DB_PATH, or
// db_v2.sqlite3 in KEYS_PATH, RUSTDESK_DIR or the data folder. It reads
// the console's env file for
// what the environment does not set, as the script read its .env: the
// file BETTERDESK_ENV_FILE names (main loads it), else the .env in
// CONSOLE_DIR. A database without a users table is first set up as a
// server start sets it up.
//
// The new password is the first line of standard input, without its line
// end. It is never taken from the arguments and never printed, and a
// terminal on standard input is refused, because the password would show
// as it is typed. The script's --delete-all is not taken.
//
// It prints what the script printed and exits with 0 when the password
// was set and 1 otherwise.
func ResetPassword(args []string, env Env) int {
	cwd, _ := os.Getwd()
	return resetPassword(args, env, resetOptions{
		random:   rand.Reader,
		terminal: isTerminal,
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		cwd: cwd,
	})
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func resetPassword(args []string, env Env, opts resetOptions) int {
	o, e := env.Stdout, env.Stderr
	usage := func(hint string) int {
		fmt.Fprintln(e, resetUsage)
		if hint != "" {
			fmt.Fprintln(e, hint)
		}
		return 1
	}
	getenv := consoleEnv(env.Getenv)

	username := "admin"
	switch {
	case len(args) > 1:
		return usage("The password is read from standard input, not from the arguments.")
	case len(args) == 1 && strings.HasPrefix(args[0], "-"):
		return usage("")
	case len(args) == 1 && args[0] != "":
		username = args[0]
	}
	if opts.terminal(env.Stdin) {
		return usage(`Pipe it in, for example: printf '%s\n' "$PASSWORD" | betterdesk-server reset-password admin`)
	}
	password, ok, err := firstLine(env.Stdin)
	if err != nil {
		fmt.Fprintf(e, "Error: %v\n", err)
		return 1
	}
	if !ok {
		return usage("")
	}
	// Checked before the database is opened, so a refused password
	// leaves nothing behind
	if utf16Length(password) < 6 {
		fmt.Fprintln(e, "Password must be at least 6 characters")
		return 1
	}

	ctx := context.Background()
	var users *sql.DB
	// The database is the one the server opens: DB_URL by its scheme,
	// then the console's keys
	dialect, dsn := "sqlite", ""
	dbURL := getenv("DB_URL")
	dbType := strings.ToLower(orElse(getenv("DB_TYPE"), "sqlite"))
	switch {
	case isPostgresURL(dbURL):
		dialect, dsn = "postgres", dbURL
	case dbURL == "" && (dbType == "postgres" || dbType == "postgresql"):
		dialect, dsn = "postgres", getenv("DATABASE_URL")
		if dsn == "" {
			fmt.Fprintln(e, "DB_TYPE=postgres but DATABASE_URL is not set")
			return 1
		}
	}
	if dialect == "postgres" {
		u, err := url.Parse(dsn)
		if err != nil || u.Scheme == "" {
			fmt.Fprintln(e, "Error: Invalid URL")
			return 1
		}
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		fmt.Fprintf(o, "Auth database (PostgreSQL): %s\n", host)
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			fmt.Fprintf(e, "Error: %v\n", err)
			return 1
		}
		users = stdlib.OpenDB(*cfg)
	} else {
		dsn = sqlitePath(getenv, opts)
		fmt.Fprintf(o, "Auth database (SQLite): %s\n", dsn)
		if !opts.exists(filepath.Dir(dsn)) {
			fmt.Fprintln(e, "Error: Cannot open database because the directory does not exist")
			return 1
		}
		users, err = openResetSQLite(ctx, dsn)
		if err != nil {
			fmt.Fprintf(e, "Error: %s\n", sqliteMessage(err))
			return 1
		}
	}
	defer users.Close()

	// A database without accounts is set up as a server start sets it up,
	// so the console finds the users table it makes itself
	hasUsers, err := hasUsersTable(ctx, users, dialect)
	if err == nil && !hasUsers {
		err = migrateLikeTheServer(ctx, dsn)
	}
	if err != nil {
		fmt.Fprintf(e, "Error: %s\n", sqliteMessage(err))
		return 1
	}

	hash, err := authn.NewPasswordHash(ctx, opts.random, password)
	if err != nil {
		fmt.Fprintf(e, "Error: %v\n", err)
		return 1
	}
	if valid, err := authn.CheckPassword(ctx, password, hash); err != nil || !valid {
		fmt.Fprintln(e, "CRITICAL: bcrypt self-test failed! The generated hash cannot verify the password.")
		return 1
	}

	bind := func(query string) string {
		if dialect != "postgres" {
			return query
		}
		for n := 1; strings.Contains(query, "?"); n++ {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", n), 1)
		}
		return query
	}
	var id any
	err = users.QueryRowContext(ctx, bind("SELECT id FROM users WHERE username = ?"), username).Scan(&id)
	switch {
	case err == nil:
		if _, err := users.ExecContext(ctx, bind("UPDATE users SET password_hash = ? WHERE username = ?"), hash, username); err != nil {
			fmt.Fprintf(e, "Error: %s\n", sqliteMessage(err))
			return 1
		}
		fmt.Fprintf(o, "Password updated for user: %s\n", username)
	case errors.Is(err, sql.ErrNoRows):
		if _, err := users.ExecContext(ctx, bind("INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)"), username, hash, "admin"); err != nil {
			fmt.Fprintf(e, "Error: %s\n", sqliteMessage(err))
			return 1
		}
		fmt.Fprintf(o, "Created admin user: %s\n", username)
	default:
		fmt.Fprintf(e, "Error: %s\n", sqliteMessage(err))
		return 1
	}
	fmt.Fprintf(o, "Hash type: bcrypt, length: %d\n", len(hash))
	if err := users.Close(); err != nil {
		fmt.Fprintf(e, "Error: %s\n", sqliteMessage(err))
		return 1
	}
	fmt.Fprintln(o, "Done")
	return 0
}

// consoleEnv reads the environment and, for what it does not set, the
// console's env file: BETTERDESK_ENV_FILE when set (main has loaded it
// already), else the .env in the console's folder, read with the loader's
// rule (the first value of a key that is not empty).
func consoleEnv(getenv func(string) string) func(string) string {
	if getenv("BETTERDESK_ENV_FILE") != "" {
		return getenv
	}
	cfg, err := consoleconfig.Load(getenv)
	if err != nil {
		return getenv
	}
	raw, err := os.ReadFile(cfg.EnvPath)
	if err != nil {
		return getenv
	}
	file := map[string]string{}
	for _, line := range serverinfo.ParseEnvLines(serverinfo.DecodeUTF8(raw)).Entries {
		if line.Type == serverinfo.EnvPair && file[line.Key] == "" {
			file[line.Key] = jsbind.Trim(line.Value)
		}
	}
	return func(key string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return file[key]
	}
}

// firstLine reads the first line of r without its line end. ok is false
// when r has nothing at all.
func firstLine(r io.Reader) (line string, ok bool, err error) {
	reader := bufio.NewReader(io.LimitReader(r, 1<<20))
	text, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, fmt.Errorf("read the password: %w", err)
	}
	if text == "" {
		return "", false, nil
	}
	text = strings.TrimSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\r")
	// What is no UTF-8 reached the script as U+FFFD
	return serverinfo.DecodeUTF8([]byte(text)), true, nil
}

// isPostgresURL reports whether a DB_URL names PostgreSQL, as the server
// tells it (db.Open).
func isPostgresURL(dsn string) bool {
	lower := strings.ToLower(dsn)
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

// sqlitePath is the SQLite file of the server and the console: DB_URL,
// else DB_PATH, else db_v2.sqlite3 in KEYS_PATH, RUSTDESK_DIR or the data
// folder.
func sqlitePath(getenv func(string) string, opts resetOptions) string {
	if p := orElse(getenv("DB_URL"), getenv("DB_PATH")); p != "" {
		return p
	}
	dir := orElse(getenv("KEYS_PATH"), getenv("RUSTDESK_DIR"))
	if dir == "" {
		dir = orElse(getenv("DATA_DIR"), findDataDir(getenv, opts))
	}
	return filepath.Join(dir, "db_v2.sqlite3")
}

// findDataDir is the script's search for a data folder: the first of these
// that exists, else data in the working folder. The Windows names are
// looked up as they are on every system.
func findDataDir(getenv func(string) string, opts resetOptions) string {
	for _, p := range []string{
		getenv("RUSTDESK_DATA"),
		getenv("DATA_DIR"),
		`C:\BetterDeskConsole\data`,
		`C:\BetterDesk\BetterDeskConsole\data`,
		`C:\BetterDesk\data`,
		"/opt/BetterDeskConsole/data",
		"/opt/rustdesk",
		"/var/lib/rustdesk",
		`C:\RustDesk`,
		filepath.Join(opts.cwd, "data"),
	} {
		if p != "" && opts.exists(p) {
			return p
		}
	}
	return filepath.Join(opts.cwd, "data")
}

func openResetSQLite(ctx context.Context, path string) (*sql.DB, error) {
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(1)
	if _, err := sdb.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	return sdb, nil
}

// hasUsersTable reports whether the database has a users table.
func hasUsersTable(ctx context.Context, users *sql.DB, dialect string) (bool, error) {
	if dialect == "postgres" {
		var name sql.NullString
		if err := users.QueryRowContext(ctx, "SELECT to_regclass('users')::text").Scan(&name); err != nil {
			return false, err
		}
		return name.Valid, nil
	}
	var n int
	if err := users.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'").Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// migrateLikeTheServer sets the database dsn up as a server start does:
// the signal server's tables first, then the console's.
func migrateLikeTheServer(ctx context.Context, dsn string) error {
	database, err := db.Open(dsn)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		return fmt.Errorf("server schema: %w", err)
	}
	var console *store.DB
	if pg, ok := database.(*db.PostgresDB); ok {
		console = store.OpenPostgres(pg.Pool())
	} else if console, err = store.OpenSQLite(dsn); err != nil {
		return err
	}
	defer console.Close()
	return console.Migrate(ctx)
}

// sqliteCode is the " (26)" the SQLite driver puts after a message;
// better-sqlite3 gave the message alone.
var sqliteCode = regexp.MustCompile(` \(\d+\)$`)

func sqliteMessage(err error) string { return sqliteCode.ReplaceAllString(err.Error(), "") }

func orElse(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
