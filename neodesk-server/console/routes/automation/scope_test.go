package automation_test

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestCommandsNeedDeviceConnectAndTheScope: the three command routes ask
// device.connect besides server.config, so a server admin keeps the rules
// and the SMTP settings but sends and reads no commands; an account with
// both permissions and a limited device scope sends commands only to the
// devices of its scope and sees only their commands (BUGS RV32, A80).
func TestCommandsNeedDeviceConnectAndTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		restricted := func(cfg *config.Config) { cfg.DeviceScopeDefault = "restricted" }
		w := replay.NewWorldWithAPI(t, db, restricted, replay.RoleOverrides{"operator": {"server.config": true}}, knownDevices)
		if _, err := db.Exec(context.Background(), "INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, 'D1')", replay.Users["operator"].ID); err != nil {
			t.Fatal(err)
		}
		do := func(role, method, path, body string) replay.Answer {
			req := replay.Request{Role: role, Method: method, Path: path}
			if body != "" {
				req.Body = replay.Text(body)
			}
			return w.Do(req)
		}
		for _, device := range []string{"D1", "D2"} {
			if got := do("admin", "POST", "/api/automation/commands", `{"device_id":"`+device+`","payload":"whoami"}`); got.Status != 201 {
				t.Fatalf("the admin's command for %s: %d %s", device, got.Status, got.Body)
			}
		}

		const denied = `{"success":false,"error":"Permission denied: device.connect"}`
		for _, c := range []struct{ method, path, body string }{
			{"POST", "/api/automation/commands", `{"device_id":"D1","payload":"id"}`},
			{"GET", "/api/automation/commands", ""},
			{"GET", "/api/automation/commands/1", ""},
		} {
			if got := do("server_admin", c.method, c.path, c.body); got.Status != 403 || got.Body != denied {
				t.Errorf("%s %s as server_admin: %d %s, want 403 %s", c.method, c.path, got.Status, got.Body, denied)
			}
		}
		for _, path := range []string{"/api/automation/rules", "/api/automation/smtp"} {
			if got := do("server_admin", "GET", path, ""); got.Status != 200 {
				t.Errorf("GET %s as server_admin: %d %s, want 200", path, got.Status, got.Body)
			}
		}

		if got := do("operator", "POST", "/api/automation/commands", `{"device_id":"D2","payload":"id"}`); got.Status != 403 || got.Body != `{"error":"Device is outside your device scope"}` {
			t.Errorf("the operator's command for D2: %d %s", got.Status, got.Body)
		}
		if got := do("operator", "POST", "/api/automation/commands", `{"device_id":"D1","payload":"id"}`); got.Status != 201 {
			t.Errorf("the operator's command for D1: %d %s", got.Status, got.Body)
		}
		if got := do("operator", "GET", "/api/automation/commands/2", ""); got.Status != 404 || got.Body != `{"error":"Command not found"}` {
			t.Errorf("the command for D2 as the operator: %d %s", got.Status, got.Body)
		}
		if got := do("operator", "GET", "/api/automation/commands/1", ""); got.Status != 200 || !strings.Contains(got.Body, `"device_id":"D1"`) {
			t.Errorf("the command for D1 as the operator: %d %s", got.Status, got.Body)
		}
		devicesOf := func(body string) string {
			var ids []string
			for _, part := range strings.Split(body, `"device_id":"`)[1:] {
				id, _, _ := strings.Cut(part, `"`)
				ids = append(ids, id)
			}
			return strings.Join(ids, ",")
		}
		if got := do("operator", "GET", "/api/automation/commands", ""); got.Status != 200 || devicesOf(got.Body) != "D1,D1" {
			t.Errorf("the operator's list: %d %s, want the commands of D1 alone", got.Status, got.Body)
		}
		if got := do("admin", "GET", "/api/automation/commands", ""); got.Status != 200 || devicesOf(got.Body) != "D1,D2,D1" {
			t.Errorf("the admin's list: %d %s, want every command", got.Status, got.Body)
		}
		if got := w.Rows("SELECT created_by, device_id FROM remote_commands ORDER BY id", nil); got != `[{"created_by":"e2e_admin","device_id":"D1"},{"created_by":"e2e_admin","device_id":"D2"},{"created_by":"e2e_operator","device_id":"D1"}]` {
			t.Errorf("the stored commands: %s", got)
		}
	})
}
