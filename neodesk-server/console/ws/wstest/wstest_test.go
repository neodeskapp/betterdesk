package wstest

import (
	"bytes"
	"testing"
)

// The goroutine check leaves out what the PostgreSQL driver started (BUGS
// L150): a dump with the test's own goroutine, a slow database's
// connection reader and the database handle's opener.
func TestCountGoroutinesLeavesOutTheDatabaseDriver(t *testing.T) {
	stacks := []byte(`goroutine 25765 [running]:
github.com/unitronix/betterdesk-server/console/ws/wstest.Goroutines.func1()
	/repo/betterdesk-server/console/ws/wstest/wstest.go:187 +0x51
created by testing.(*T).Run in goroutine 25157

goroutine 25967 [IO wait]:
internal/poll.runtime_pollWait(0x7f1c2c0d3e00, 0x72)
	/usr/local/go/src/runtime/netpoll.go:351 +0x85
github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).bgRead(0xc000a1c0a0)
	/go/pkg/mod/github.com/jackc/pgx/v5@v5.10.0/pgconn/internal/bgreader/bgreader.go:102 +0x7c
created by github.com/jackc/pgx/v5/pgconn/internal/bgreader.(*BGReader).Start in goroutine 25966
	/go/pkg/mod/github.com/jackc/pgx/v5@v5.10.0/pgconn/internal/bgreader/bgreader.go:61 +0xbf

goroutine 25773 [select]:
database/sql.(*DB).connectionOpener(0xc0001d2340, {0x1a2b3c0, 0xc000412230})
	/usr/local/go/src/database/sql/sql.go:1261 +0x87
created by database/sql.OpenDB in goroutine 25765
	/usr/local/go/src/database/sql/sql.go:841 +0x130
`)
	if got := countGoroutines(stacks); got != 2 {
		t.Fatalf("counted %d goroutines, want 2 (the driver's reader left out)", got)
	}
}

// A goroutine of the test's own still counts. Only this test's goroutines
// are counted: goroutines of the tests before it may end meanwhile.
func TestCountGoroutinesSeesAGoroutineLeftRunning(t *testing.T) {
	own := func() []byte {
		var blocks [][]byte
		for _, g := range bytes.Split(allStacks(), []byte("\n\n")) {
			if bytes.Contains(g, []byte("wstest.TestCountGoroutinesSeesAGoroutineLeftRunning")) {
				blocks = append(blocks, g)
			}
		}
		return bytes.Join(blocks, []byte("\n\n"))
	}
	before := countGoroutines(own())
	stop := make(chan struct{})
	defer close(stop)
	go func() { <-stop }()
	if got := countGoroutines(own()); got != before+1 {
		t.Fatalf("counted %d goroutines with one more running, %d before", got, before)
	}
}
