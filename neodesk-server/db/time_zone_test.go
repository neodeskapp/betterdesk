package db

import (
	"fmt"
	"testing"
	"time"
)

// zonedStores runs a test on SQLite and on the shared test PostgreSQL
// opened with the DSN's timezone set to zone, as on a database server
// that runs in that zone.
func zonedStores(t *testing.T, zone string, test func(t *testing.T, store Database)) {
	t.Run("sqlite", func(t *testing.T) { test(t, newTestDB(t)) })
	t.Run("postgres", func(t *testing.T) {
		test(t, openTestPostgres(t, pgTestDSN(t, "", map[string]string{"timezone": zone})))
	})
}

// parseStoredTime reads a time the database hands back as text, with or
// without a zone; text without a zone is UTC.
func parseStoredTime(t *testing.T, text string) time.Time {
	t.Helper()
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05Z07", "2006-01-02 15:04:05.999999Z07", time.RFC3339} {
		if tm, err := time.Parse(layout, text); err == nil {
			return tm
		}
	}
	t.Fatalf("unreadable time %q", text)
	return time.Time{}
}

// TestTimesAsTextKeepTheirInstant: the API server passes UTC times as text
// without a zone. They mean the same instant on a PostgreSQL whose time zone
// is not UTC: a telemetry command queued for ten minutes is pending, and a
// client session's expiry read back and written again (a touch without
// sliding expiry) does not move (BUGS RV71).
func TestTimesAsTextKeepTheirInstant(t *testing.T) {
	for _, zone := range []string{"Europe/Warsaw", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			zonedStores(t, zone, func(t *testing.T, database Database) {
				run := fmt.Sprintf("%d", time.Now().UnixNano())
				device := "RV71DEV" + run
				pg, _ := database.(*PostgresDB)
				if pg != nil {
					t.Cleanup(func() {
						_, _ = pg.pool.Exec(pg.ctx, `DELETE FROM device_telemetry_commands WHERE device_id = $1`, device)
					})
				}

				expires := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
				if _, err := database.QueueTelemetryCommand(&TelemetryCommand{
					DeviceID: device, Command: "collect.metrics", Args: `{}`,
					ExpiresAt: expires.Format("2006-01-02 15:04:05"),
				}); err != nil {
					t.Fatal(err)
				}
				pending, err := database.GetPendingTelemetryCommands(device, 10)
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) != 1 {
					t.Fatalf("%d pending commands, want the one queued for ten minutes", len(pending))
				}
				if got := parseStoredTime(t, pending[0].ExpiresAt); !got.Equal(expires) {
					t.Errorf("command expires %s, written %s", got.UTC(), expires)
				}

				if err := database.EnsureClientSessionsSchema(); err != nil {
					t.Fatal(err)
				}
				user := &User{Username: "rv71" + run, PasswordHash: "x", Role: "viewer"}
				if err := database.CreateUser(user); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if pg != nil {
						_, _ = pg.pool.Exec(pg.ctx, `DELETE FROM client_sessions WHERE user_id = $1`, user.ID)
					}
					_ = database.DeleteUser(user.ID)
				})
				sessionExpiry := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second)
				sess := &ClientSession{
					TokenHash: "rv71" + run, UserID: user.ID, ClientID: device,
					ExpiresAt: sessionExpiry.Format("2006-01-02 15:04:05"),
				}
				if err := database.CreateClientSession(sess); err != nil {
					t.Fatal(err)
				}
				for touch := 0; touch < 3; touch++ {
					got, err := database.GetClientSessionByTokenHash(sess.TokenHash)
					if err != nil || got == nil {
						t.Fatalf("session after %d touches: %v %v", touch, got, err)
					}
					if at := parseStoredTime(t, got.ExpiresAt); !at.Equal(sessionExpiry) {
						t.Fatalf("session expiry after %d touches %s, written %s", touch, at.UTC(), sessionExpiry)
					}
					now := time.Now().UTC().Format("2006-01-02 15:04:05")
					if err := database.TouchClientSession(got.ID, got.ExpiresAt, now); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
