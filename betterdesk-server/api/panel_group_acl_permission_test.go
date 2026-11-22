package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// TestPanelGroupAccessListsAskUserEdit: the access list of a device group
// (and of a folder, whose list is that of its folder_<id> group) is
// changed only with user.edit, through the ACL route or the lists of a
// group save; names and members stay with device.edit, also when a save
// sends the stored lists unchanged (RV172, A91).
func TestPanelGroupAccessListsAskUserEdit(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateUser(&db.User{Username: "rv_boss", PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	status, answer := asConsole(t, srv, http.MethodPost, "/api/panel/device-groups", `{"name":"Office","allowed_users":["rv_boss"]}`)
	var created map[string]any
	if err := json.Unmarshal([]byte(answer), &created); err != nil || status != http.StatusCreated {
		t.Fatalf("create in process: %d %s", status, answer)
	}
	guid := created["guid"].(string)
	operator := map[string]string{"X-API-Key": testOperatorKey}
	call := func(method, path, body string) (int, string) {
		return listenerCall(t, srv, method, path, body, operator)
	}
	const refusal = `{"error":"Insufficient permissions"}`

	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/panel/device-groups/" + guid + "/acl", `{"allowed_users":["rv_boss"],"allowed_user_groups":[]}`},
		{http.MethodPost, "/api/panel/device-groups", `{"guid":"` + guid + `","name":"Office","allowed_users":[]}`},
		{http.MethodPost, "/api/panel/device-groups", `{"guid":"` + guid + `","name":"Office","allowed_users_csv":"rv_boss,tadmin"}`},
		{http.MethodPost, "/api/panel/device-groups", `{"name":"Mine","allowed_users":["rv_boss"]}`},
	} {
		if status, body := call(rt.method, rt.path, rt.body); status != http.StatusForbidden || body != refusal {
			t.Errorf("%s %s %s as an operator: %d %s, want 403", rt.method, rt.path, rt.body, status, body)
		}
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/panel/device-groups/"+guid+"/acl", ""); body != `{"allowed_users":["rv_boss"],"allowed_user_groups":[]}` {
		t.Errorf("the access list after the refused writes: %d %s", status, body)
	}

	// Names and members keep device.edit.
	for _, rt := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/panel/device-groups", `{"guid":"` + guid + `","name":"Front office"}`, http.StatusOK},
		{http.MethodPost, "/api/panel/device-groups", `{"guid":"` + guid + `","name":"Front office","allowed_users":[" rv_boss "],"allowed_groups":[]}`, http.StatusOK},
		{http.MethodPut, "/api/panel/device-groups/" + guid + "/peers", `{"peers":["PEERAAA1"]}`, http.StatusOK},
		{http.MethodPost, "/api/panel/device-groups", `{"name":"Open","allowed_users":[]}`, http.StatusCreated},
	} {
		if status, body := call(rt.method, rt.path, rt.body); status != rt.want {
			t.Errorf("%s %s %s as an operator: %d %s, want %d", rt.method, rt.path, rt.body, status, body, rt.want)
		}
	}

	// user.edit granted by an override opens the access lists.
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermUserEdit, true); err != nil {
		t.Fatal(err)
	}
	if status, body := call(http.MethodPut, "/api/panel/device-groups/"+guid+"/acl", `{"allowed_users":["rv_boss","tadmin"]}`); status != http.StatusOK {
		t.Errorf("PUT acl with user.edit granted: %d %s, want 200", status, body)
	}
}

// TestPanelDeletesOfGroupsWithAnAccessListAskUserEdit: deleting a device
// group or a folder whose folder_<id> group has an access list opens its
// devices to everyone, so it asks user.edit; groups and folders without a
// list stay with device.edit (L47).
func TestPanelDeletesOfGroupsWithAnAccessListAskUserEdit(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateUser(&db.User{Username: "rv_boss", PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	create := func(path, body string) map[string]any {
		t.Helper()
		status, answer := asConsole(t, srv, http.MethodPost, path, body)
		var out map[string]any
		if err := json.Unmarshal([]byte(answer), &out); err != nil || status != http.StatusCreated {
			t.Fatalf("POST %s %s in process: %d %s", path, body, status, answer)
		}
		return out
	}
	listed := create("/api/panel/device-groups", `{"name":"Office","allowed_users":["rv_boss"]}`)["guid"].(string)
	plain := create("/api/panel/device-groups", `{"name":"Lab"}`)["guid"].(string)
	folder := fmt.Sprintf("%.0f", create("/api/panel/folders", `{"name":"Office folder"}`)["id"].(float64))
	emptyFolder := fmt.Sprintf("%.0f", create("/api/panel/folders", `{"name":"Lab folder"}`)["id"].(float64))
	if err := database.CreateDeviceGroup(&db.DeviceGroup{GUID: "folder_" + folder, Name: "Office folder", SourceType: "manual"}); err != nil {
		t.Fatal(err)
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/panel/device-groups/folder_"+folder+"/acl", `{"allowed_users":["rv_boss"]}`); status != http.StatusOK {
		t.Fatalf("the folder's access list in process: %d %s", status, body)
	}
	operator := map[string]string{"X-API-Key": testOperatorKey}
	call := func(method, path string) (int, string) {
		return listenerCall(t, srv, method, path, "", operator)
	}
	const refusal = `{"error":"Insufficient permissions"}`

	for _, path := range []string{"/api/panel/device-groups/" + listed, "/api/panel/folders/" + folder} {
		if status, body := call(http.MethodDelete, path); status != http.StatusForbidden || body != refusal {
			t.Errorf("DELETE %s with an access list as an operator: %d %s, want 403", path, status, body)
		}
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/panel/device-groups/"+listed+"/acl", ""); status != http.StatusOK || body != `{"allowed_users":["rv_boss"],"allowed_user_groups":[]}` {
		t.Errorf("the group after the refused delete: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/panel/folders", ""); status != http.StatusOK || !strings.Contains(body, `"Office folder"`) {
		t.Errorf("the folder after the refused delete: %d %s", status, body)
	}
	for _, path := range []string{"/api/panel/device-groups/" + plain, "/api/panel/folders/" + emptyFolder} {
		if status, body := call(http.MethodDelete, path); status != http.StatusOK {
			t.Errorf("DELETE %s without an access list as an operator: %d %s, want 200", path, status, body)
		}
	}

	// user.edit granted by an override deletes them.
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermUserEdit, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/panel/device-groups/" + listed, "/api/panel/folders/" + folder} {
		if status, body := call(http.MethodDelete, path); status != http.StatusOK {
			t.Errorf("DELETE %s with user.edit granted: %d %s, want 200", path, status, body)
		}
	}
}

// TestUserGroupsListAsksUserView: GET /api/user-groups lists the user
// groups only for a caller with user.view; a viewer key gets 403 (L52).
func TestUserGroupsListAsksUserView(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	if status, body := asConsole(t, srv, http.MethodPost, "/api/user-groups", `{"name":"Support"}`); status != http.StatusOK {
		t.Fatalf("create a user group in process: %d %s", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/user-groups", "", map[string]string{"X-API-Key": testViewerKey}); status != http.StatusForbidden || body != `{"error":"Insufficient permissions"}` {
		t.Errorf("GET /api/user-groups as a viewer: %d %s, want 403", status, body)
	}
	status, body := listenerCall(t, srv, http.MethodGet, "/api/user-groups", "", map[string]string{"X-API-Key": testOperatorKey})
	if status != http.StatusOK || !strings.Contains(body, `"name":"Support"`) {
		t.Errorf("GET /api/user-groups as an operator: %d %s, want 200 with the group", status, body)
	}
}
