package permissions_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// The rows of the launch (BUGS L rows) that the permission routes fixed.

// TestRolesNameTheTicksTheCodeFixes: the role list names, for each role,
// the permissions whose tick has no effect because the code decides them,
// with the rule: a Pro license account never gets device and remote
// target permissions nor assigns devices to organizations, and a role
// that can give no role creates and deletes no account. A super admin
// role, global_admin and a role the code does not know have none
// (BUGS L109).
func TestRolesNameTheTicksTheCodeFixes(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/roles", 200, `{"roles":[{"name":"super_admin"},{"name":"global_admin"},{"name":"operator"},{"name":"guest_issuer"},{"name":"pro"},{"name":"nobody"},"odd"],"all_permissions":[]}`)
	got := routetest.New(t, routetest.Options{API: api}).As("admin").Do("GET", "/api/panel/roles", "")
	pro := `"device.view":"pro_devices","device.connect":"pro_devices","device.edit":"pro_devices","device.delete":"pro_devices","device.ban":"pro_devices",` +
		`"device.change_id":"pro_devices","device.connection_mode":"pro_devices","device.signin_use":"pro_devices","device.signin_manage":"pro_devices",` +
		`"remote_target.view":"pro_devices","remote_target.connect":"pro_devices",` +
		`"remote_target.edit":"pro_devices","remote_target.delete":"pro_devices","remote_target.test":"pro_devices",` +
		`"user.create":"assigns_no_role","user.delete":"assigns_no_role","org.manage_devices":"pro_devices"`
	noRole := `{"user.create":"assigns_no_role","user.delete":"assigns_no_role"}`
	want := `{"success":true,"data":{"roles":[{"name":"super_admin","fixed":{}},{"name":"global_admin","fixed":{}},` +
		`{"name":"operator","fixed":` + noRole + `},{"name":"guest_issuer","fixed":` + noRole + `},` +
		`{"name":"pro","fixed":{` + pro + `}},{"name":"nobody","fixed":{}},"odd"],"all_permissions":[],"requires":` + requires + `}}`
	if got.Status != http.StatusOK || got.Body != want {
		t.Errorf("roles: %d\n got  %s\n want %s", got.Status, got.Body, want)
	}
	if calls := strings.Join(api.Calls(), "\n"); calls != "GET /api/roles" {
		t.Errorf("calls: %s", calls)
	}
}
