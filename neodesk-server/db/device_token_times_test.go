package db

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// TestDeviceTokenTimesAndCleanup: the device token list has the revoke and
// last-use times the single read has, and the cleanup marks exactly the
// pending tokens whose expiry has passed, also an expiry written in
// another zone, on both databases (BUGS RV78).
func TestDeviceTokenTimesAndCleanup(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		at := func(d time.Duration, zone *time.Location) *time.Time {
			tm := time.Now().Add(d).In(zone).Truncate(time.Second)
			return &tm
		}
		plus2, minus5 := time.FixedZone("plus2", 2*3600), time.FixedZone("minus5", -5*3600)
		tokens := map[string]*time.Time{
			"expired-utc":    at(-time.Hour, time.UTC),
			"expired-minus5": at(-time.Hour, minus5),
			"valid-utc":      at(time.Hour, time.UTC),
			"valid-plus2":    at(time.Hour, plus2),
			"used":           nil,
		}
		ids := map[string]int64{}
		for name, expires := range tokens {
			tok := &DeviceToken{
				Token: "rv78" + name + "-token", TokenHash: "rv78-" + name, Name: name,
				Status: TokenStatusPending, ExpiresAt: expires,
			}
			if err := database.CreateDeviceToken(tok); err != nil {
				t.Fatal(err)
			}
			ids[name] = tok.ID
		}
		if err := database.IncrementTokenUse("rv78-used"); err != nil {
			t.Fatal(err)
		}
		if err := database.RevokeDeviceToken(ids["used"]); err != nil {
			t.Fatal(err)
		}

		marked, err := database.CleanupExpiredTokens()
		if err != nil {
			t.Fatal(err)
		}
		if marked != 2 {
			t.Errorf("cleanup marked %d tokens, want the two expired ones", marked)
		}
		list, err := database.ListDeviceTokens(true)
		if err != nil {
			t.Fatal(err)
		}
		var statuses []string
		recent := func(what string, tm *time.Time) {
			t.Helper()
			if tm == nil {
				t.Errorf("%s missing in the list", what)
			} else if d := time.Since(*tm); d < -time.Minute || d > time.Minute {
				t.Errorf("%s %s is %s away from now", what, tm, d.Round(time.Second))
			}
		}
		for _, tok := range list {
			statuses = append(statuses, tok.Name+"="+tok.Status)
			if tok.Name == "used" {
				recent("revoked_at", tok.RevokedAt)
				recent("last_used_at", tok.LastUsedAt)
			}
		}
		sort.Strings(statuses)
		if got, want := strings.Join(statuses, " "), "expired-minus5=expired expired-utc=expired used=revoked valid-plus2=pending valid-utc=pending"; got != want {
			t.Errorf("statuses %q, want %q", got, want)
		}
	})
}
