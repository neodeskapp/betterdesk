package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestLimitedAccountsWidenNoDeviceAccessOnTheAPIPort: on port 21121 an
// account whose device scope is limited changes no access list, makes no
// tag group and puts no device it can't see into a group, as in the
// console (access.GroupRightsForUser, L390, L425). Each of these let it see
// more devices: here an operator given user.edit, in restricted mode, sees
// PEERAAA1 through the group Office and not PEERBBB2 in the group Lab.
// Accounts that see every device and the console in this process are
// unchanged.
func TestLimitedAccountsWidenNoDeviceAccessOnTheAPIPort(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermUserEdit, true); err != nil {
		t.Fatal(err)
	}
	people := signedIn(t, srv, database)
	create := func(body string) string {
		t.Helper()
		status, answer := asConsole(t, srv, http.MethodPost, "/api/panel/device-groups", body)
		var out map[string]any
		if err := json.Unmarshal([]byte(answer), &out); err != nil || status != http.StatusCreated {
			t.Fatalf("create %s in process: %d %s", body, status, answer)
		}
		return out["guid"].(string)
	}
	office := create(`{"name":"Office","allowed_users":["person_operator"]}`)
	lab := create(`{"name":"Lab"}`)
	srv.SetPanelStore(&mockPanelACLStore{
		restrictedDefault: true,
		groups: []db.PanelDeviceGroup{
			{ID: 1, GUID: office, Name: "Office", AllowedUsers: []string{"person_operator"}},
			{ID: 2, GUID: lab, Name: "Lab"},
		},
		members: map[int64][]string{1: {"PEERAAA1"}, 2: {"PEERBBB2"}},
	})
	operator := people[auth.RoleOperator]

	refused := []struct{ what, method, path, body string }{
		{"puts itself on the list of a group it can't see", http.MethodPut, "/api/panel/device-groups/" + lab + "/acl", `{"allowed_users":["person_operator"]}`},
		{"adds someone to the list of its own group", http.MethodPut, "/api/panel/device-groups/" + office + "/acl", `{"allowed_users":["person_operator","person_viewer"]}`},
		{"sends a list with a group save", http.MethodPost, "/api/panel/device-groups", `{"guid":"` + lab + `","name":"Lab","allowed_users":["person_operator"]}`},
		{"makes a new group with a list", http.MethodPost, "/api/panel/device-groups", `{"name":"Mine","allowed_users":["person_operator"]}`},
		{"turns its group into a tag group", http.MethodPost, "/api/panel/device-groups", `{"guid":"` + office + `","name":"Office","source_type":"tag","tag_filter":"office"}`},
		{"puts a device it can't see into its group", http.MethodPut, "/api/panel/device-groups/" + office + "/peers", `{"peers":["PEERAAA1","PEERBBB2"]}`},
		{"deletes a group with a list", http.MethodDelete, "/api/panel/device-groups/" + office, ""},
	}
	for _, rt := range refused {
		if status, body := listenerCall(t, srv, rt.method, rt.path, rt.body, operator); status != http.StatusForbidden {
			t.Errorf("a limited operator %s (%s %s): %d %s, want 403", rt.what, rt.method, rt.path, status, body)
		}
	}
	for guid, want := range map[string]string{
		lab:    `{"allowed_users":[],"allowed_user_groups":[]}`,
		office: `{"allowed_users":["person_operator"],"allowed_user_groups":[]}`,
	} {
		if status, body := asConsole(t, srv, http.MethodGet, "/api/panel/device-groups/"+guid+"/acl", ""); status != http.StatusOK || body != want {
			t.Errorf("access list of %s after the refused writes: %d %s, want %s", guid, status, body, want)
		}
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/panel/device-groups/"+office+"/peers", ""); status != http.StatusOK || body != `{"guid":"`+office+`","peers":[],"total":0}` {
		t.Errorf("members of Office after the refused writes: %d %s", status, body)
	}

	// What stays: devices it sees into its own group, a name, and every
	// change for accounts that see every device and for the console
	for _, rt := range []struct {
		who                string
		headers            map[string]string
		method, path, body string
	}{
		{"the operator", operator, http.MethodPut, "/api/panel/device-groups/" + office + "/peers", `{"peers":["PEERAAA1"]}`},
		{"the operator", operator, http.MethodPost, "/api/panel/device-groups", `{"guid":"` + office + `","name":"Front office"}`},
		{"global_admin", people[auth.RoleGlobalAdmin], http.MethodPut, "/api/panel/device-groups/" + lab + "/acl", `{"allowed_users":["person_operator"]}`},
		{"global_admin", people[auth.RoleGlobalAdmin], http.MethodPost, "/api/panel/device-groups", `{"guid":"` + lab + `","name":"Lab","source_type":"tag","tag_filter":"lab"}`},
		{"global_admin", people[auth.RoleGlobalAdmin], http.MethodPut, "/api/panel/device-groups/" + lab + "/peers", `{"peers":["PEERAAA1","PEERBBB2"]}`},
	} {
		if status, body := listenerCall(t, srv, rt.method, rt.path, rt.body, rt.headers); status != http.StatusOK {
			t.Errorf("%s: %s %s %s: %d %s, want 200", rt.who, rt.method, rt.path, rt.body, status, body)
		}
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/panel/device-groups/"+office+"/acl", `{"allowed_users":["person_operator","person_viewer"]}`); status != http.StatusOK {
		t.Errorf("PUT acl in process: %d %s, want 200", status, body)
	}
}

// TestLimitedAccountsReadOnlyTheirGroupsOnTheAPIPort: an account that sees
// only part of the devices lists on port 21121 only the device groups open
// to it, reads no member or access list of a group closed to it, and gets
// the folder of no device it can't see, as the console shows it (L425).
func TestLimitedAccountsReadOnlyTheirGroupsOnTheAPIPort(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	people := signedIn(t, srv, database)
	call := func(method, path, body string) string {
		t.Helper()
		status, answer := asConsole(t, srv, method, path, body)
		if status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("%s %s in process: %d %s", method, path, status, answer)
		}
		return answer
	}
	guid := func(answer string) string {
		var out map[string]any
		if err := json.Unmarshal([]byte(answer), &out); err != nil {
			t.Fatal(err)
		}
		return out["guid"].(string)
	}
	office := guid(call(http.MethodPost, "/api/panel/device-groups", `{"name":"Office","allowed_users":["person_operator"]}`))
	boss := guid(call(http.MethodPost, "/api/panel/device-groups", `{"name":"Boss","allowed_users":["person_viewer"]}`))
	call(http.MethodPut, "/api/panel/device-groups/"+office+"/peers", `{"peers":["PEERAAA1"]}`)
	call(http.MethodPut, "/api/panel/device-groups/"+boss+"/peers", `{"peers":["PEERBBB2"]}`)
	call(http.MethodPost, "/api/panel/folders", `{"name":"Desks"}`)
	call(http.MethodPut, "/api/panel/devices/PEERAAA1/folder", `{"folder_id":1}`)
	call(http.MethodPut, "/api/panel/devices/PEERBBB2/folder", `{"folder_id":1}`)
	srv.SetPanelStore(&mockPanelACLStore{
		groups: []db.PanelDeviceGroup{
			{ID: 1, GUID: office, Name: "Office", AllowedUsers: []string{"person_operator"}},
			{ID: 2, GUID: boss, Name: "Boss", AllowedUsers: []string{"person_viewer"}},
		},
		members: map[int64][]string{1: {"PEERAAA1"}, 2: {"PEERBBB2"}},
	})
	operator := people[auth.RoleOperator]

	status, body := listenerCall(t, srv, http.MethodGet, "/api/panel/device-groups", "", operator)
	if status != http.StatusOK || !strings.Contains(body, `"Office"`) || strings.Contains(body, `"Boss"`) || strings.Contains(body, "person_viewer") {
		t.Errorf("the operator's group list: %d %s, want Office without Boss", status, body)
	}
	for _, path := range []string{"/api/panel/device-groups/" + boss + "/peers", "/api/panel/device-groups/" + boss + "/acl"} {
		if status, body := listenerCall(t, srv, http.MethodGet, path, "", operator); status != http.StatusForbidden {
			t.Errorf("GET %s of a group closed to the operator: %d %s, want 403", path, status, body)
		}
	}
	status, body = listenerCall(t, srv, http.MethodGet, "/api/panel/folders", "", operator)
	if status != http.StatusOK || !strings.Contains(body, `"PEERAAA1":1`) || strings.Contains(body, "PEERBBB2") {
		t.Errorf("the operator's folders: %d %s, want PEERAAA1's folder and not PEERBBB2's", status, body)
	}

	// An account that sees every device reads everything
	status, body = listenerCall(t, srv, http.MethodGet, "/api/panel/device-groups", "", people[auth.RoleGlobalAdmin])
	if status != http.StatusOK || !strings.Contains(body, `"Boss"`) {
		t.Errorf("global_admin's group list: %d %s, want Boss", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/panel/device-groups/"+boss+"/peers", "", people[auth.RoleGlobalAdmin]); status != http.StatusOK || !strings.Contains(body, "PEERBBB2") {
		t.Errorf("global_admin reads Boss's members: %d %s", status, body)
	}
}
