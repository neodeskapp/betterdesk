package api

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/db"
)

// TestRewriteRemoteTargetConnectAllowlist: the connect guacd gets holds
// the values the server sets, the browser's values of the harmless display
// and clipboard parameters, and nothing else the browser sent: no drive,
// recording, SFTP, gateway or wake-on-LAN parameter, no value past guacd's
// names (RV175).
func TestRewriteRemoteTargetConnectAllowlist(t *testing.T) {
	target := &db.RemoteTarget{Host: "10.9.9.9", Port: 5900, Username: "", CredentialMode: "prompt"}
	names := []string{"VERSION_1_5_0", "hostname", "port", "username", "password", "width", "dpi",
		"enable-drive", "drive-path", "recording-path", "create-recording-path", "sftp-hostname",
		"gateway-hostname", "dest-host", "wol-send-packet", "ignore-cert", "disable-copy"}
	values := []string{"VERSION_1_5_0", "127.0.0.1", "6379", "alice", "pw", "1280", "96",
		"true", "/", "/etc/cron.d", "true", "evil.example", "evil.example", "127.0.0.1", "true", "true", "true", "extra"}
	got := rewriteRemoteTargetConnect(values, names, target, "")
	want := []string{"VERSION_1_5_0", "10.9.9.9", "5900", "alice", "pw", "1280", "96",
		"", "", "", "", "", "", "", "", "false", "true"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("connect values\n got  %q\n want %q", got, want)
	}
}

// TestRemoteTargetTunnelHoldsConnectUntilArgs: a connect that comes with
// select in one frame, before guacd answered with its parameter names,
// waits for them and reaches guacd rewritten, never with the browser's
// host and port (RV175).
func TestRemoteTargetTunnelHoldsConnectUntilArgs(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := gateway.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString(';'); err != nil { // select
			return
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = conn.Write(encodeGuacInstruction(guacInstruction{opcode: "args", args: []string{"VERSION_1_5_0", "hostname", "port", "password"}}))
		connect, err := reader.ReadString(';')
		if err != nil {
			return
		}
		received <- connect
	}()

	srv, database := newInternalServer(t, nil)
	srv.cfg.GuacdAddress = gateway.Addr().String()
	if err := database.CreateRemoteTarget(&db.RemoteTarget{
		ID: "rt-1", Name: "box", Protocol: "vnc", Host: "10.9.9.9", Port: 5900,
		CredentialMode: "prompt", TLSMode: "disabled", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tunnel/{id}", srv.handleRemoteTargetTunnel)
	web := httptest.NewServer(mux)
	defer web.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http")+"/tunnel/rt-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	frame := string(encodeGuacInstruction(guacInstruction{opcode: "select", args: []string{"vnc"}})) +
		string(encodeGuacInstruction(guacInstruction{opcode: "connect", args: []string{"VERSION_1_5_0", "127.0.0.1", "6379", "pw"}}))
	if err := ws.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	select {
	case connect := <-received:
		if want := "7.connect,13.VERSION_1_5_0,8.10.9.9.9,4.5900,2.pw;"; connect != want {
			t.Errorf("guacd got %q, want %q", connect, want)
		}
	case <-ctx.Done():
		t.Fatal("guacd got no connect")
	}
}
