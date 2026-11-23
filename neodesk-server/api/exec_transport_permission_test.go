package api

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// TestUnifiedExecAsksTheTransportsTerminalPermission: POST
// /api/peers/{id}/exec on a device connected over CDAP asks cdap.terminal,
// not mesh.terminal, and the role map with its overrides decides (RV159,
// RV170, A96).
func TestUnifiedExecAsksTheTransportsTerminalPermission(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	gw, device := connectCDAPDevice(t, database)
	srv.SetCDAPGateway(gw)
	path := "/api/peers/" + device.id + "/exec"
	key := map[string]string{"X-API-Key": testViewerKey}
	set := func(perm string, granted bool) {
		t.Helper()
		if err := database.SetRolePermission(auth.RoleViewer, perm, granted); err != nil {
			t.Fatal(err)
		}
	}

	set(auth.PermCDAPTerminal, true)
	if status, body := listenerCall(t, srv, http.MethodPost, path, `{"command":"whoami"}`, key); status != http.StatusOK || body != `{"status":"sent","transport":"cdap"}` {
		t.Fatalf("exec on a CDAP device with cdap.terminal: %d %s, want 200 sent over cdap", status, body)
	}
	if got := device.expect("command"); got["widget_id"] != "shell" || got["action"] != "run" {
		t.Errorf("the device got %v, want the shell run command", got)
	}

	set(auth.PermCDAPTerminal, false)
	set(auth.PermMeshTerminal, true)
	if status, body := listenerCall(t, srv, http.MethodPost, path, `{"command":"whoami"}`, key); status != http.StatusForbidden || body != `{"error":"Insufficient permissions"}` {
		t.Errorf("exec on a CDAP device with mesh.terminal only: %d %s, want 403", status, body)
	}

	// Neither terminal permission: refused before anything about the device.
	set(auth.PermMeshTerminal, false)
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/peers/PEERAAA1/exec", `{"command":"whoami"}`, key); status != http.StatusForbidden || body != `{"error":"Insufficient permissions"}` {
		t.Errorf("exec without a terminal permission: %d %s, want 403", status, body)
	}
}
