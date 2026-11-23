package authn

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestAuthenticateReportsAProAccount: the right password of a pro account
// is the failure FailureProOnly, so the route can say what the account is
// for; a wrong one is the plain refusal (BUGS.md S11).
func TestAuthenticateReportsAProAccount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "client", "pw", "pro", "local")
		outcome, err := s.Authenticate(ctx, "client", "pw")
		if err != nil || outcome.User != nil || outcome.Failure != FailureProOnly {
			t.Errorf("right password: %+v, %v; want the failure %s", outcome, err, FailureProOnly)
		}
		outcome, err = s.Authenticate(ctx, "client", "wrong")
		if err != nil || outcome.User != nil || outcome.IsFailure() {
			t.Errorf("wrong password: %+v, %v; want the plain refusal", outcome, err)
		}
		var noted int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE id = ? AND last_login IS NOT NULL`, id).Scan(&noted); err != nil || noted != 0 {
			t.Errorf("a refused sign-in noted a sign-in on the row: %d, %v", noted, err)
		}
	})
}
