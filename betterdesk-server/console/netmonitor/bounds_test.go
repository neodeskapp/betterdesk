package netmonitor

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A target stored with a timeout above MaxTimeoutMs, from before the
// routes held timeout_ms to it, is checked with MaxTimeoutMs, and every
// check runs under a deadline of its timeout and a few seconds (BUGS
// RV44).
func TestALongTimeoutIsBounded(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, row := range []string{
		"INSERT INTO network_targets (name, host, port, check_type, timeout_ms) VALUES ('tcp', '10.1.2.3', 80, 'tcp', 2147480000)",
		"INSERT INTO network_targets (name, host, check_type, timeout_ms) VALUES ('ping', '10.1.2.3', 'ping', 2147480000)",
	} {
		if _, err := db.Exec(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	var (
		mu        sync.Mutex
		deadlines = map[string]time.Duration{}
		pingArgs  string
	)
	note := func(what string, ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()
		deadlines[what] = -1
		if deadline, ok := ctx.Deadline(); ok {
			deadlines[what] = time.Until(deadline)
		}
	}
	// A minute for the check and three seconds for the ping process
	const limit = 63 * time.Second
	no := false
	m := New(Options{
		Store:    db,
		Resolver: &tableResolver{names: map[string][]string{}, errors: map[string]string{}},
		Windows:  &no,
		Dialer: dialerFunc(func(ctx context.Context, network, _ string) (net.Conn, error) {
			note("dial", ctx)
			return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		}),
		Ping: func(ctx context.Context, _ string, args ...string) ([]byte, int, error) {
			note("ping", ctx)
			mu.Lock()
			pingArgs = strings.Join(args, " ")
			mu.Unlock()
			return nil, 1, nil
		},
	})
	targets, err := ListTargets(ctx, db, TargetFilter{})
	if err != nil || len(targets) != 2 {
		t.Fatalf("targets: %d, %v", len(targets), err)
	}
	for i := range targets {
		m.CheckTarget(ctx, &targets[i])
	}
	if pingArgs != "-c 1 -W 60 10.1.2.3" {
		t.Errorf("the ping command got %q, want a wait of 60 seconds", pingArgs)
	}
	for _, what := range []string{"dial", "ping"} {
		if got := deadlines[what]; got < 59*time.Second || got > limit {
			t.Errorf("the %s may take %s, want at most %s", what, got, limit)
		}
	}

	// The tools' checks get the same deadline from a context without one
	deadlines = map[string]time.Duration{}
	m.CheckTCPPort(ctx, "10.1.2.3", 80, 2147480000)
	m.PingHost(ctx, "10.1.2.3", 2147480000)
	for _, what := range []string{"dial", "ping"} {
		if got := deadlines[what]; got < 59*time.Second || got > limit {
			t.Errorf("the tool's %s may take %s, want at most %s", what, got, limit)
		}
	}
}
