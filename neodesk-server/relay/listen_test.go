package relay

import (
	"net"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// A test relay listens on loopback only. On every interface, Windows asks
// for a firewall exception each time go test builds a new test binary.
func TestRelayListensOnLoopbackInTests(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.RelayPort = relayPort(t)
	srv := New(cfg)
	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	addr := srv.tcpLn.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Fatalf("relay listens on %s, want loopback", addr)
	}
}
