package signal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/crypto"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

func startLimitTestServer(t *testing.T, signalPort int, configure func(*Server)) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.SignalPort = signalPort
	cfg.RelayPort = signalPort + 1
	dir := t.TempDir()
	cfg.DBPath = dir + "/test.db"
	cfg.KeyFile = dir + "/id_ed25519"
	database, err := db.OpenSQLite(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	database.Migrate()
	t.Cleanup(func() { database.Close() })
	kp, err := crypto.LoadOrGenerateKeyPair(cfg.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, kp, database)
	if configure != nil {
		configure(srv)
	}
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	time.Sleep(200 * time.Millisecond)
	return srv
}

// wsClosedWithin reports whether the server closes the connection within limit.
func wsClosedWithin(t *testing.T, ws *websocket.Conn, limit time.Duration) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), limit+2*time.Second)
	defer cancel()
	start := time.Now()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return !errors.Is(err, context.DeadlineExceeded) && time.Since(start) <= limit
		}
		if len(data) == 0 {
			continue // the server's idle keepalive frame
		}
	}
}

func TestWSSignalClosesSilentConnection(t *testing.T) {
	old := atomic.SwapInt64(&wsSignalFirstReadNs, int64(300*time.Millisecond))
	t.Cleanup(func() { atomic.StoreInt64(&wsSignalFirstReadNs, old) })
	srv := startLimitTestServer(t, 29220, nil)

	ws, _, err := websocket.Dial(t.Context(), fmt.Sprintf("ws://127.0.0.1:%d/", srv.cfg.WSSignalPort()), nil)
	if err != nil {
		t.Fatalf("WS dial: %v", err)
	}
	defer ws.CloseNow()

	if !wsClosedWithin(t, ws, 2*time.Second) {
		t.Fatal("a WebSocket that never sends was not closed")
	}
}

func TestWSSignalLimitsOpenConnectionsPerAddress(t *testing.T) {
	srv := startLimitTestServer(t, 29230, func(s *Server) { s.wsConns = newConnLimiter(2, 100) })
	url := fmt.Sprintf("ws://127.0.0.1:%d/", srv.cfg.WSSignalPort())

	for i := 0; i < 2; i++ {
		ws, _, err := websocket.Dial(t.Context(), url, nil)
		if err != nil {
			t.Fatalf("WS dial %d: %v", i+1, err)
		}
		defer ws.CloseNow()
	}
	if ws, _, err := websocket.Dial(t.Context(), url, nil); err == nil {
		ws.CloseNow()
		t.Fatal("a third open WebSocket from one address was accepted")
	}
}

func TestNATPortLimitsAndClosesSilentConnections(t *testing.T) {
	old := atomic.SwapInt64(&natReadNs, int64(300*time.Millisecond))
	t.Cleanup(func() { atomic.StoreInt64(&natReadNs, old) })
	srv := startLimitTestServer(t, 29240, func(s *Server) { s.natConns = newConnLimiter(1, 100) })
	addr := fmt.Sprintf("127.0.0.1:%d", srv.cfg.NATTestPort())

	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	time.Sleep(50 * time.Millisecond)
	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	second.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := second.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("a second open connection from one address was kept (err=%v)", err)
	}
	first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := first.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("a silent NAT port connection was not closed (err=%v)", err)
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
