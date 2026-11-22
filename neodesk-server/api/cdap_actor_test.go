package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/cdap"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	eventsModule "github.com/unitronix/betterdesk-server/events"
	"github.com/unitronix/betterdesk-server/peer"
)

// cdapDevice is a CDAP device connected to a gateway the test started: it
// signed in with a device token and registered one button.
type cdapDevice struct {
	t    *testing.T
	id   string
	conn *websocket.Conn
}

// connectCDAPDevice starts a CDAP gateway on database and connects a
// device to it.
func connectCDAPDevice(t *testing.T, database db.Database) (*cdap.Gateway, *cdapDevice) {
	t.Helper()
	const id, token = "CDAPACT1", "cdap-actor-token-0123456789"
	if err := database.UpsertPeer(&db.Peer{ID: id, Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if err := database.CreateDeviceToken(&db.DeviceToken{Token: token, TokenHash: hex.EncodeToString(sum[:]), Name: "cdap", PeerID: id, Status: db.TokenStatusActive}); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.CDAPPort = freePort(t)
	gw := cdap.New(cfg, database, peer.NewMap(), eventsModule.NewBus())
	gw.SetJWTManager(auth.NewJWTManager("cdap-actor-test-secret", time.Hour))
	if err := gw.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/cdap", cfg.CDAPPort), &websocket.DialOptions{Subprotocols: []string{"cdap-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	d := &cdapDevice{t: t, id: id, conn: conn}
	d.send("auth", map[string]string{"method": "device_token", "token": token, "device_id": id})
	d.expect("auth_result")
	d.send("register", map[string]any{"manifest": map[string]any{
		"device":       map[string]string{"name": "Actor test", "type": "iot"},
		"capabilities": []string{"commands"},
		"widgets":      []map[string]any{{"type": "button", "id": "b1", "label": "Go", "permissions": map[string]string{"control": "admin"}}},
	}})
	d.expect("registered")
	return gw, d
}

func (d *cdapDevice) send(typ string, payload any) {
	d.t.Helper()
	raw, _ := json.Marshal(payload)
	msg, _ := json.Marshal(cdap.Message{Type: typ, Payload: raw})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.conn.Write(ctx, websocket.MessageText, msg); err != nil {
		d.t.Fatalf("send %s: %v", typ, err)
	}
}

// expect reads messages until one of the type comes and returns its
// payload.
func (d *cdapDevice) expect(typ string) map[string]any {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, raw, err := d.conn.Read(ctx)
		if err != nil {
			d.t.Fatalf("waiting for %s: %v", typ, err)
		}
		var msg cdap.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			d.t.Fatalf("message %s: %v", raw, err)
		}
		if msg.Type == "error" {
			d.t.Fatalf("waiting for %s: %s", typ, msg.Payload)
		}
		if msg.Type != typ {
			continue
		}
		out := map[string]any{}
		_ = json.Unmarshal(msg.Payload, &out)
		return out
	}
}

// TestCDAPRecordsTheActor: a command and a terminal session that an
// in-process call starts for the console's signed-in account
// (Identity.Actor) name that account as their operator to the device. The
// delegations still go by the caller's username, so naming an account
// that holds a delegation gains nothing (BUGS G7).
func TestCDAPRecordsTheActor(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	gw, device := connectCDAPDevice(t, database)
	srv.SetCDAPGateway(gw)
	h := srv.InternalHandler()
	withIdentity := func(id Identity) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
	command := func(id Identity, action string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/cdap/devices/"+device.id+"/command", strings.NewReader(`{"widget_id":"b1","action":"`+action+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		withIdentity(id).ServeHTTP(rec, req)
		return rec.Code
	}

	bridge := Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin, Actor: "alice"}
	if status := command(bridge, "trigger"); status != http.StatusAccepted {
		t.Fatalf("command: %d", status)
	}
	if got := device.expect("command"); got["operator"] != "alice" {
		t.Errorf("the command's operator: %v, want alice", got["operator"])
	}

	srvHTTP := httptest.NewServer(withIdentity(bridge))
	t.Cleanup(srvHTTP.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	browser, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srvHTTP.URL, "http")+"/api/cdap/devices/"+device.id+"/terminal", &websocket.DialOptions{Subprotocols: []string{"cdap-terminal"}})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()
	if err := browser.Write(ctx, websocket.MessageText, []byte(`{"cols":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	if got := device.expect("terminal_start"); got["operator_name"] != "alice" {
		t.Errorf("the terminal's operator: %v, want alice", got["operator_name"])
	}

	// The button asks admin for a trigger. An operator that names alice,
	// who holds an admin delegation, stays an operator; its own delegation
	// counts.
	operator := Identity{Username: "apikey:operator", Role: auth.RoleOperator, Actor: "alice"}
	delegate := func(grantee string) {
		t.Helper()
		if err := gw.Delegations().Add(&cdap.Delegation{ID: "d-" + grantee, Grantor: "admin", Grantee: grantee, DeviceID: device.id, Role: "admin", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	delegate("alice")
	if status := command(operator, "trigger"); status != http.StatusForbidden {
		t.Errorf("an operator naming alice, who holds a delegation: %d, want 403", status)
	}
	delegate(operator.Username)
	if status := command(operator, "trigger"); status != http.StatusAccepted {
		t.Errorf("an operator with its own delegation: %d, want 202", status)
	}
	if got := device.expect("command"); got["operator"] != "alice" {
		t.Errorf("the delegated command's operator: %v, want alice", got["operator"])
	}
}
