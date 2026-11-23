package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

func signedBdMgmtHeaders(t *testing.T, privateKey ed25519.PrivateKey, deviceID string) http.Header {
	t.Helper()
	timestamp := time.Now().UTC().Format(time.RFC3339)
	nonce := fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	payload := bdMgmtSignaturePayload(deviceID, timestamp, nonce)
	signature := ed25519.Sign(privateKey, payload)

	headers := make(http.Header)
	headers.Set("X-ND-Timestamp", timestamp)
	headers.Set("X-ND-Nonce", nonce)
	headers.Set("X-ND-Signature", base64.StdEncoding.EncodeToString(signature))
	return headers
}

func applyHeaders(req *http.Request, headers http.Header) {
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

func TestVerifyBdMgmtRequestAcceptsValidSignature(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "BDM001", PK: pub, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}

	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	req, err := http.NewRequest("GET", "http://example.test/ws/nd-mgmt/BDM001", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyHeaders(req, signedBdMgmtHeaders(t, priv, "BDM001"))

	if err := srv.verifyBdMgmtRequest(req, "BDM001"); err != nil {
		t.Fatalf("verifyBdMgmtRequest returned error: %v", err)
	}
}

func TestVerifyBdMgmtRequestRejectsReplay(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "BDM002", PK: pub, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}

	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	headers := signedBdMgmtHeaders(t, priv, "BDM002")

	firstReq, err := http.NewRequest("GET", "http://example.test/ws/nd-mgmt/BDM002", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyHeaders(firstReq, headers)
	if err := srv.verifyBdMgmtRequest(firstReq, "BDM002"); err != nil {
		t.Fatalf("first verifyBdMgmtRequest returned error: %v", err)
	}

	replayReq, err := http.NewRequest("GET", "http://example.test/ws/nd-mgmt/BDM002", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyHeaders(replayReq, headers)
	if err := srv.verifyBdMgmtRequest(replayReq, "BDM002"); err == nil {
		t.Fatal("expected replayed request to be rejected")
	}
}

func TestBdMgmtWebSocketRequiresProof(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.APIPort = 19886
	database := testSetupDB(t)
	defer database.Close()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "BDM003", PK: pub, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}

	srv := New(cfg, database, peer.NewMap(), nil, "test")
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws/nd-mgmt/BDM003", cfg.APIPort)

	unauthConn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		unauthConn.Close(websocket.StatusPolicyViolation, "expected unauthorized")
		t.Fatal("expected unsigned management websocket to be rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected 401 for unsigned websocket, got %d (err=%v)", status, err)
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: signedBdMgmtHeaders(t, priv, "BDM003"),
	})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected signed websocket to connect, got status %d: %v", status, err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

// A management socket is in the audit log as nd_mgmt_connect when it
// opens and as nd_mgmt_disconnect when it closes (L301).
func TestBdMgmtAuditEventsSayNd(t *testing.T) {
	f := newInternalFixture(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpsertPeer(&db.Peer{ID: "BDML301", PK: pub, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+f.host+"/ws/nd-mgmt/BDML301", &websocket.DialOptions{
		HTTPHeader: signedBdMgmtHeaders(t, priv, "BDML301"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil { // welcome
		t.Fatal(err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")

	// The events of the device, oldest first, once the socket's cleanup ran
	var got []string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		got = got[:0]
		events := f.srv.auditLog.RecentByAction(audit.ActionPeerUpdated, 50)
		for i := len(events) - 1; i >= 0; i-- {
			if e := events[i]; e.Target == "BDML301" {
				got = append(got, e.Details["event"]+" "+e.Details["auth"])
			}
		}
		if len(got) >= 2 {
			break
		}
	}
	if want := []string{"nd_mgmt_connect device_signature", "nd_mgmt_disconnect device_signature"}; strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("audit events of the socket: %q, want %q", got, want)
	}
}

// TestBdMgmtReconnectStaysConnected: when a desktop client opens a second
// management socket, the first one's cleanup leaves the second in the hub,
// so the device is listed and messages reach it (RV183).
func TestBdMgmtReconnectStaysConnected(t *testing.T) {
	f := newInternalFixture(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpsertPeer(&db.Peer{ID: "BDMRV183", PK: pub, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	open := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, "ws://"+f.host+"/ws/nd-mgmt/BDMRV183", &websocket.DialOptions{
			HTTPHeader: signedBdMgmtHeaders(t, priv, "BDMRV183"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(ctx); err != nil { // welcome
			t.Fatal(err)
		}
		return conn
	}
	first := open()
	defer first.CloseNow()
	second := open()
	defer second.CloseNow()

	// The first socket is closed as replaced; give its cleanup time to run.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, err := first.Read(ctx); err != nil {
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	if !mgmtHub.IsConnected("BDMRV183") {
		t.Fatal("the device is not listed while its second socket is open")
	}
	if a := f.overListener(t, http.MethodPost, "/api/nd/mgmt/BDMRV183/send", `{"type":"ping"}`, testOperatorKey); a.status != http.StatusOK {
		t.Errorf("send to the reconnected device: %d %s, want 200", a.status, a.body)
	}
}
