package auth

import (
	"context"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// RegisterOIDCSession adds GET /api/auth/oidc/session for the tests, with
// a stand-in for the profile picture sync.
func RegisterOIDCSession(d *kit.Deps, syncPicture func(userID int64, exchange *goapi.Object)) {
	h := newHandlers(d)
	h.syncPicture = syncPicture
	d.Router.Get("/api/auth/oidc/session", h.oidcSession)
}

// RunHousekeeping runs the group's clean-up job with a short interval
// until ctx ends.
func RunHousekeeping(ctx context.Context, d *kit.Deps, interval time.Duration) {
	newHandlers(d).housekeeping(ctx, interval)
}
