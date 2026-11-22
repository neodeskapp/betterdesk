package auth

import (
	"context"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// RunHousekeeping runs the group's clean-up job with a short interval
// until ctx ends.
func RunHousekeeping(ctx context.Context, d *kit.Deps, interval time.Duration) {
	newHandlers(d).housekeeping(ctx, interval)
}
