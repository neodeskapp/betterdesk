package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/meshcentral"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestControlSocketOnTheAPIPortAsksTheAPIsPermissions: on the API port
// /control.ashx opens for a full token of a role that holds device.view,
// as the API's own routes decide it (stored overrides included), and stays
// shut for a pending second factor, a device token and a role whose
// override takes device.view away (RV155).
func TestControlSocketOnTheAPIPortAsksTheAPIsPermissions(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	// The gateway writes its agent certificate into the working directory
	t.Chdir(t.TempDir())
	jm := auth.NewJWTManager("control-port-secret-0123456789abcdef", time.Hour)
	srv.SetJWTManager(jm)
	gw, err := meshcentral.NewGateway(srv.cfg, database, peer.NewMap(), eventsModule.NewBus(), "test-jwt-secret-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Stop)
	gw.SetJWTManager(jm)
	srv.SetMeshGateway(gw)
	for _, u := range []*db.User{
		{Username: "anna", PasswordHash: "x", Role: auth.RoleOperator},
		{Username: "vera", PasswordHash: "x", Role: auth.RoleViewer},
	} {
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(srv.ListenerHandler())
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/control.ashx"

	token := func(sub, role string) string {
		t.Helper()
		tok, err := jm.Generate(sub, role)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	dial := func(tok string) int {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}},
		})
		if err == nil {
			conn.CloseNow()
			return http.StatusSwitchingProtocols
		}
		if resp == nil {
			t.Fatalf("dial: %v", err)
		}
		return resp.StatusCode
	}

	if got := dial(token("anna", auth.RoleOperator)); got != http.StatusSwitchingProtocols {
		t.Errorf("operator token: %d, want the socket open", got)
	}
	if got := dial(token("vera", auth.RoleViewer)); got != http.StatusSwitchingProtocols {
		t.Errorf("viewer token: %d, want the socket open", got)
	}
	for name, tok := range map[string]string{
		"pending second factor": token("anna", "__2fa_pending__"),
		"device token":          token("anna", auth.RoleDevice),
	} {
		if got := dial(tok); got == http.StatusSwitchingProtocols {
			t.Errorf("%s: socket opened, want it refused", name)
		}
	}
	if err := database.SetRolePermission(auth.RoleViewer, auth.PermDeviceView, false); err != nil {
		t.Fatal(err)
	}
	if got := dial(token("vera", auth.RoleViewer)); got != http.StatusForbidden {
		t.Errorf("viewer without device.view by override: %d, want 403", got)
	}
}
