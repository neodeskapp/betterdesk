package api

import (
	"encoding/json"
	"net/http"
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
