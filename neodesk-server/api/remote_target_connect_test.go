package api

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	got := rewriteRemoteTargetConnect(values, names, target, "", remoteTargetLimits{})
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

// TestRemoteTargetTunnelOutlivesTheHandshake: once guacd says ready, the
// dial deadline no longer applies, so a session runs past it while frames
// flow (RV176).
func TestRemoteTargetTunnelOutlivesTheHandshake(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
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
		_, _ = conn.Write(encodeGuacInstruction(guacInstruction{opcode: "args", args: []string{"VERSION_1_5_0", "hostname", "port"}}))
		if _, err := reader.ReadString(';'); err != nil { // connect
			return
		}
		_, _ = conn.Write(encodeGuacInstruction(guacInstruction{opcode: "ready", args: []string{"$id"}}))
		for range 26 {
			time.Sleep(500 * time.Millisecond)
			if _, err := conn.Write(encodeGuacInstruction(guacInstruction{opcode: "sync", args: []string{"1"}})); err != nil {
				return
			}
		}
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http")+"/tunnel/rt-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	for _, in := range []guacInstruction{{opcode: "select", args: []string{"vnc"}}, {opcode: "connect", args: []string{"VERSION_1_5_0", "h", "p"}}} {
		if err := ws.Write(ctx, websocket.MessageText, encodeGuacInstruction(in)); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("the tunnel ended after %s: %v", time.Since(start).Round(time.Second), err)
		}
		if strings.Contains(string(data), "sync") && time.Since(start) > remoteTargetDialTimeout+2*time.Second {
			return
		}
	}
}

// TestRemoteTargetLimitsOverTheBrowser: the limits of the account's
// client features set read-only, disable-copy, disable-paste and
// disable-audio to true whatever the browser sent, in any letter case of
// guacd's names; without limits the browser's values stay.
func TestRemoteTargetLimitsOverTheBrowser(t *testing.T) {
	target := &db.RemoteTarget{Host: "10.9.9.9", Port: 3389, CredentialMode: "prompt"}
	names := []string{"hostname", "read-only", "Disable-Copy", "disable-paste", "disable-audio", "width"}
	values := []string{"x", "false", "false", "", "false", "1280"}
	for _, c := range []struct {
		name   string
		limits remoteTargetLimits
		want   string
	}{
		{"none", remoteTargetLimits{}, "10.9.9.9|false|false||false|1280"},
		{"read only", remoteTargetLimits{readOnly: true}, "10.9.9.9|true|false||false|1280"},
		{"no clipboard", remoteTargetLimits{noClipboard: true}, "10.9.9.9|false|true|true|false|1280"},
		{"no audio", remoteTargetLimits{noAudio: true}, "10.9.9.9|false|false||true|1280"},
	} {
		if got := strings.Join(rewriteRemoteTargetConnect(values, names, target, "", c.limits), "|"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for query, want := range map[string]remoteTargetLimits{
		"":                                      {},
		"read_only=1":                           {readOnly: true},
		"read_only=true&no_clipboard=0":         {},
		"read_only=1&no_clipboard=1&no_audio=1": {readOnly: true, noClipboard: true, noAudio: true},
	} {
		values, _ := url.ParseQuery(query)
		if got := remoteTargetLimitsOf(values); got != want {
			t.Errorf("%q: %+v, want %+v", query, got, want)
		}
	}
}
