package users

import (
	"context"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
)

// sweepInactive runs the rule for inactive accounts (filled in with the
// rule's settings).
func sweepInactive(ctx context.Context, d *kit.Deps, life *lifecycle.Store, accounts []sweepAccount, states map[int64]lifecycle.State, now time.Time) error {
	return nil
}
