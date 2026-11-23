package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/cdap"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestCDAPRegistrationAsksTheAPIsPermission: the API server gives the CDAP
// gateway its permission check, so an API key registers a device on the
// CDAP port only while its role holds enrollment.approve, overrides
// included (RV157, A95).
func TestCDAPRegistrationAsksTheAPIsPermission(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	cfg := config.DefaultConfig()
	cfg.CDAPPort = freePort(t)
	gw := cdap.New(cfg, database, peer.NewMap(), eventsModule.NewBus())
	gw.SetJWTManager(auth.NewJWTManager("cdap-enroll-test-secret", time.Hour))
	srv.SetCDAPGateway(gw)
	if err := gw.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Stop)

	signIn := func() string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/cdap", cfg.CDAPPort), &websocket.DialOptions{Subprotocols: []string{"cdap-v1"}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		payload, _ := json.Marshal(map[string]string{"method": "api_key", "key": testOperatorKey, "device_id": "plc-9"})
		msg, _ := json.Marshal(cdap.Message{Type: "auth", Payload: payload})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	if answer := signIn(); !strings.Contains(answer, `"type":"error"`) || !strings.Contains(answer, "enrollment.approve") {
		t.Errorf("an operator's API key: %s, want the enrollment.approve refusal", answer)
	}
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermEnrollmentApprove, true); err != nil {
		t.Fatal(err)
	}
	if answer := signIn(); !strings.Contains(answer, `"type":"auth_result"`) {
		t.Errorf("an operator's API key with enrollment.approve granted: %s, want auth_result", answer)
	}
}
