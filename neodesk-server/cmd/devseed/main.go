// Command devseed fills a local NeoDesk console with fake data so UI
// work and test runs have something to show. It is a development tool and
// not part of the server binary. Never run it against a real server.
//
//	go run ./cmd/devseed demo --admin-password <password>
//	go run ./cmd/devseed many-devices --count 5000
//	go run ./cmd/devseed heartbeats
//	go run ./cmd/devseed accounts <file with the password>
//
// It runs where the console runs: it writes into the console's database
// (the SQLite file, or PostgreSQL) and signs in to the console's API.
//
// demo, many-devices and accounts write only into a database that carries
// the test stack's marker: the row ('devseed_test_stack', '1') in
// server_config, the table the Go server keeps its settings in and that
// no route lists. The test stack (.ui-rework/go/stack/instance.sh) writes
// it before it seeds; a real install never has it, so the commands refuse
// a live database. demo asks it of a separate auth.db as well. For
// a console in Docker, build it for Linux and copy it into the container:
//
//	cd neodesk-server
//	CGO_ENABLED=0 GOOS=linux go build -o devseed ./cmd/devseed
//	docker cp devseed neodesk:/tmp/devseed
//	docker compose exec -u neodesk neodesk /tmp/devseed demo --admin-password <password>
//	docker compose exec -d -u neodesk neodesk /tmp/devseed heartbeats
//
// The database is the one the server uses: --go-db (a file or a
// postgres:// address), else DB_URL or DATABASE_URL when DB_TYPE is
// postgres (or DATABASE_URL is a PostgreSQL address and DB_TYPE is unset),
// else DB_PATH, else /opt/rustdesk/db_v2.sqlite3.
//
// demo: devices, help requests, pending enrollments, tickets, chat,
// inventory, activity, billing sessions and device history go straight
// into the database. Users, folders, groups, tags, organizations,
// policies, billing packages, contracts and saved reports go through the
// console API, so they are created the normal way. Running it again
// replaces the demo rows instead of adding more. Flags:
//
//	--admin-password  the admin's password (default ADMIN_PASSWORD)
//	--admin-user      the admin's name (default admin)
//	--cookie-file     a Playwright storage state file whose session is used
//	                  instead of signing in (each account allows one session,
//	                  so signing in ends the one a browser has)
//	--panel           the console (default http://127.0.0.1:$PORT, PORT 5000)
//	--go-db           the database (see above)
//	--auth-db         an older install's separate user database (default
//	                  AUTH_DB_PATH or /app/data/auth.db when it exists)
//	--online-file     where the ids for heartbeats go (default
//	                  /app/data/dev-online-ids.json)
//	--now             the time to seed at, milliseconds since 1970 or RFC
//	                  3339 (default now); the same time gives the same rows
//
// many-devices: devices for performance checks of the device list, ids
// 950000000 and up with host names "perf-...", so --remove deletes exactly
// them. Flags --count (default 5000, at most 20000), --remove, --go-db,
// --now.
//
// heartbeats: keeps the demo's online devices online with RustDesk
// RegisterPeer heartbeats over UDP to the signal server
// (SIGNAL_HOST:SIGNAL_PORT, default 127.0.0.1:21116), every 5 seconds,
// until stopped. The argument is the ids file (default
// /app/data/dev-online-ids.json).
//
// accounts: one account per role (e2e_admin, e2e_super_admin, ...) with
// the password in the given file, and three tokens issued by the admin
// account (DEFAULT_ADMIN_USERNAME, default admin) in files next to it: the
// device tokens device_token for 900100001 and device_token_other for
// 900100002, as the console issues them for a device it admitted, and
// operator_token, the admin's own token for the operator routes of the
// device API, as the console issues it when a desktop client signs in as
// an operator. Accounts and token files that exist are left alone; a token
// is never printed. With --device-tokens-only it makes the tokens only.
// --now sets the time the tokens count from.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "devseed:", err)
		os.Exit(1)
	}
}

const usage = `usage: devseed <command> [flags]

  demo           the demo data (devices, users, organizations, tickets, ...)
  many-devices   devices for performance checks (--count n, --remove)
  heartbeats     keep the demo's online devices online
  accounts       one account per role and two device tokens

"go doc ./cmd/devseed" describes each command and its flags.`

func run(ctx context.Context, args []string, getenv func(string) string, out, errOut io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(errOut, usage)
		return errors.New("no command")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "demo":
		return runDemo(ctx, rest, getenv, out, errOut)
	case "many-devices":
		return runManyDevices(ctx, rest, getenv, out)
	case "heartbeats":
		return runHeartbeats(ctx, rest, getenv, out)
	case "accounts":
		return runAccounts(ctx, rest, getenv, out)
	case "help", "-h", "--help":
		fmt.Fprintln(out, usage)
		return nil
	}
	fmt.Fprintln(errOut, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

// flags reads --name value pairs and --name switches. A name given twice
// takes its first value; a name followed by nothing or by an empty value
// keeps the default.
type flags struct {
	values   map[string]string
	switches map[string]bool
	rest     []string
}

func parseFlags(args []string, valued, switches []string) (*flags, error) {
	f := &flags{values: map[string]string{}, switches: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, isFlag := strings.CutPrefix(a, "--")
		if !isFlag {
			if name, isFlag = strings.CutPrefix(a, "-"); !isFlag || name == "" {
				f.rest = append(f.rest, a)
				continue
			}
		}
		name, inline, hasInline := strings.Cut(name, "=")
		switch {
		case slices.Contains(switches, name):
			f.switches[name] = true
		case slices.Contains(valued, name):
			value := inline
			if !hasInline {
				if i+1 < len(args) {
					value = args[i+1]
					i++
				}
			}
			if _, set := f.values[name]; !set && value != "" {
				f.values[name] = value
			}
		default:
			return nil, fmt.Errorf("unknown flag --%s", name)
		}
	}
	return f, nil
}

func (f *flags) get(name, fallback string) string {
	if v, ok := f.values[name]; ok {
		return v
	}
	return fallback
}

// seedTime reads --now: milliseconds since 1970 or RFC 3339. Empty is the
// current time.
func seedTime(text string) (time.Time, error) {
	if text == "" {
		return time.Now(), nil
	}
	if ms, err := strconv.ParseInt(text, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("--now %q is neither milliseconds since 1970 nor RFC 3339", text)
	}
	return t, nil
}

var postgresURL = regexp.MustCompile(`(?i)^postgres(ql)?://`)

// databaseFrom is the database the server uses, by the rules in the
// package comment.
func databaseFrom(flag string, getenv func(string) string) string {
	if flag != "" {
		return flag
	}
	dbType := strings.ToLower(getenv("DB_TYPE"))
	if dbType == "" && postgresURL.MatchString(getenv("DATABASE_URL")) {
		dbType = "postgres"
	}
	if dbType == "postgres" || dbType == "postgresql" {
		if u := getenv("DB_URL"); postgresURL.MatchString(u) {
			return u
		}
		return getenv("DATABASE_URL")
	}
	if p := getenv("DB_PATH"); p != "" {
		return p
	}
	return "/opt/rustdesk/db_v2.sqlite3"
}

// database is an open console database.
type database struct {
	*store.DB
	pool *pgxpool.Pool
	// label names it in output: the file, or "postgres" (an address
	// can hold a password).
	label string
}

// openDatabase opens a SQLite file or a postgres:// address. A SQLite file
// that is not there is refused instead of created.
func openDatabase(ctx context.Context, dsn string) (*database, error) {
	if postgresURL.MatchString(dsn) {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, fmt.Errorf("connect to postgres: %w", err)
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			return nil, fmt.Errorf("connect to postgres: %w", err)
		}
		return &database{DB: store.OpenPostgres(pool), pool: pool, label: "postgres"}, nil
	}
	if _, err := os.Stat(dsn); err != nil {
		return nil, fmt.Errorf("open %s: %w", dsn, err)
	}
	db, err := store.OpenSQLite(dsn)
	if err != nil {
		return nil, err
	}
	return &database{DB: db, label: dsn}, nil
}

// The test stack's marker: a server_config row the stack writes before it
// seeds.
const (
	markerKey   = "devseed_test_stack"
	markerValue = "1"
)

// requireMarker refuses a database without the test stack's marker.
func requireMarker(ctx context.Context, d *database) error {
	var value string
	err := d.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, markerKey).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows) || err == nil && value != markerValue:
		return fmt.Errorf("refusing to seed %s: it has no test stack marker (server_config key %q with the value %q), "+
			"which the test stack writes before it seeds; devseed is for test stacks only", d.label, markerKey, markerValue)
	case err != nil:
		return fmt.Errorf("refusing to seed %s: cannot read the test stack marker (server_config key %q): %w", d.label, markerKey, err)
	}
	return nil
}

func (d *database) Close() {
	_ = d.DB.Close()
	if d.pool != nil {
		d.pool.Close()
	}
}
