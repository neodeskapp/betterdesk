package authn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// disable marks an account disabled, as the Users page does.
func disable(t *testing.T, db *store.DB, id int64) {
	t.Helper()
	mustExec(t, db, `UPDATE users SET disabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, id)
}

// A disabled account is refused at every sign-in once its credentials are
// right, with a code of its own: a local password, the directory and
// single sign-on. A wrong password gets the plain refusal, so the answer
// tells nobody without the password that the account is disabled. A
// disabled directory account stays the one row, it is not made again
// (L107).
func TestDisabledAccountsCannotSignIn(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		ctx := context.Background()
		s, stub, _ := testService(t, db, nil)

		local := addUser(t, s, "dora", "pw", "operator", "local")
		if outcome, err := s.Authenticate(ctx, "dora", "pw"); err != nil || outcome.User == nil {
			t.Fatalf("enabled: %+v, %v", outcome, err)
		}
		disable(t, db, local)
		outcome, err := s.Authenticate(ctx, "dora", "pw")
		if err != nil || outcome.User != nil || outcome.Failure != FailureAccountDisabled {
			t.Errorf("disabled, right password: %+v, %v; want %s", outcome, err, FailureAccountDisabled)
		}
		stub.load(&goScript{})
		if outcome, err := s.Authenticate(ctx, "dora", "wrong"); err != nil || outcome.User != nil || outcome.IsFailure() {
			t.Errorf("disabled, wrong password: %+v, %v; want the plain refusal", outcome, err)
		}

		directory := addUser(t, s, "erin", "unused", "viewer", "ldap")
		disable(t, db, directory)
		stub.load(&goScript{SSO: json.RawMessage(ldapOn), Login: []json.RawMessage{json.RawMessage(ldapAccepted)}})
		if outcome, err := s.Authenticate(ctx, "erin", "pw"); err != nil || outcome.User != nil || outcome.Failure != FailureAccountDisabled {
			t.Errorf("disabled directory account: %+v, %v; want %s", outcome, err, FailureAccountDisabled)
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE username = 'erin'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("%d rows of erin, want the one: %v", n, err)
		}

		sso := addUser(t, s, "finn", "unused", "viewer", "oidc")
		disable(t, db, sso)
		if user, err := s.OIDCUser(ctx, "finn", "viewer"); !errors.Is(err, ErrAccountDisabled) || user != nil {
			t.Errorf("disabled single sign-on account: %+v, %v; want ErrAccountDisabled", user, err)
		}
	})
}

// The access tokens of a disabled account no longer sign anything in, and
// work again once the account is enabled (L107).
func TestDisabledAccountsTokensAreRefused(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "gina", "pw", "operator", "local")
		token, err := s.IssueAccessToken(ctx, id, "client", "uuid", "198.51.100.7")
		if err != nil {
			t.Fatal(err)
		}
		disable(t, db, id)
		if user, err := s.ValidateAccessToken(ctx, token); err != nil || user != nil {
			t.Errorf("token of a disabled account: %+v, %v", user, err)
		}
		mustExec(t, db, `UPDATE users SET disabled = `+db.Pick("0", "FALSE")+` WHERE id = ?`, id)
		if user, err := s.ValidateAccessToken(ctx, token); err != nil || user == nil || user.Username != "gina" {
			t.Errorf("token after the account was enabled: %+v, %v", user, err)
		}
	})
}
