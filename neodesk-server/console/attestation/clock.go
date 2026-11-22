package attestation

import (
	"context"
	"time"
)

// Clock is the time a run reads and waits on. Tests give one that moves
// only when the run waits.
type Clock interface {
	Now() time.Time
	// SleepUntil waits until t. It returns early with false when wake is
	// closed first (a nil channel never is), and with ctx's error when ctx
	// ends first.
	SleepUntil(ctx context.Context, t time.Time, wake <-chan struct{}) (bool, error)
}

// RealClock is the system's clock.
type RealClock struct{}

// Now is time.Now.
func (RealClock) Now() time.Time { return time.Now() }

// SleepUntil waits on a timer.
func (RealClock) SleepUntil(ctx context.Context, t time.Time, wake <-chan struct{}) (bool, error) {
	d := time.Until(t)
	if d <= 0 {
		return true, ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true, nil
	case <-wake:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
