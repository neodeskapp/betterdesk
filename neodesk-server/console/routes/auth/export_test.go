package auth

import (
	"context"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// RunHousekeeping runs the group's clean-up job with a short interval
// until ctx ends.
func RunHousekeeping(ctx context.Context, d *kit.Deps, interval time.Duration) {
	newHandlers(d).housekeeping(ctx, interval)
}

// HoldResetWork keeps the work of the reset requests from running after
// their answers until the returned function runs it, for the rest of the
// test.
func HoldResetWork(t interface{ Cleanup(func()) }) func() {
	var held []func()
	before := resetWork
	resetWork = func(f func()) { held = append(held, f) }
	t.Cleanup(func() { resetWork = before })
	return func() {
		for _, f := range held {
			f()
		}
		held = nil
	}
}
