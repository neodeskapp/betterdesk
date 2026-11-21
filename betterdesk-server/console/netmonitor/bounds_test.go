package netmonitor

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// streamServer answers 200 and then sends its body for good: every gap
// apart, a part of the size, until the client goes. It counts the bytes
// it wrote.
func streamServer(t *testing.T, part int, gap time.Duration) (port int, written *atomic.Int64) {
	t.Helper()
	written = &atomic.Int64{}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ErrorLog: log.New(io.Discard, "", 0), Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/octet-stream")
		rw.WriteHeader(http.StatusOK)
		chunk := make([]byte, part)
		for {
			n, err := rw.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
			rw.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(gap):
			}
		}
	})}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close() })
	return l.Addr().(*net.TCPAddr).Port, written
}

// An HTTP check whose answer keeps coming ends all the same, with the
// answer's status: a body that drips ends at the check's deadline, its
// timeout and a few seconds, and a body that streams after a bounded part
// of it was read (BUGS RV45).
func TestAnHTTPCheckOfABodyWithoutEndEnds(t *testing.T) {
	w := newWorld(t)
	p := w.prober(false)
	for _, c := range []struct {
		name   string
		part   int
		gap    time.Duration
		within time.Duration
	}{
		{"a byte every 50 ms", 1, 50 * time.Millisecond, 300*time.Millisecond + 4*time.Second},
		{"a stream without pause", 32 << 10, 0, 2 * time.Second},
	} {
		port, written := streamServer(t, c.part, c.gap)
		done := make(chan Result, 1)
		start := time.Now()
		go func() { done <- p.checkHTTP(context.Background(), "http://"+standIn+":"+strconv.Itoa(port)+"/", 300) }()
		select {
		case res := <-done:
			if got := w.resultJSON(res); !strings.HasPrefix(got, `{"success":true,"rtt_ms":`) || !strings.HasSuffix(got, `,"status_code":200,"error":null}`) {
				t.Errorf("%s: result %s", c.name, got)
			}
			if took := time.Since(start); took > c.within {
				t.Errorf("%s: the check took %s, want at most %s", c.name, took, c.within)
			}
			if n := written.Load(); n > 16<<20 {
				t.Errorf("%s: the server wrote %d bytes before the check ended", c.name, n)
			}
		case <-time.After(c.within + 2*time.Second):
			t.Fatalf("%s: the check did not end", c.name)
		}
	}
}
