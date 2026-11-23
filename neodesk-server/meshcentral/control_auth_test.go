package meshcentral

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// defaultPermissions answers with the built-in role permissions.
type defaultPermissions struct{}

func (defaultPermissions) RoleHasPermission(role, permission string) (bool, error) {
	if auth.IsSuperAdminRole(role) {
		return true, nil
	}
	return auth.RoleHasPermission(role, permission), nil
}

type failingPermissions struct{}

func (failingPermissions) RoleHasPermission(string, string) (bool, error) {
	return false, errors.New("database down")
}

// fakeAgent puts an authenticated agent connection under peerID into the
// gateway and returns the messages the gateway writes to it.
func fakeAgent(t *testing.T, gw *Gateway, peerID, nodeID string) <-chan string {
	t.Helper()
	got := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			got <- string(data)
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	gw.agents.Store(peerID, &AgentConn{gw: gw, conn: conn, peerID: peerID, meshNodeID: nodeID, authenticated: true})
	return got
}

func controlGateway(t *testing.T) (*Gateway, string, *auth.JWTManager) {
	t.Helper()
	gw, srv := newTestGateway(t)
	jm := auth.NewJWTManager("control-test-secret-0123456789abcdef", time.Hour)
	gw.SetJWTManager(jm)
	gw.SetPermissionChecker(defaultPermissions{})
	for _, u := range []*db.User{
		{Username: "anna", PasswordHash: "x", Role: auth.RoleOperator},
		{Username: "vera", PasswordHash: "x", Role: auth.RoleViewer},
		{Username: "devacct", PasswordHash: "x", Role: auth.RoleViewer},
	} {
		if err := gw.db.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	return gw, "ws" + strings.TrimPrefix(srv.URL, "http") + "/control.ashx", jm
}

func dialControl(ctx context.Context, url, token string) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
}

func controlSend(t *testing.T, ctx context.Context, c *websocket.Conn, msg string) {
	t.Helper()
	if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

// controlReadAction reads until a message with the action comes.
func controlReadAction(t *testing.T, ctx context.Context, c *websocket.Conn, action string) map[string]any {
	t.Helper()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read %s: %v", action, err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && m["action"] == action {
			return m
		}
	}
}

func token(t *testing.T, jm *auth.JWTManager, sub, role string) string {
	t.Helper()
	tok, err := jm.Generate(sub, role)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestControlSocketRefusesTokensTheAPIRefuses(t *testing.T) {
	_, url, jm := controlGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for name, tok := range map[string]string{
		"pending second factor": token(t, jm, "anna", pendingSecondFactorRole),
		"device":                token(t, jm, "devacct", auth.RoleDevice),
		"deleted account":       token(t, jm, "ghost", auth.RoleAdmin),
		"other secret":          token(t, auth.NewJWTManager("another-secret-0123456789abcdef", time.Hour), "anna", auth.RoleOperator),
	} {
		c, resp, err := dialControl(ctx, url, tok)
		if err == nil {
			c.CloseNow()
			t.Errorf("%s: socket opened", name)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %v, want 401", name, resp)
		}
	}
}

// TestControlSocketActsWithTheStoredAccount: the socket acts with the role
// the account has now, not the role inside the token; a token issued
// before the account's password changed and an organization token, whose
// subject is no server account, open nothing (RV228).
func TestControlSocketActsWithTheStoredAccount(t *testing.T) {
	gw, url, jm := controlGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// vera is a viewer: an admin claim must not give her a relay cookie
	forged, _, err := dialControl(ctx, url, token(t, jm, "vera", auth.RoleAdmin))
	if err != nil {
		t.Fatalf("viewer with an admin claim: %v", err)
	}
	defer forged.CloseNow()
	controlSend(t, ctx, forged, `{"action":"authcookie"}`)
	controlSend(t, ctx, forged, `{"action":"ping"}`)
	for {
		_, data, err := forged.Read(ctx)
		if err != nil {
			t.Fatalf("viewer with an admin claim: %v", err)
		}
		if strings.Contains(string(data), `"authcookie"`) {
			t.Errorf("a viewer's token with an admin claim got a relay cookie: %s", data)
		}
		if strings.Contains(string(data), `"pong"`) {
			break
		}
	}

	refused := func(what, tok string) {
		t.Helper()
		c, resp, err := dialControl(ctx, url, tok)
		if err == nil {
			c.CloseNow()
			t.Errorf("%s: socket opened", what)
			return
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %v, want 401", what, resp)
		}
	}
	org, err := jm.GenerateOrgToken("anna", auth.RoleAdmin, "org-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	refused("organization token named like a server account", org)

	old := token(t, jm, "anna", auth.RoleOperator)
	if c, _, err := dialControl(ctx, url, old); err != nil {
		t.Fatalf("operator token before the password change: %v", err)
	} else {
		c.CloseNow()
	}
	// The change has to come in a later second than the token
	time.Sleep(time.Until(time.Unix(time.Now().Unix()+1, 0)))
	anna, err := gw.db.GetUser("anna")
	if err != nil || anna == nil {
		t.Fatalf("anna: %v %v", anna, err)
	}
	anna.PasswordHash = "y"
	if err := gw.db.UpdateUser(anna); err != nil {
		t.Fatal(err)
	}
	refused("token issued before the password change", old)
	if c, _, err := dialControl(ctx, url, token(t, jm, "anna", auth.RoleOperator)); err != nil {
		t.Errorf("token issued after the password change: %v", err)
	} else {
		c.CloseNow()
	}
}

func TestControlActionsAskThePermissionOfTheirRoute(t *testing.T) {
	gw, url, jm := controlGateway(t)
	agent := fakeAgent(t, gw, "Mpeer0001", "nodeA")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	viewer, _, err := dialControl(ctx, url, token(t, jm, "vera", auth.RoleViewer))
	if err != nil {
		t.Fatalf("viewer: %v", err)
	}
	defer viewer.CloseNow()
	nodes := controlReadAction(t, ctx, viewer, "nodes")
	if list, _ := nodes["nodes"].([]any); len(list) != 1 {
		t.Fatalf("viewer node list = %v, want the one agent", nodes["nodes"])
	}
	controlSend(t, ctx, viewer, `{"action":"runcommands","nodeid":"node//nodeA","cmds":"touch /tmp/viewer"}`)
	controlSend(t, ctx, viewer, `{"action":"devicepower","nodeid":"node//nodeA","actiontype":3}`)
	controlSend(t, ctx, viewer, `{"action":"authcookie"}`)
	controlSend(t, ctx, viewer, `{"action":"ping"}`)
	for {
		_, data, err := viewer.Read(ctx)
		if err != nil {
			t.Fatalf("viewer: %v", err)
		}
		if strings.Contains(string(data), `"authcookie"`) {
			t.Fatalf("the viewer got a relay cookie: %s", data)
		}
		if strings.Contains(string(data), `"pong"`) {
			break
		}
	}

	op, _, err := dialControl(ctx, url, token(t, jm, "anna", auth.RoleOperator))
	if err != nil {
		t.Fatalf("operator: %v", err)
	}
	defer op.CloseNow()
	controlSend(t, ctx, op, `{"action":"runcommands","nodeid":"node//nodeA","cmds":"echo operator"}`)
	select {
	case msg := <-agent:
		if !strings.Contains(msg, "echo operator") {
			t.Fatalf("first message the agent got = %s, want the operator's command", msg)
		}
	case <-ctx.Done():
		t.Fatal("the operator's command did not reach the agent")
	}
	controlSend(t, ctx, op, `{"action":"authcookie"}`)
	if m := controlReadAction(t, ctx, op, "authcookie"); m["cookie"] == "" {
		t.Fatal("operator got no cookie")
	}
}

func TestControlSocketWithoutPermissionCheckIsClosed(t *testing.T) {
	gw, url, jm := controlGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for name, pc := range map[string]PermissionChecker{"none": nil, "failing": failingPermissions{}} {
		gw.SetPermissionChecker(pc)
		c, resp, err := dialControl(ctx, url, token(t, jm, "anna", auth.RoleOperator))
		if err == nil {
			c.CloseNow()
			t.Errorf("%s: socket opened", name)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: got %v, want 403", name, resp)
		}
	}
}
