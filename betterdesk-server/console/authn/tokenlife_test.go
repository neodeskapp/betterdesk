package authn

import (
	"context"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A new lifetime counts for the next token, without a restart; a value
// that is no whole number above zero means 7 days, as at the start.
func TestTokenLifetimeFollowsTheSetting(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		s, _, _ := testService(t, db, func(o *Options) { o.Now = func() time.Time { return now } })
		id := addUser(t, s, "operator", "pw", "operator", "local")
		for _, c := range []struct {
			set  string
			days int
		}{{"", 7}, {"30", 30}, {"12.9", 12}, {"0", 7}, {"abc", 7}, {"2", 2}} {
			if c.set != "" {
				s.SetTokenExpiryDays(c.set)
			}
			if got := s.TokenExpiryDays(); got != c.days {
				t.Errorf("after %q: %d days, want %d", c.set, got, c.days)
			}
			token, err := s.GenerateAccessToken(ctx, id, "", "uuid-"+c.set, "")
			if err != nil {
				t.Fatal(err)
			}
			want := store.DateTimeMillis(now.Add(time.Duration(c.days) * 24 * time.Hour))
			var found int
			if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM access_tokens WHERE token_hash = ? AND expires_at = ?", HashAccessToken(token), want).Scan(&found); err != nil {
				t.Fatal(err)
			}
			if found != 1 {
				t.Errorf("after %q: the token does not end at %s", c.set, want)
			}
		}
	})
}
