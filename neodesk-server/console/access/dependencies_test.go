package access

import (
	"reflect"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// TestPermissionNeedsNameWhatTheCodeMakesTrue pins the table: a change to
// it is a change to what the permissions page tells an admin.
func TestPermissionNeedsNameWhatTheCodeMakesTrue(t *testing.T) {
	view := []PermissionNeed{{"device.view", "devices_page"}}
	want := map[string][]PermissionNeed{
		"guest.create":           view,
		"device.edit":            view,
		"device.delete":          view,
		"device.ban":             view,
		"device.change_id":       view,
		"device.connection_mode": view,
		"device.signin_use":      {{"device.connect", "signin_use_connects"}},
		"device.signin_manage":   view,
		"remote_target.connect":  view,
		"remote_target.edit":     view,
		"remote_target.delete":   view,
		"remote_target.test":     view,
		"user.create":            {{"user.view", "users_page"}},
		"user.delete":            {{"user.view", "users_page"}},
		"billing.export":         {{"billing.view", "billing_page"}},
		"cdap.terminal":          {{"cdap.command", "cdap_terminal_command"}},
		"mesh.terminal":          view,
		"mesh.files":             view,
		"mesh.power":             view,
		"agent.files":            view,
	}
	if got := PermissionNeeds(); !reflect.DeepEqual(got, want) {
		t.Errorf("permission needs:\n got  %v\n want %v", got, want)
	}
}

// TestPermissionNeedsAreKnownAndAcyclic: every name in the table is a
// permission the server knows, none needs itself, a needed permission
// needs nothing more (so turning one on is enough), and the table handed
// out is a copy.
func TestPermissionNeedsAreKnownAndAcyclic(t *testing.T) {
	needs := PermissionNeeds()
	for p, list := range needs {
		if !auth.ValidPermission(p) {
			t.Errorf("%s is no permission", p)
		}
		for _, n := range list {
			if !auth.ValidPermission(n.Permission) || n.Permission == p || n.Reason == "" {
				t.Errorf("%s needs %+v", p, n)
			}
			if len(needs[n.Permission]) != 0 {
				t.Errorf("%s needs %s, which needs more itself", p, n.Permission)
			}
		}
	}
	needs["device.edit"][0].Permission = "changed"
	if PermissionNeeds()["device.edit"][0].Permission != "device.view" {
		t.Error("the table handed out is the table itself")
	}
}
