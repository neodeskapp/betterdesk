package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

var ctx = context.Background()

// columns lists the table's columns with their types.
func columns(t *testing.T, db *store.DB) map[string]string {
	t.Helper()
	query := db.Pick(
		`SELECT name, type || CASE WHEN "notnull" = 1 THEN ' NOT NULL' ELSE '' END FROM pragma_table_info('panel_sessions')`,
		`SELECT column_name, data_type || CASE WHEN is_nullable = 'NO' THEN ' NOT NULL' ELSE '' END
		 FROM information_schema.columns WHERE table_name = 'panel_sessions' AND table_schema = current_schema()`)
	rows, err := db.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]string{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatal(err)
		}
		cols[name] = kind
	}
	return cols
}

func indexes(t *testing.T, db *store.DB) []string {
	t.Helper()
	query := db.Pick(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'panel_sessions' AND name LIKE 'idx_%'`,
		`SELECT indexname FROM pg_indexes WHERE tablename = 'panel_sessions' AND schemaname = current_schema() AND indexname LIKE 'idx_%'`)
	rows, err := db.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func wantSchema(db *store.DB) map[string]string {
	if db.IsPostgres() {
		return map[string]string{
			"sid": "text NOT NULL", "session_json": "text NOT NULL",
			"expires_at": "timestamp with time zone NOT NULL", "updated_at": "timestamp with time zone NOT NULL",
			"user_id": "integer", "username": "text", "ip_address": "text", "user_agent": "text",
			"client_fingerprint": "text", "created_at": "bigint", "last_activity_at": "bigint",
			"ended_at": "bigint", "end_reason": "text", "is_active": "boolean NOT NULL",
		}
	}
	return map[string]string{
		"sid": "TEXT", "session_json": "TEXT NOT NULL", "expires_at": "INTEGER NOT NULL", "updated_at": "TEXT NOT NULL",
		"user_id": "INTEGER", "username": "TEXT", "ip_address": "TEXT", "user_agent": "TEXT",
		"client_fingerprint": "TEXT", "created_at": "INTEGER", "last_activity_at": "INTEGER",
		"ended_at": "INTEGER", "end_reason": "TEXT", "is_active": "INTEGER NOT NULL",
	}
}

func checkSchema(t *testing.T, db *store.DB) {
	t.Helper()
	got, want := columns(t, db), wantSchema(db)
	if len(got) != len(want) {
		t.Errorf("columns: got %v, want %v", got, want)
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("column %s: got %q, want %q", name, got[name], kind)
		}
	}
	wantIdx := []string{"idx_panel_sessions_active", "idx_panel_sessions_expires", "idx_panel_sessions_user_active"}
	if got := indexes(t, db); strings.Join(got, ",") != strings.Join(wantIdx, ",") {
		t.Errorf("indexes: got %v, want %v", got, wantIdx)
	}
}

func TestSchemaOnEmptyDatabase(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		newFixture(t, db)
		checkSchema(t, db)
		// A second start changes nothing and fails on nothing
		newFixture(t, db)
		checkSchema(t, db)
	})
}

func TestSchemaUpgradesTheFirstTable(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		// The table as the first version of the store created it, with a row
		create := db.Pick(
			`CREATE TABLE panel_sessions (sid TEXT PRIMARY KEY, session_json TEXT NOT NULL, expires_at INTEGER NOT NULL, updated_at TEXT NOT NULL DEFAULT (datetime('now')))`,
			`CREATE TABLE panel_sessions (sid TEXT PRIMARY KEY, session_json TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
		if _, err := db.Exec(ctx, create); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(time.Hour).UnixMilli()
		insert := `INSERT INTO panel_sessions (sid, session_json, expires_at) VALUES (?, ?, ` + db.Pick("?", "to_timestamp(? / 1000.0)") + `)`
		if _, err := db.Exec(ctx, insert, "old-sid", `{"cookie":{"originalMaxAge":1000,"expires":null},"k":"v"}`, future); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, db)
		checkSchema(t, db)
		row := f.row("old-sid")
		if row == nil || !row.active {
			t.Fatalf("the old row is not active after the upgrade: %+v", row)
		}
		text, found, _, err := f.m.table.get(ctx, "old-sid")
		if err != nil || !found || !strings.Contains(text, `"k":"v"`) {
			t.Fatalf("get(old-sid) = %q, %v, %v", text, found, err)
		}
	})
}

// rec is a record for user 7 that expires in a minute.
func (f *fixture) rec(userID int64, at int64) record {
	return record{
		json:           fmt.Sprintf(`{"cookie":{"originalMaxAge":60000,"expires":null},"userId":%d}`, userID),
		expiresAt:      f.clock.Now().UnixMilli() + 60_000,
		createdAt:      at,
		lastActivityAt: at,
		userID:         userID,
		username:       fmt.Sprintf("user%d", userID),
	}
}

func (f *fixture) mustSet(sid string, rec record) {
	f.t.Helper()
	if err := f.m.table.set(ctx, sid, rec); err != nil {
		f.t.Fatalf("set %s: %v", sid, err)
	}
}

func (f *fixture) found(sid string) bool {
	f.t.Helper()
	_, found, _, err := f.m.table.get(ctx, sid)
	if err != nil {
		f.t.Fatalf("get %s: %v", sid, err)
	}
	return found
}

func TestStoreSetGetTouchDestroy(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		const text = `{"cookie":{"originalMaxAge":60000,"expires":"2030-01-01T00:00:00.000Z"},"userId":7,"user":{"id":7,"username":"alice","role":"operator"}}`
		if f.found("sid-1") {
			t.Fatal("a session before any was stored")
		}
		f.mustSet("sid-1", record{
			json: text, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now,
			userID: int64(7), username: "alice", ip: "203.0.113.10", userAgent: "TestAgent/1.0", fingerprint: "fp_12345678",
		})
		got, found, _, err := f.m.table.get(ctx, "sid-1")
		if err != nil || !found || got != text {
			t.Fatalf("get = %q, %v, %v", got, found, err)
		}
		row := f.row("sid-1")
		if !row.active || row.userID.Int64 != 7 || row.username.String != "alice" || row.ip.String != "203.0.113.10" ||
			row.userAgent.String != "TestAgent/1.0" || row.fp.String != "fp_12345678" ||
			row.createdAt.Int64 != now || row.lastActivityAt.Int64 != now || row.expiresAt != now+60_000 ||
			row.endedAt.Valid || row.reason.Valid {
			t.Fatalf("row after set: %+v", row)
		}

		// touch moves the expiry and the last activity and leaves the JSON
		if err := f.m.table.touch(ctx, "sid-1", now+120_000, now+5); err != nil {
			t.Fatal(err)
		}
		row = f.row("sid-1")
		if row.expiresAt != now+120_000 || row.lastActivityAt.Int64 != now+5 || row.json != text || row.createdAt.Int64 != now {
			t.Fatalf("row after touch: %+v", row)
		}

		if err := f.m.table.destroy(ctx, "sid-1"); err != nil {
			t.Fatal(err)
		}
		if f.found("sid-1") {
			t.Fatal("the session is still found after destroy")
		}
		row = f.row("sid-1")
		if row == nil || row.active || row.json != "{}" || row.reason.String != ReasonDestroyed || row.endedAt.Int64 != now || row.userID.Int64 != 7 {
			t.Fatalf("row after destroy: %+v; the row stays as history", row)
		}

		// touch and set do not bring an ended session back
		if err := f.m.table.touch(ctx, "sid-1", now+999_000, now+9); err != nil {
			t.Fatal(err)
		}
		if row = f.row("sid-1"); row.expiresAt != now+120_000 {
			t.Fatalf("touch moved an ended row: %+v", row)
		}
		f.mustSet("sid-1", record{json: text, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now, userID: int64(7)})
		if row = f.row("sid-1"); row.active || row.reason.String != ReasonDestroyed || !row.endedAt.Valid {
			t.Fatalf("set revived an ended row: %+v", row)
		}
		if f.found("sid-1") {
			t.Fatal("an ended session is found again after set")
		}
	})
}

func TestStoreSetOnExistingRow(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("sid", record{json: `{"cookie":{},"n":1}`, expiresAt: now + 1000, createdAt: now, lastActivityAt: now,
			userID: int64(7), username: "alice", ip: "1.1.1.1", userAgent: "ua-1", fingerprint: "fp_11111111"})
		// The second write has no account and no client details
		f.mustSet("sid", record{json: `{"cookie":{},"n":2}`, expiresAt: now + 2000, createdAt: now + 50, lastActivityAt: now + 60})
		row := f.row("sid")
		if row.json != `{"cookie":{},"n":2}` || row.expiresAt != now+2000 || row.lastActivityAt.Int64 != now+60 {
			t.Errorf("JSON, expiry and last activity are replaced: %+v", row)
		}
		if row.createdAt.Int64 != now {
			t.Errorf("created_at changed to %d, want it kept at %d", row.createdAt.Int64, now)
		}
		if row.userID.Int64 != 7 || row.username.String != "alice" || row.ip.String != "1.1.1.1" || row.userAgent.String != "ua-1" || row.fp.String != "fp_11111111" {
			t.Errorf("empty values replaced stored ones: %+v", row)
		}
		// Values that are not empty replace
		f.mustSet("sid", record{json: `{"cookie":{}}`, expiresAt: now + 3000, createdAt: now, lastActivityAt: now,
			userID: int64(8), username: "bob", ip: "2.2.2.2", userAgent: "ua-2", fingerprint: "fp_22222222"})
		row = f.row("sid")
		if row.userID.Int64 != 8 || row.username.String != "bob" || row.ip.String != "2.2.2.2" || row.userAgent.String != "ua-2" || row.fp.String != "fp_22222222" {
			t.Errorf("new values did not replace: %+v", row)
		}
	})
}

func TestStoreAnonymousRow(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("anon", record{json: `{"cookie":{}}`, expiresAt: now + 1000, createdAt: now, lastActivityAt: now})
		row := f.row("anon")
		if row.userID.Valid || row.username.Valid || row.ip.Valid || row.userAgent.Valid || row.fp.Valid {
			t.Fatalf("empty values must be NULL: %+v", row)
		}
	})
}

func TestStoreGetExpiry(t *testing.T) {
	const idle, maxAge = int64(30 * 60 * 1000), int64(24 * 60 * 60 * 1000)
	cases := []struct {
		name string
		// offsets from now, in ms
		created, last, expires int64
		found                  bool
		reason                 string
	}{
		{"fresh", -1000, -1000, 60_000, true, ""},
		{"idle exactly the timeout is still alive", -idle, -idle, 60_000, true, ""},
		{"idle one millisecond over", -idle - 1, -idle - 1, 60_000, false, ReasonIdle},
		{"absolute lifetime exactly reached is still alive", -maxAge, -1000, 60_000, true, ""},
		{"absolute lifetime one millisecond over", -maxAge - 1, -1000, 60_000, false, ReasonMaxAge},
		{"absolute lifetime wins over idle", -maxAge - 1, -maxAge - 1, 60_000, false, ReasonMaxAge},
		{"expiry reached", -1000, -1000, 0, false, ReasonIdle},
		{"expiry in the past", -1000, -1000, -5000, false, ReasonIdle},
		{"expiry one millisecond ahead", -1000, -1000, 1, true, ""},
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newFixture(t, db)
				now := f.clock.Now().UnixMilli()
				sid := fmt.Sprintf("sid-%d", i)
				f.mustSet(sid, record{json: `{"cookie":{},"keep":"me"}`, expiresAt: now + tc.expires,
					createdAt: now + tc.created, lastActivityAt: now + tc.last, userID: int64(9)})
				if got := f.found(sid); got != tc.found {
					t.Fatalf("found = %v, want %v", got, tc.found)
				}
				row := f.row(sid)
				if tc.found {
					if !row.active || row.reason.Valid || row.endedAt.Valid {
						t.Fatalf("a live row was ended: %+v", row)
					}
					return
				}
				if row.active || row.reason.String != tc.reason || row.endedAt.Int64 != now {
					t.Fatalf("row: %+v; want ended at %d with reason %q", row, now, tc.reason)
				}
				if row.json != `{"cookie":{},"keep":"me"}` {
					t.Fatalf("an expired row lost its JSON: %s", row.json)
				}
			})
		}
	})
}

func TestStoreGetWithoutTimes(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("bare", record{json: `{"cookie":{}}`, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
		f.exec(`UPDATE panel_sessions SET created_at = NULL, last_activity_at = NULL WHERE sid = ?`, "bare")
		if !f.found("bare") {
			t.Fatal("a row without times is alive until its expiry")
		}
		f.clock.Advance(61 * time.Second)
		if f.found("bare") {
			t.Fatal("a row without times ends at its expiry")
		}
		if row := f.row("bare"); row.reason.String != ReasonIdle {
			t.Fatalf("reason %q, want idle", row.reason.String)
		}
	})
}

func TestStoreGetKeepsTheFirstReason(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("sid", f.rec(7, now))
		if err := f.m.table.markEnded(ctx, "sid", ReasonLogout); err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(time.Hour)
		if err := f.m.table.markEnded(ctx, "sid", ReasonIdle); err != nil {
			t.Fatal(err)
		}
		if err := f.m.table.destroy(ctx, "sid"); err != nil {
			t.Fatal(err)
		}
		row := f.row("sid")
		if row.reason.String != ReasonLogout || row.endedAt.Int64 != now {
			t.Fatalf("row: %+v; want the first reason and time kept", row)
		}
		// An ended row without a reason reads as revoked
		gone, why := f.m.table.expired(storedRow{active: false}, now)
		if !gone || why != ReasonRevoked {
			t.Fatalf("expired = %v, %q; want revoked", gone, why)
		}
	})
}

func TestStoreEnd(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("sid", f.rec(7, now))
		if err := f.m.table.end(ctx, "sid", ReasonLogout); err != nil {
			t.Fatal(err)
		}
		row := f.row("sid")
		if row.active || row.reason.String != ReasonLogout || row.endedAt.Int64 != now || row.json != "{}" {
			t.Fatalf("row after end: %+v", row)
		}
		// Ending a session that has no row is not an error and makes none
		if err := f.m.table.end(ctx, "nobody", ReasonLogout); err != nil {
			t.Fatal(err)
		}
		if f.row("nobody") != nil {
			t.Fatal("end created a row")
		}
	})
}

// login stores a session for a user and activates it, as sign-in does.
func (f *fixture) login(sid string, userID, at int64) []string {
	f.t.Helper()
	f.mustSet(sid, f.rec(userID, at))
	replaced, err := f.m.table.activate(ctx, sid, userID, fmt.Sprintf("user%d", userID), Meta{IP: "::ffff:198.51.100.20", UserAgent: " Agent/2.0 ", Fingerprint: "fp_abcdef12"}, at)
	if err != nil {
		f.t.Fatalf("activate %s: %v", sid, err)
	}
	var sids []string
	for _, r := range replaced {
		sids = append(sids, r.sid)
	}
	return sids
}

func withLimit(n int) func(*Options) {
	return func(o *Options) { o.Policy.MaxSessionsPerUser = n }
}

func TestLimitOneSessionPerAccount(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		if got := f.login("sid-a", 42, now); len(got) != 0 {
			t.Fatalf("the first sign-in replaced %v", got)
		}
		row := f.row("sid-a")
		if row.ip.String != "198.51.100.20" || row.userAgent.String != "Agent/2.0" || row.fp.String != "fp_abcdef12" ||
			row.username.String != "user42" || row.expiresAt != now+f.m.policy.IdleTimeoutMs {
			t.Fatalf("row after activation: %+v", row)
		}
		f.clock.Advance(time.Second)
		if got := f.login("sid-b", 42, now+1000); len(got) != 1 || got[0] != "sid-a" {
			t.Fatalf("the second sign-in replaced %v, want sid-a", got)
		}
		if f.found("sid-a") || !f.found("sid-b") {
			t.Fatal("want sid-a ended and sid-b alive")
		}
		old := f.row("sid-a")
		if old.active || old.reason.String != ReasonReplaced || old.endedAt.Int64 != now+1000 {
			t.Fatalf("replaced row: %+v", old)
		}
		if !strings.Contains(old.json, `"userId":42`) {
			t.Fatalf("a replaced row keeps its JSON, got %s", old.json)
		}
		list, err := f.m.List(ctx, 42, "sid-b")
		if err != nil || len(list) != 2 {
			t.Fatalf("history: %v, %v", list, err)
		}
	})
}

func TestLimitZeroAllowsAnyNumber(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(0))
		now := f.clock.Now().UnixMilli()
		for _, sid := range []string{"u-a", "u-b", "u-c"} {
			if got := f.login(sid, 50, now); len(got) != 0 {
				t.Fatalf("%s replaced %v", sid, got)
			}
		}
		for _, sid := range []string{"u-a", "u-b", "u-c"} {
			if !f.found(sid) {
				t.Fatalf("%s was ended", sid)
			}
		}
	})
}

func TestLimitKeepsTheMostRecentlyActive(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(2))
		now := f.clock.Now().UnixMilli()
		f.login("n-old", 51, now-3000)
		f.login("n-mid", 51, now-2000)
		if got := f.login("n-new", 51, now-1000); len(got) != 1 || got[0] != "n-old" {
			t.Fatalf("replaced %v, want n-old", got)
		}
		if f.found("n-old") || !f.found("n-mid") || !f.found("n-new") {
			t.Fatal("want n-old ended, n-mid and n-new alive")
		}
		if row := f.row("n-old"); row.reason.String != ReasonReplaced {
			t.Fatalf("reason %q", row.reason.String)
		}
		// Recency is the last activity, not the sign-in order: n-mid is used,
		// then a third sign-in ends n-new
		if err := f.m.table.touch(ctx, "n-mid", now+60_000, now); err != nil {
			t.Fatal(err)
		}
		if got := f.login("n-last", 51, now); len(got) != 1 || got[0] != "n-new" {
			t.Fatalf("replaced %v, want n-new", got)
		}
	})
}

func TestLimitEndsSeveralAtOnce(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(0))
		now := f.clock.Now().UnixMilli()
		f.login("s1", 52, now-4000)
		f.login("s2", 52, now-3000)
		f.login("s3", 52, now-2000)
		// The same table under a limit of 2
		g := newFixture(t, db, withLimit(2))
		got := g.login("s4", 52, now)
		if strings.Join(got, ",") != "s2,s1" {
			t.Fatalf("replaced %v, want s2 and s1 (the least recently active)", got)
		}
		if !g.found("s3") || !g.found("s4") {
			t.Fatal("want s3 and s4 alive")
		}
	})
}

func TestLimitIsPerAccount(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.login("x-1", 60, now)
		if got := f.login("y-1", 61, now); len(got) != 0 {
			t.Fatalf("another account's sign-in replaced %v", got)
		}
		if !f.found("x-1") {
			t.Fatal("x-1 was ended by another account's sign-in")
		}
	})
}

func TestLimitIgnoresEndedSessions(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.login("a", 70, now)
		if err := f.m.table.end(ctx, "a", ReasonLogout); err != nil {
			t.Fatal(err)
		}
		if got := f.login("b", 70, now); len(got) != 0 {
			t.Fatalf("replaced %v, want none: the other session had ended", got)
		}
		if row := f.row("a"); row.reason.String != ReasonLogout {
			t.Fatalf("reason %q, want logout kept", row.reason.String)
		}
	})
}

func TestActivateRevivesItsOwnRow(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("sid", f.rec(7, now-5000))
		if err := f.m.table.markEnded(ctx, "sid", ReasonIdle); err != nil {
			t.Fatal(err)
		}
		if _, err := f.m.table.activate(ctx, "sid", 7, "alice", Meta{}, now); err != nil {
			t.Fatal(err)
		}
		row := f.row("sid")
		if !row.active || row.reason.Valid || row.endedAt.Valid || row.createdAt.Int64 != now || row.lastActivityAt.Int64 != now {
			t.Fatalf("row after activation: %+v", row)
		}
		if row.ip.Valid || row.userAgent.Valid || row.fp.Valid {
			t.Fatalf("empty client details must be NULL: %+v", row)
		}
	})
}

func TestActivateExpiryNearTheAbsoluteEnd(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		f.mustSet("sid", f.rec(7, now))
		// Signed in almost a whole lifetime ago: the absolute end comes first
		at := now - f.m.policy.MaxAgeMs + 5000
		if _, err := f.m.table.activate(ctx, "sid", 7, "alice", Meta{}, at); err != nil {
			t.Fatal(err)
		}
		if row := f.row("sid"); row.expiresAt != now+5000 {
			t.Fatalf("expires_at = now%+d ms, want now+5000", row.expiresAt-now)
		}
	})
}

func TestCleanup(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		idle, maxAge := f.m.policy.IdleTimeoutMs, f.m.policy.MaxAgeMs
		const day = int64(24 * 60 * 60 * 1000)
		set := func(sid string, userID any, created, last, expires int64) {
			f.mustSet(sid, record{json: `{"cookie":{},"keep":1}`, expiresAt: now + expires, createdAt: now + created, lastActivityAt: now + last, userID: userID})
		}
		set("live", int64(1), -1000, -1000, 60_000)
		set("expired", int64(1), -1000, -1000, -60_000)
		set("idle", int64(1), -1000, -idle-60_000, 60_000)
		set("old", int64(1), -maxAge-60_000, -1000, 60_000)
		set("old-and-idle", int64(1), -maxAge-60_000, -idle-60_000, -60_000)
		set("anon-live", nil, -1000, -1000, 60_000)
		set("anon-expired-recently", nil, -1000, -1000, -day+60_000)
		set("anon-expired-long-ago", nil, -1000, -1000, -day-60_000)
		set("user-expired-long-ago", int64(1), -1000, -1000, -day-60_000)
		set("logged-out", int64(1), -1000, -1000, -60_000)
		set("logged-out-long-ago", int64(1), -91*day, -91*day, -91*day)
		set("logged-out-89-days-ago", int64(1), -89*day, -89*day, -89*day)
		if err := f.m.table.end(ctx, "logged-out", ReasonLogout); err != nil {
			t.Fatal(err)
		}
		ended := f.row("logged-out").endedAt.Int64
		f.exec(`UPDATE panel_sessions SET is_active = `+db.Pick("0", "FALSE")+`, end_reason = 'logout', ended_at = ? WHERE sid = ?`, now-91*day, "logged-out-long-ago")
		f.exec(`UPDATE panel_sessions SET is_active = `+db.Pick("0", "FALSE")+`, end_reason = 'logout', ended_at = ? WHERE sid = ?`, now-89*day, "logged-out-89-days-ago")

		f.clock.Advance(time.Second)
		if err := f.m.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
		for _, sid := range []string{"live", "anon-live"} {
			if row := f.row(sid); row == nil || !row.active {
				t.Errorf("%s: %+v, want active", sid, row)
			}
		}
		// A row past the absolute lifetime ends as max_age, the others
		// past a limit as idle (BUGS.md S8)
		for sid, reason := range map[string]string{
			"expired": ReasonIdle, "idle": ReasonIdle, "old": ReasonMaxAge, "old-and-idle": ReasonMaxAge,
			"anon-expired-recently": ReasonIdle, "user-expired-long-ago": ReasonIdle,
		} {
			row := f.row(sid)
			if row == nil || row.active || row.reason.String != reason || row.endedAt.Int64 != now+1000 || row.json != `{"cookie":{},"keep":1}` {
				t.Errorf("%s: %+v, want ended as %s at the sweep with its JSON kept", sid, row, reason)
			}
		}
		if f.row("anon-expired-long-ago") != nil {
			t.Error("an anonymous row a day past its expiry was kept")
		}
		if row := f.row("logged-out"); row.reason.String != ReasonLogout || row.endedAt.Int64 != ended {
			t.Errorf("the sweep changed an ended row: %+v", row)
		}
		// The row of an account goes 90 days after its session ended
		if f.row("logged-out-long-ago") != nil {
			t.Error("the row of a session that ended 91 days ago was kept")
		}
		if f.row("logged-out-89-days-ago") == nil {
			t.Error("the row of a session that ended 89 days ago was deleted")
		}
	})
}

func TestRunCleanupStopsWithTheContext(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		f.m.RunCleanup(runCtx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCleanup did not return after its context ended")
	}
}

func TestList(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(0))
		now := f.clock.Now().UnixMilli()
		f.login("first", 80, now-3000)
		f.login("second", 80, now-1000)
		f.login("third", 80, now-2000)
		f.login("other-account", 81, now)
		if err := f.m.table.end(ctx, "third", ReasonLogout); err != nil {
			t.Fatal(err)
		}
		list, err := f.m.List(ctx, 80, "second")
		if err != nil {
			t.Fatal(err)
		}
		var order []string
		for _, info := range list {
			order = append(order, info.ID)
		}
		if strings.Join(order, ",") != "second,third,first" {
			t.Fatalf("order %v, want the most recently active first, ended ones included", order)
		}
		if !list[0].Current || list[1].Current || list[2].Current {
			t.Fatalf("current flags: %+v", list)
		}
		got := list[1]
		if got.Active || got.EndReason != ReasonLogout || got.EndedAt != now || got.Username != "user80" ||
			got.IPAddress != "198.51.100.20" || got.UserAgent != "Agent/2.0" || got.ClientFingerprint != "fp_abcdef12" ||
			got.CreatedAt != now-2000 || got.LastActivityAt != now-2000 {
			t.Fatalf("ended entry: %+v", got)
		}

		raw, err := json.Marshal(list[0])
		if err != nil {
			t.Fatal(err)
		}
		// The BIGINT columns have always gone out as text on PostgreSQL
		created := fmt.Sprintf(db.Pick("%d", `"%d"`), now-1000)
		want := fmt.Sprintf(`{"id":"second","current":true,"username":"user80","ipAddress":"198.51.100.20","userAgent":"Agent/2.0",`+
			`"clientFingerprint":"fp_abcdef12","createdAt":%s,"lastActivityAt":%s,"endedAt":null,"endReason":null,"active":true,"expiresAt":%d}`,
			created, created, list[0].ExpiresAt)
		if string(raw) != want {
			t.Fatalf("JSON:\n got %s\nwant %s", raw, want)
		}

		// An entry with nothing set is all null
		f.exec(`UPDATE panel_sessions SET username = NULL, ip_address = '', user_agent = NULL, client_fingerprint = NULL,
		        created_at = NULL, last_activity_at = NULL WHERE sid = ?`, "first")
		list, err = f.m.List(ctx, 80, "")
		if err != nil {
			t.Fatal(err)
		}
		last := list[len(list)-1]
		if last.ID != "first" {
			t.Fatalf("an entry without times sorts last on both databases, got order ending in %s", last.ID)
		}
		raw, _ = json.Marshal(last)
		want = fmt.Sprintf(`{"id":"first","current":false,"username":null,"ipAddress":null,"userAgent":null,"clientFingerprint":null,`+
			`"createdAt":null,"lastActivityAt":null,"endedAt":null,"endReason":null,"active":true,"expiresAt":%d}`, last.ExpiresAt)
		if string(raw) != want {
			t.Fatalf("JSON:\n got %s\nwant %s", raw, want)
		}
	})
}

func TestListLimit(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(0))
		now := f.clock.Now().UnixMilli()
		for i := 0; i < 55; i++ {
			f.login(fmt.Sprintf("s%02d", i), 90, now-int64(i))
		}
		list, err := f.m.List(ctx, 90, "")
		if err != nil || len(list) != 50 {
			t.Fatalf("got %d entries (%v), want 50", len(list), err)
		}
		if list[0].ID != "s00" || list[49].ID != "s49" {
			t.Fatalf("got %s to %s, want the 50 most recently active", list[0].ID, list[49].ID)
		}
		active, err := f.m.table.list(ctx, 90, false, 500)
		if err != nil || len(active) != 55 {
			t.Fatalf("active only, limit 500: %d entries (%v), want all 55 under the cap of 200", len(active), err)
		}
	})
}
