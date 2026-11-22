package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// TestPanelGroupWritesKeepOutClosedGroups: a caller that a device group's
// or a folder's access list keeps out cannot change, empty or delete it,
// put itself on its list, or move devices into or out of it; open groups
// and the console's in-process identity are unchanged (RV172).
func TestPanelGroupWritesKeepOutClosedGroups(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateUser(&db.User{Username: "rv_boss", PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	create := func(path, body string) map[string]any {
		t.Helper()
		status, answer := asConsole(t, srv, http.MethodPost, path, body)
		var out map[string]any
		if err := json.Unmarshal([]byte(answer), &out); err != nil || status != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", path, status, answer)
		}
		return out
	}
	closed := create("/api/panel/device-groups", `{"name":"Boss","allowed_users":["rv_boss"]}`)["guid"].(string)
	open := create("/api/panel/device-groups", `{"name":"Everyone"}`)["guid"].(string)
	folder := int64(create("/api/panel/folders", `{"name":"Boss folder"}`)["id"].(float64))
	srv.SetPanelStore(&mockPanelACLStore{
		userIDs: map[string]int64{"rv_boss": 7},
		groups: []db.PanelDeviceGroup{
			{ID: 1, GUID: closed, Name: "Boss", AllowedUsers: []string{"rv_boss"}},
			{ID: 2, GUID: open, Name: "Everyone"},
		},
		members:     map[int64][]string{1: {"PEERAAA1"}},
		folderUsers: map[int64][]string{folder: {"rv_boss"}},
	})
	key := map[string]string{"X-API-Key": testOperatorKey}

	refused := []struct{ method, path, body string }{
		{http.MethodPut, "/api/panel/device-groups/" + closed + "/acl", `{"allowed_users":["rv_boss","apikey:operator-key"]}`},
		{http.MethodPut, "/api/panel/device-groups/" + closed + "/peers", `{"peers":[]}`},
		{http.MethodPost, "/api/panel/device-groups", `{"guid":"` + closed + `","name":"Mine","allowed_users":[]}`},
		{http.MethodDelete, "/api/panel/device-groups/" + closed, ""},
		{http.MethodPut, "/api/panel/devices/PEERBBB2/device-group", `{"guid":"` + closed + `","groups":["` + closed + `"]}`},
		{http.MethodPut, "/api/panel/devices/PEERBBB2/device-group", `{"guid":"","groups":["` + closed + `"]}`},
		{http.MethodPut, "/api/panel/devices/PEERAAA1/device-group", `{"guid":"","groups":[]}`},
		{http.MethodPut, fmt.Sprintf("/api/panel/folders/%d", folder), `{"name":"Mine"}`},
		{http.MethodDelete, fmt.Sprintf("/api/panel/folders/%d", folder), ""},
		{http.MethodPut, "/api/panel/devices/PEERBBB2/folder", fmt.Sprintf(`{"folder_id":%d}`, folder)},
	}
	for _, rt := range refused {
		if status, body := listenerCall(t, srv, rt.method, rt.path, rt.body, key); status != http.StatusForbidden {
			t.Errorf("%s %s %s by a caller kept out: %d %s, want 403", rt.method, rt.path, rt.body, status, body)
		}
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/panel/device-groups/"+closed+"/acl", ""); status != http.StatusOK || body != `{"allowed_users":["rv_boss"],"allowed_user_groups":[]}` {
		t.Errorf("the closed group's access list after the refused writes: %d %s", status, body)
	}

	if status, body := listenerCall(t, srv, http.MethodPut, "/api/panel/device-groups/"+open+"/peers", `{"peers":["PEERBBB2"]}`, key); status != http.StatusOK {
		t.Errorf("PUT peers of an open group: %d %s, want 200", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/panel/device-groups/"+closed+"/acl", `{"allowed_users":["rv_boss"]}`); status != http.StatusOK {
		t.Errorf("PUT acl in process: %d %s, want 200", status, body)
	}
}
