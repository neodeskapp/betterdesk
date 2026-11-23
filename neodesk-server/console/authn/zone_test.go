package authn

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// inZone opens the schema of db again through connections whose session
// time zone is zone, as on a PostgreSQL server that does not run in UTC.
func inZone(t *testing.T, db *store.DB, zone string) *store.DB {
	t.Helper()
	ctx := context.Background()
	var schema string
	if err := db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv(storetest.PostgresEnv))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["timezone"] = zone
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.OpenPostgres(pool)
}

// secondsFromNow is how far a time column of one row is from the
// database's clock, in seconds.
func secondsFromNow(t *testing.T, db *store.DB, query string, args ...any) float64 {
	t.Helper()
	var seconds float64
	if err := db.QueryRow(context.Background(), query, args...).Scan(&seconds); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return seconds
}

// TestLockoutAndTokenTimesInAnotherZone: on a PostgreSQL server whose
// session time zone is not UTC, a lockout lasts its 15 minutes and an
// access token its days, and the seconds a locked account has to wait are
// those of the stored failure, also when the process runs in another zone
// (BUGS.md AN2, AN3).
func TestLockoutAndTokenTimesInAnotherZone(t *testing.T) {
	base := storetest.Prepare(t, storetest.Postgres(t), storetest.ConsoleFirst)
	for _, zone := range []string{"Europe/Berlin", "America/New_York", "Asia/Kolkata"} {
		t.Run(zone, func(t *testing.T) {
			location, err := time.LoadLocation(zone)
			if err != nil {
				t.Skip("no time zone data")
			}
			db := inZone(t, base, zone)
			ctx := context.Background()
			mustExec(t, db, `DELETE FROM account_lockouts`)
			mustExec(t, db, `DELETE FROM login_attempts`)
			mustExec(t, db, `DELETE FROM access_tokens`)
			mustExec(t, db, `DELETE FROM users`)
			s, _, _ := testService(t, db, func(o *Options) {
				o.Settings = Settings{MaxFailedAttempts: 1, LockoutMinutes: 15}
			})
			s.zone = location

			if err := s.RecordAttempt(ctx, "alice", "198.51.100.7", false); err != nil {
				t.Fatal(err)
			}
			check, err := s.CheckBruteForce(ctx, "alice", "198.51.100.7")
			if err != nil || !check.Blocked || check.RetryAfter < 840 || check.RetryAfter > 900 {
				t.Errorf("check after the lockout: %+v, %v; want blocked for 900 seconds", check, err)
			}

			id := addUser(t, s, "client", "pw", "operator", "local")
			token, err := s.IssueAccessToken(ctx, id, "client-id", "uuid", "198.51.100.7")
			if err != nil {
				t.Fatal(err)
			}
			week := float64(7 * 24 * 3600)
			left := secondsFromNow(t, db, `SELECT EXTRACT(EPOCH FROM expires_at - NOW()) FROM access_tokens WHERE user_id = ?`, id)
			if left < week-60 || left > week {
				t.Errorf("the token expires in %.0f seconds, want %.0f", left, week)
			}
			if user, err := s.ValidateAccessToken(ctx, token); err != nil || user == nil {
				t.Errorf("the new token: %v, %v", user, err)
			}

			// The wait counts to the millisecond of the stored failure: an
			// end 600.5 seconds away is 601 to wait
			now := time.Now().Add(time.Hour).Truncate(time.Second)
			s.now = func() time.Time { return now }
			failed := now.Add(600*time.Second + 500*time.Millisecond - 15*time.Minute)
			mustExec(t, db, `UPDATE login_attempts SET created_at = ? WHERE username = 'alice'`, failed.UTC().Format("2006-01-02 15:04:05.000")+"+00")
			check, err = s.CheckBruteForce(ctx, "alice", "198.51.100.7")
			if err != nil || !check.Blocked || check.RetryAfter != 601 {
				t.Errorf("check of an end 600.5 seconds away: %+v, %v; want 601 seconds", check, err)
			}
		})
	}
}
