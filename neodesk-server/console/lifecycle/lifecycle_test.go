package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func account(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	id, err := db.Insert(context.Background(), `INSERT INTO users (username, password_hash, role, email) VALUES (?, 'x', 'viewer', 'old@example.test')`, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A confirmation link works once and until it expires, for its account; a
// new one or a cancel ends it; only its hash is stored. End dates, the
// exception and Enabled are kept per account.
func TestLifecycle(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		ctx := context.Background()
		for range 2 {
			if err := Ensure(ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
		s := New(db, func() time.Time { return now })
		ada := account(t, db, "ada")

		first, _, err := s.IssueEmailChange(ctx, ada, "new@example.test", "admin")
		if err != nil {
			t.Fatal(err)
		}
		var stored int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM email_changes WHERE token_hash = ?`, first).Scan(&stored); err != nil || stored != 0 {
			t.Fatalf("the token itself is stored: %d %v", stored, err)
		}
		second, expires, err := s.IssueEmailChange(ctx, ada, "newer@example.test", "admin")
		if err != nil || !expires.Equal(now.Add(EmailChangeLifetime)) {
			t.Fatalf("second: %v %v", expires, err)
		}
		if _, err := s.LookupEmailChange(ctx, first); !errors.Is(err, ErrUnusable) {
			t.Errorf("the first link after a new one: %v", err)
		}
		c, err := s.ConfirmEmailChange(ctx, second)
		if err != nil || c.Email != "newer@example.test" || c.Username != "ada" {
			t.Fatalf("confirm: %+v %v", c, err)
		}
		var email string
		if err := db.QueryRow(ctx, `SELECT email FROM users WHERE id = ?`, ada).Scan(&email); err != nil || email != "newer@example.test" {
			t.Fatalf("address after confirm: %q %v", email, err)
		}
		if _, err := s.ConfirmEmailChange(ctx, second); !errors.Is(err, ErrUnusable) {
			t.Errorf("a used link: %v", err)
		}

		late, _, _ := s.IssueEmailChange(ctx, ada, "late@example.test", "")
		now = now.Add(EmailChangeLifetime)
		if _, err := s.ConfirmEmailChange(ctx, late); !errors.Is(err, ErrUnusable) {
			t.Errorf("an expired link: %v", err)
		}
		if p, err := s.PendingEmail(ctx, ada); err != nil || p == nil || !p.Expired {
			t.Errorf("pending after expiry: %+v %v", p, err)
		}
		if had, err := s.CancelEmailChange(ctx, ada); !had || err != nil {
			t.Errorf("cancel: %v %v", had, err)
		}

		// End dates
		end := now.Add(48 * time.Hour)
		if err := s.SetExpiry(ctx, ada, end); err != nil {
			t.Fatal(err)
		}
		if gone, err := Expired(ctx, db, ada, now); gone || err != nil {
			t.Errorf("before the end date: %v %v", gone, err)
		}
		if gone, err := Expired(ctx, db, ada, end); !gone || err != nil {
			t.Errorf("on the end date: %v %v", gone, err)
		}
		now = end.Add(time.Hour)
		if err := s.Enabled(ctx, ada); err != nil {
			t.Fatal(err)
		}
		st, err := s.Get(ctx, ada)
		if err != nil || !st.ExpiresAt.IsZero() || !st.ActiveFrom.Equal(now) {
			t.Errorf("after enabling: %+v %v", st, err)
		}
		if err := s.Update(ctx, ada, func(st *State) { st.InactiveExempt = true }); err != nil {
			t.Fatal(err)
		}
		if all, err := s.All(ctx); err != nil || !all[ada].InactiveExempt {
			t.Errorf("all: %+v %v", all, err)
		}
		if err := s.Forget(ctx, ada); err != nil {
			t.Fatal(err)
		}
		if st, _ := s.Get(ctx, ada); st != (State{}) {
			t.Errorf("after forget: %+v", st)
		}
	})
}
