package api

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// A stock RustDesk client with an empty API server setting sends its
// heartbeat and system information to the ID server's port minus 2. With
// the API on another port, as in Docker, the API answers there too.
func TestAPIAnswersOnTheDerivedClientPort(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.APIPort = freeTCPPort(t)
	derived := freeTCPPort(t)
	database := testSetupDB(t)
	defer database.Close()
	srv := New(cfg, database, peer.NewMap(), nil, "1.0.0-test")
	srv.SetDerivedClientPort(derived)
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := &http.Client{Timeout: 3 * time.Second}
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", derived))
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the derived client port does not answer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health on the derived client port: %d", resp.StatusCode)
	}
}

// The API's own port is never opened twice.
func TestDerivedClientPortIsNotTheAPIPort(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.APIPort = 21114
	srv := New(cfg, nil, peer.NewMap(), nil, "1.0.0-test")
	srv.SetDerivedClientPort(21114)
	if srv.derivedPort != 0 {
		t.Fatalf("derived port %d on the API's own port", srv.derivedPort)
	}
}
