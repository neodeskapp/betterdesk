package auditlog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// setup creates audit_log from the console's schema file. On PostgreSQL
// the table points at users, so a small users table with the accounts 5 and
// 7 comes first, as in testdata/gen.js.
func setup(t *testing.T, db *store.DB) *Log {
	t.Helper()
	ctx := context.Background()
	if db.IsPostgres() {
		for _, stmt := range []string{
			`CREATE TABLE users (id SERIAL PRIMARY KEY, username TEXT NOT NULL)`,
			`INSERT INTO users (id, username) VALUES (5, 'five'), (7, 'seven')`,
		} {
			if _, err := db.Exec(ctx, stmt); err != nil {
				t.Fatalf("create users: %v", err)
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "store", "schema", db.Dialect().String(), "audit_log.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	for _, stmt := range strings.Split(strings.Join(lines, "\n"), ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("create audit_log: %v", err)
		}
	}
	return New(db)
}

// storedRow is one row of audit_log, column by column: the type each value
// has in the database, and text columns as the hex of their bytes.
type storedRow struct {
	Error       string  `json:"error"`
	UserType    string  `json:"user_type"`
	UserID      *string `json:"user_id"`
	Action      string  `json:"action"`
	DetailsType string  `json:"details_type"`
	DetailsHex  *string `json:"details_hex"`
	IPType      string  `json:"ip_type"`
	IPHex       *string `json:"ip_hex"`
	CreatedType string  `json:"created_type"`
	CreatedAt   string  `json:"created_at"`
}

func (r storedRow) details(t *testing.T) *string { return unhex(t, r.DetailsHex) }
func (r storedRow) ip(t *testing.T) *string      { return unhex(t, r.IPHex) }

func unhex(t *testing.T, h *string) *string {
	t.Helper()
	if h == nil {
		return nil
	}
	raw, err := hex.DecodeString(*h)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	return &s
}

func show(s *string) string {
	if s == nil {
		return "NULL"
	}
	if len(*s) > 80 {
		return "'" + (*s)[:80] + "...'"
	}
	return "'" + *s + "'"
}

var sqliteTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

// rowsAfter reads the rows with an id above after. created_at is checked
// here: on SQLite it is the text "YYYY-MM-DD HH:MM:SS" in UTC, on
// PostgreSQL a timestamptz, and on both the time of the insert.
func rowsAfter(t *testing.T, db *store.DB, after int64) []storedRow {
	t.Helper()
	ctx := context.Background()
	query := `SELECT typeof(user_id), CAST(user_id AS TEXT), action,
		typeof(details), lower(hex(CAST(details AS BLOB))),
		typeof(ip_address), lower(hex(CAST(ip_address AS BLOB))),
		typeof(created_at), created_at, 1
		FROM audit_log WHERE id > ? ORDER BY id`
	if db.IsPostgres() {
		query = `SELECT CASE WHEN user_id IS NULL THEN 'null' ELSE pg_typeof(user_id)::text END, user_id::text, action,
			CASE WHEN details IS NULL THEN 'null' ELSE 'text' END, encode(convert_to(details, 'UTF8'), 'hex'),
			CASE WHEN ip_address IS NULL THEN 'null' ELSE 'text' END, encode(convert_to(ip_address, 'UTF8'), 'hex'),
			pg_typeof(created_at)::text, created_at::text,
			CASE WHEN abs(extract(epoch FROM (now() - created_at))) < 60 THEN 1 ELSE 0 END
			FROM audit_log WHERE id > ? ORDER BY id`
	}
	rows, err := db.Query(ctx, query, after)
	if err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	defer rows.Close()
	var out []storedRow
	for rows.Next() {
		var r storedRow
		var userID, detailsHex, ipHex sql.NullString
		var createdNow int
		if err := rows.Scan(&r.UserType, &userID, &r.Action, &r.DetailsType, &detailsHex, &r.IPType, &ipHex,
			&r.CreatedType, &r.CreatedAt, &createdNow); err != nil {
			t.Fatalf("read audit_log: %v", err)
		}
		if userID.Valid {
			r.UserID = &userID.String
		}
		if r.DetailsType != "null" {
			r.DetailsHex = &detailsHex.String
		}
		if r.IPType != "null" {
			r.IPHex = &ipHex.String
		}
		if db.IsPostgres() {
			if r.CreatedType != "timestamp with time zone" || createdNow != 1 {
				t.Errorf("created_at is %s %q, want a timestamptz of now", r.CreatedType, r.CreatedAt)
			}
		} else {
			at, err := time.Parse("2006-01-02 15:04:05", r.CreatedAt)
			if r.CreatedType != "text" || !sqliteTime.MatchString(r.CreatedAt) || err != nil || time.Since(at).Abs() > time.Minute {
				t.Errorf("created_at is %s %q, want the text YYYY-MM-DD HH:MM:SS of now in UTC", r.CreatedType, r.CreatedAt)
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	return out
}

func lastID(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(), `SELECT COALESCE(MAX(id), 0) FROM audit_log`).Scan(&id); err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	return id
}

// oneRow runs write and returns the single row it added.
func oneRow(t *testing.T, db *store.DB, write func() error) storedRow {
	t.Helper()
	before := lastID(t, db)
	if err := write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	rows := rowsAfter(t, db, before)
	if len(rows) != 1 {
		t.Fatalf("the write added %d rows, want 1", len(rows))
	}
	return rows[0]
}

type rowCase struct {
	Call string `json:"call"`
	Go   struct {
		User    *int64  `json:"user"`
		Action  string  `json:"action"`
		Details *string `json:"details"`
		IP      *string `json:"ip"`
	} `json:"go"`
	SQLite   storedRow `json:"sqlite"`
	Postgres storedRow `json:"postgres"`
}

// TestWriteEntryStoresWhatNodeStores replays testdata/rows.json: for each
// call of the Node console's logAction, on each database, the row Node
// left (or the error it got) and the Entry that stands for the same
// arguments in Go.
func TestWriteEntryStoresWhatNodeStores(t *testing.T) {
	var file struct {
		Cases []rowCase `json:"cases"`
	}
	loadJSON(t, "rows.json", &file)
	if len(file.Cases) < 20 {
		t.Fatalf("%d cases, want at least 20", len(file.Cases))
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		log := setup(t, db)
		failures := 0
		for _, c := range file.Cases {
			want := c.SQLite
			if db.IsPostgres() {
				want = c.Postgres
			}
			if !db.IsPostgres() && want.Error == "" && !sqliteTime.MatchString(want.CreatedAt) {
				t.Errorf("%s: Node's created_at is %q, want YYYY-MM-DD HH:MM:SS", c.Call, want.CreatedAt)
			}
			before := lastID(t, db)
			err := log.WriteEntry(context.Background(), Entry{UserID: c.Go.User, Action: c.Go.Action, Details: c.Go.Details, IP: c.Go.IP})
			got := rowsAfter(t, db, before)
			if want.Error != "" {
				failures++
				var pgErr *pgconn.PgError
				if err == nil || !errors.As(err, &pgErr) || pgErr.Code != want.Error {
					t.Errorf("%s: error %v, Node got the database error %s", c.Call, err, want.Error)
				}
				if len(got) != 0 {
					t.Errorf("%s: %d rows written, Node wrote none", c.Call, len(got))
				}
				continue
			}
			if err != nil {
				t.Errorf("%s: %v", c.Call, err)
				continue
			}
			if len(got) != 1 {
				t.Errorf("%s: %d rows written, want 1", c.Call, len(got))
				continue
			}
			row := got[0]
			if row.UserType != want.UserType || show(row.UserID) != show(want.UserID) {
				t.Errorf("%s: user_id is %s %s, Node stored %s %s", c.Call, row.UserType, show(row.UserID), want.UserType, show(want.UserID))
			}
			if row.Action != want.Action {
				t.Errorf("%s: action is %q, Node stored %q", c.Call, row.Action, want.Action)
			}
			if row.DetailsType != want.DetailsType || show(row.details(t)) != show(want.details(t)) || show(row.DetailsHex) != show(want.DetailsHex) {
				t.Errorf("%s: details is %s %s, Node stored %s %s", c.Call, row.DetailsType, show(row.details(t)), want.DetailsType, show(want.details(t)))
			}
			if row.IPType != want.IPType || show(row.IPHex) != show(want.IPHex) {
				t.Errorf("%s: ip_address is %s %s, Node stored %s %s", c.Call, row.IPType, show(row.ip(t)), want.IPType, show(want.ip(t)))
			}
			if row.CreatedType != want.CreatedType {
				t.Errorf("%s: created_at is a %s, Node stored a %s", c.Call, row.CreatedType, want.CreatedType)
			}
		}
		// The three calls PostgreSQL refuses: user 0, a user that does not
		// exist, a NUL character in the text. SQLite takes them all.
		if want := map[bool]int{false: 0, true: 3}[db.IsPostgres()]; failures != want {
			t.Errorf("%d calls failed in Node, want %d", failures, want)
		}
	})
}

func TestWrite(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		log := setup(t, db)
		ctx := context.Background()

		row := oneRow(t, db, func() error {
			return log.Write(ctx, 5, "login_failed", "Username: alice; token=abc\r\nnext", "203.0.113.7")
		})
		if row.UserType != "integer" || show(row.UserID) != "'5'" {
			t.Errorf("user_id is %s %s, want the integer 5", row.UserType, show(row.UserID))
		}
		if row.Action != "login_failed" {
			t.Errorf("action is %q", row.Action)
		}
		if got := show(row.details(t)); got != `'Username: a***; token=***'` {
			t.Errorf("details is %s, want the redacted text", got)
		}
		if got := show(row.ip(t)); got != "'203.0.113.7'" {
			t.Errorf("ip_address is %s", got)
		}

		// User 0 is no account: NULL. Empty details and an empty address
		// are stored as empty text, not as NULL.
		row = oneRow(t, db, func() error { return log.Write(ctx, 0, "help_request", "", "") })
		if row.UserType != "null" || row.UserID != nil {
			t.Errorf("user_id is %s %s, want NULL", row.UserType, show(row.UserID))
		}
		if row.DetailsType != "text" || show(row.details(t)) != "''" {
			t.Errorf("details is %s %s, want the empty text", row.DetailsType, show(row.details(t)))
		}
		if row.IPType != "text" || show(row.ip(t)) != "''" {
			t.Errorf("ip_address is %s %s, want the empty text", row.IPType, show(row.ip(t)))
		}

		// Only details are redacted.
		row = oneRow(t, db, func() error { return log.Write(ctx, 7, "token=abc", "token=abc", "User: bob") })
		if row.Action != "token=abc" || show(row.details(t)) != "'token=***'" || show(row.ip(t)) != "'User: bob'" {
			t.Errorf("stored %q, %s, %s", row.Action, show(row.details(t)), show(row.ip(t)))
		}
	})
}

func TestWriteEntryNulls(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		log := setup(t, db)
		ctx := context.Background()

		row := oneRow(t, db, func() error { return log.WriteEntry(ctx, Entry{Action: "a"}) })
		if row.UserType != "null" || row.DetailsType != "null" || row.IPType != "null" {
			t.Errorf("stored %s, %s, %s; want three NULLs", row.UserType, row.DetailsType, row.IPType)
		}

		user, details, ip := int64(7), "", ""
		row = oneRow(t, db, func() error {
			return log.WriteEntry(ctx, Entry{UserID: &user, Action: "a", Details: &details, IP: &ip})
		})
		if show(row.UserID) != "'7'" || row.DetailsType != "text" || show(row.details(t)) != "''" || row.IPType != "text" || show(row.ip(t)) != "''" {
			t.Errorf("stored %s, %s %s, %s %s; want 7 and two empty texts", show(row.UserID), row.DetailsType, show(row.details(t)), row.IPType, show(row.ip(t)))
		}

		// A pointer to 0 is the number 0, not "no account".
		zero := int64(0)
		before := lastID(t, db)
		err := log.WriteEntry(ctx, Entry{UserID: &zero, Action: "a"})
		rows := rowsAfter(t, db, before)
		if db.IsPostgres() {
			if err == nil || len(rows) != 0 {
				t.Errorf("user 0 on PostgreSQL: error %v, %d rows; want the foreign key error", err, len(rows))
			}
		} else if err != nil || len(rows) != 1 || rows[0].UserType != "integer" || show(rows[0].UserID) != "'0'" {
			t.Errorf("user 0 on SQLite: error %v, rows %+v; want the integer 0", err, rows)
		}
	})
}

// Bytes that are not UTF-8 are stored as U+FFFD. PostgreSQL would refuse
// the row otherwise.
func TestWriteEntryInvalidUTF8(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		log := setup(t, db)
		details, ip := "caf\xe9 token=a\xffb", "\xff::1"
		row := oneRow(t, db, func() error {
			return log.WriteEntry(context.Background(), Entry{Action: "act\xffion", Details: &details, IP: &ip})
		})
		if row.Action != "act\uFFFDion" || show(row.details(t)) != "'caf\uFFFD token=***'" || show(row.ip(t)) != "'\uFFFD::1'" {
			t.Errorf("stored %q, %s, %s", row.Action, show(row.details(t)), show(row.ip(t)))
		}
	})
}

// A failed write comes back as an error that names the action and not the
// details.
func TestWriteErrors(t *testing.T) {
	const secret = "token=hunter2 Username: alice"
	check := func(t *testing.T, name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: no error", name)
			return
		}
		if !strings.HasPrefix(err.Error(), `write audit entry "device_deleted": `) {
			t.Errorf("%s: error %q does not say what was being done", name, err)
		}
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "alice") {
			t.Errorf("%s: error %q holds the details", name, err)
		}
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		log := New(db)
		err := log.Write(ctx, 5, "device_deleted", secret, "::1")
		check(t, "missing table", err)
		var pgErr *pgconn.PgError
		if db.IsPostgres() && (!errors.As(err, &pgErr) || pgErr.Code != "42P01") {
			t.Errorf("missing table: the database error is not wrapped: %v", err)
		}

		log = setup(t, db)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		err = log.Write(cancelled, 5, "device_deleted", secret, "::1")
		check(t, "cancelled", err)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled: %v does not wrap context.Canceled", err)
		}

		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		check(t, "closed database", log.Write(ctx, 5, "device_deleted", secret, "::1"))
	})

	check(t, "no database", New(nil).Write(context.Background(), 5, "device_deleted", secret, "::1"))
	var none *Log
	check(t, "nil log", none.Write(context.Background(), 5, "device_deleted", secret, "::1"))
}
