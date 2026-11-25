package passwordlinks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// account adds an account and returns its id.
func account(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	id, err := db.Insert(context.Background(), `INSERT INTO users (username, password_hash, role) VALUES (?, 'old-hash', 'viewer')`, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func passwordHash(t *testing.T, db *store.DB, id int64) string {
	t.Helper()
	var hash string
	if err := db.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

// A link works once and until it expires, for the account it was made
// for; a new link, a revoke and a disabled account end it; only its hash
// is stored.
func TestLinks(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		ctx := context.Background()
		if err := Ensure(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(ctx, db); err != nil {
			t.Fatalf("second Ensure: %v", err)
		}
		now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
		s := New(db, func() time.Time { return now })
		ada := account(t, db, "ada")

		token, expires, err := s.Issue(ctx, ada, PurposeInvite, "admin")
		if err != nil {
			t.Fatal(err)
		}
		if !expires.Equal(now.Add(InviteLifetime)) || len(token) < 40 {
			t.Fatalf("token %q expires %v", token, expires)
		}
		var stored int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM password_links WHERE token_hash = ?`, token).Scan(&stored); err != nil || stored != 0 {
			t.Fatalf("the token itself is in the table: %d %v", stored, err)
		}
		link, err := s.Lookup(ctx, token)
		if err != nil || link.UserID != ada || link.Username != "ada" || link.Purpose != PurposeInvite {
			t.Fatalf("lookup: %+v %v", link, err)
		}
		invites, err := s.Invites(ctx)
		if err != nil || len(invites) != 1 || invites[ada].Expired {
			t.Fatalf("invites: %v %v", invites, err)
		}
		for _, bad := range []string{"", "nope", token + "x", strings.ToUpper(token)} {
			if _, err := s.Lookup(ctx, bad); !errors.Is(err, ErrUnusable) {
				t.Errorf("lookup %q: %v", bad, err)
			}
		}

		// Expired: refused, and listed as expired
		now = now.Add(InviteLifetime)
		if _, err := s.Lookup(ctx, token); !errors.Is(err, ErrUnusable) {
			t.Errorf("expired link: %v", err)
		}
		if _, err := s.Redeem(ctx, token, "new-hash"); !errors.Is(err, ErrUnusable) || passwordHash(t, db, ada) != "old-hash" {
			t.Errorf("expired link redeemed: %v", err)
		}
		if invites, _ := s.Invites(ctx); !invites[ada].Expired {
			t.Errorf("expired invite: %v", invites)
		}

		// A new link ends the one before
		first, _, _ := s.Issue(ctx, ada, PurposeReset, "admin")
		second, _, _ := s.Issue(ctx, ada, PurposeReset, "admin")
		if _, err := s.Lookup(ctx, first); !errors.Is(err, ErrUnusable) {
			t.Errorf("replaced link: %v", err)
		}
		if invites, _ := s.Invites(ctx); len(invites) != 0 {
			t.Errorf("a reset link counts as an invite: %v", invites)
		}

		// Once
		link, err = s.Redeem(ctx, second, "new-hash")
		if err != nil || link.UserID != ada || passwordHash(t, db, ada) != "new-hash" {
			t.Fatalf("redeem: %+v %v", link, err)
		}
		if _, err := s.Redeem(ctx, second, "other-hash"); !errors.Is(err, ErrUnusable) || passwordHash(t, db, ada) != "new-hash" {
			t.Errorf("second redeem: %v", err)
		}

		// Revoked, and on a disabled account
		third, _, _ := s.Issue(ctx, ada, PurposeReset, "admin")
		if err := s.Revoke(ctx, ada); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Lookup(ctx, third); !errors.Is(err, ErrUnusable) {
			t.Errorf("revoked link: %v", err)
		}
		fourth, _, _ := s.Issue(ctx, ada, PurposeReset, "admin")
		if _, err := db.Exec(ctx, `UPDATE users SET disabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, ada); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Lookup(ctx, fourth); !errors.Is(err, ErrUnusable) {
			t.Errorf("link of a disabled account: %v", err)
		}
	})
}

// The invite mail names the account, the sender, the link and the end
// of the link, in the text and the HTML part, in the console's language
// files.
func TestBuildMail(t *testing.T) {
	texts := i18n.New("../static/files/lang", "en")
	texts.Load()
	tr := func(key string, vars map[string]any) string {
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return texts.Translate("en", key, i18n.V(pairs...))
	}
	m := BuildMail(tr, MailFacts{
		Purpose: PurposeInvite, Link: "https://desk.example/set-password#tok", Username: "ada",
		Sender: "Grace Hopper", Host: "desk.example", Expires: "2026-12-02 12:00 UTC",
		Branding: brandmail.Branding{Server: "Acme Desk"},
	})
	if m.Subject != "Grace Hopper invited you to Acme Desk" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, part := range []string{m.Text, m.HTML} {
		for _, want := range []string{"https://desk.example/set-password#tok", "ada", "2026-12-02 12:00 UTC", "Grace Hopper made you an account on Acme Desk", "Choose your password"} {
			if !strings.Contains(part, want) {
				t.Errorf("no %q in\n%s", want, part)
			}
		}
		if strings.Contains(part, "password_link.") {
			t.Errorf("a key without text in\n%s", part)
		}
	}
}
