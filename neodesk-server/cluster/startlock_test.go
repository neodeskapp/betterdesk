package cluster

import (
	"context"
	"testing"
	"time"
)

// TestASecondStartWaitsForTheFirst: while one instance holds the
// installation lock for its one-time steps, another waits, and gets it
// once the first lets go.
func TestASecondStartWaitsForTheFirst(t *testing.T) {
	poolA, poolB := testPool(t), testPool(t)
	unlockA, err := LockInstallation(context.Background(), poolA)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func(), 1)
	go func() {
		unlockB, err := LockInstallation(context.Background(), poolB)
		if err != nil {
			t.Error(err)
			return
		}
		got <- unlockB
	}()
	select {
	case <-got:
		t.Fatal("the second start got the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	unlockA()
	unlockA() // twice is harmless
	select {
	case unlockB := <-got:
		unlockB()
	case <-time.After(5 * time.Second):
		t.Fatal("the second start never got the lock")
	}
}
