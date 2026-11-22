package attestation

import (
	"context"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The messages are the bytes protobufjs wrote for the same messages
// (recorded in testdata/runs_sqlite.json.gz).
func TestMessages(t *testing.T) {
	if got := hex.EncodeToString(healthCheck()); got != "d2010a0a0862642d62656e6368" {
		t.Errorf("health check %s", got)
	}
	got := hex.EncodeToString(requestRelay("bd-bench-05ac53faa148ef96"))
	if got != "9201250a0862642d62656e6368121962642d62656e63682d30356163353366616131343865663936" {
		t.Errorf("request relay %s", got)
	}
}

// The dialer connects, sends binary messages, waits for an answer and
// closes; a closed port and a server that never answers fail.
func TestWebSocketDialer(t *testing.T) {
	received := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				received <- data
			}
			_ = c.Write(r.Context(), websocket.MessageBinary, []byte("reply"))
		}
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx := context.Background()
	conn, err := WebSocketDialer{}.Dial(ctx, url, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Send(healthCheck()); err != nil {
		t.Fatal(err)
	}
	if got := <-received; hex.EncodeToString(got) != hex.EncodeToString(healthCheck()) {
		t.Errorf("server got %x", got)
	}
	if err := conn.AwaitMessage(ctx, 2*time.Second); err != nil {
		t.Errorf("no answer: %v", err)
	}
	conn.Close()

	// a port nobody listens on
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedURL := "ws://" + l.Addr().String()
	l.Close()
	if _, err := (WebSocketDialer{}).Dial(ctx, closedURL, 2*time.Second); err == nil {
		t.Error("connected to a closed port")
	}

	// a server that accepts the connection and never answers the handshake
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	if _, err := (WebSocketDialer{}).Dial(ctx, "ws://"+silent.Addr().String(), 50*time.Millisecond); err == nil || err.Error() != "connect timeout" {
		t.Errorf("silent server: %v", err)
	}
}

// Over wss a certificate no authority signed is refused unless
// ALLOW_SELF_SIGNED_CERTS allows it.
func TestWebSocketDialerChecksCertificates(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()
	url := "wss" + strings.TrimPrefix(srv.URL, "https")
	ctx := context.Background()
	if conn, err := (WebSocketDialer{}).Dial(ctx, url, 2*time.Second); err == nil {
		conn.Close()
		t.Error("connected to a server with a self-signed certificate")
	}
	conn, err := WebSocketDialer{AllowSelfSigned: true}.Dial(ctx, url, 2*time.Second)
	if err != nil {
		t.Fatalf("with self-signed certificates allowed: %v", err)
	}
	conn.Close()
}
