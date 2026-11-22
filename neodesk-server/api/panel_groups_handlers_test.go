package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

// panelGroupTestServer starts a server on a free-ish port with an admin API key.
func panelGroupTestServer(t *testing.T, port int) (*Server, string) {
	t.Helper()
	cfg := config.DefaultConfig()
	database := testSetupDB(t)
	t.Cleanup(func() { database.Close() })

	if err := database.CreateUser(&db.User{
		Username:     "operator-grp",
		PasswordHash: "hash",
		Role:         auth.RoleOperator,
		AuthProvider: db.AuthProviderLocal,
	}); err != nil {
		t.Fatal(err)
	}

	cfg.APIPort = port
	srv := New(cfg, database, peer.NewMap(), nil, "1.0.0-test")
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })
	time.Sleep(120 * time.Millisecond)
	return srv, fmt.Sprintf("http://127.0.0.1:%d", port)
}

func doPanelJSON(t *testing.T, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var parsed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp, parsed
}

func TestPanelGroupLifecycle(t *testing.T) {
	_, base := panelGroupTestServer(t, 19896)

	// Create a group with an ACL.
	resp, body := doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{
		"name":           "Support team",
		"note":           "created by test",
		"source_type":    "manual",
		"allowed_users":  []string{"operator-grp"},
		"allowed_groups": []string{},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, want 201 (%v)", resp.StatusCode, body)
	}
	guid, _ := body["guid"].(string)
	if guid == "" {
		t.Fatal("create response has no guid")
	}

	// The list reflects it, with the ACL and member count.
	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/device-groups", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d", resp.StatusCode)
	}
	groups, _ := body["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("groups = %v, want 1", groups)
	}
	first := groups[0].(map[string]any)
	if first["name"] != "Support team" {
		t.Errorf("name = %v", first["name"])
	}
	if users, _ := first["allowed_users"].([]any); len(users) != 1 {
		t.Errorf("allowed_users = %v, want 1", first["allowed_users"])
	}

	// Membership: replace and read back.
	resp, body = doPanelJSON(t, http.MethodPut, base+"/api/panel/device-groups/"+guid+"/peers", map[string]any{
		"peers": []string{"PEER1", "PEER2", "PEER1"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put peers status %d (%v)", resp.StatusCode, body)
	}
	if total, _ := body["total"].(float64); int(total) != 2 {
		t.Errorf("peer total = %v, want 2 (duplicates dropped)", body["total"])
	}

	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/device-groups/"+guid+"/peers", nil)
	peers, _ := body["peers"].([]any)
	if resp.StatusCode != http.StatusOK || len(peers) != 2 {
		t.Fatalf("get peers status %d peers %v", resp.StatusCode, peers)
	}

	// ACL: replace and read back.
	resp, body = doPanelJSON(t, http.MethodPut, base+"/api/panel/device-groups/"+guid+"/acl", map[string]any{
		"allowed_users":       []string{"operator-grp"},
		"allowed_user_groups": []string{},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put acl status %d (%v)", resp.StatusCode, body)
	}
	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/device-groups/"+guid+"/acl", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get acl status %d", resp.StatusCode)
	}
	if users, _ := body["allowed_users"].([]any); len(users) != 1 {
		t.Errorf("acl users = %v", body["allowed_users"])
	}

	// Delete removes the group; membership and ACL go with it.
	resp, body = doPanelJSON(t, http.MethodDelete, base+"/api/panel/device-groups/"+guid, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d (%v)", resp.StatusCode, body)
	}
	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/device-groups", nil)
	if groups, _ := body["groups"].([]any); len(groups) != 0 {
		t.Errorf("groups after delete = %v, want none", groups)
	}
}

func TestPanelGroupRejectsInvalidInput(t *testing.T) {
	_, base := panelGroupTestServer(t, 19897)

	resp, _ := doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{
		"name": "   ",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty name status %d, want 400", resp.StatusCode)
	}

	resp, _ = doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", "not-an-object")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid JSON status %d, want 400", resp.StatusCode)
	}

	resp, _ = doPanelJSON(t, http.MethodGet, base+"/api/panel/device-groups/missing-guid/peers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown group peers status %d, want 200 with an empty list", resp.StatusCode)
	}

	resp, _ = doPanelJSON(t, http.MethodDelete, base+"/api/panel/device-groups/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete unknown status %d, want 404", resp.StatusCode)
	}
}

func TestPanelFoldersLifecycle(t *testing.T) {
	_, base := panelGroupTestServer(t, 19898)

	resp, body := doPanelJSON(t, http.MethodPost, base+"/api/panel/folders", map[string]any{
		"name": "Customers",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create folder status %d (%v)", resp.StatusCode, body)
	}
	id, _ := body["id"].(float64)
	if id <= 0 {
		t.Fatalf("folder id = %v", body["id"])
	}

	// Assign a device, then read assignments back through the list endpoint.
	resp, body = doPanelJSON(t, http.MethodPut, base+"/api/panel/devices/PEER9/folder", map[string]any{
		"folder_id": int64(id),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign status %d (%v)", resp.StatusCode, body)
	}

	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/folders", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list folders status %d", resp.StatusCode)
	}
	assignments, _ := body["assignments"].(map[string]any)
	if len(assignments) != 1 {
		t.Fatalf("assignments = %v, want one", assignments)
	}

	// Clearing the folder removes the assignment but keeps the folder.
	resp, _ = doPanelJSON(t, http.MethodPut, base+"/api/panel/devices/PEER9/folder", map[string]any{"folder_id": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear status %d", resp.StatusCode)
	}
	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/folders", nil)
	if assignments, _ := body["assignments"].(map[string]any); len(assignments) != 0 {
		t.Errorf("assignments after clear = %v", assignments)
	}

	// Rename, then delete.
	resp, _ = doPanelJSON(t, http.MethodPut, fmt.Sprintf("%s/api/panel/folders/%d", base, int64(id)), map[string]any{
		"name": "Customers 2026", "sort_order": 3,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename status %d", resp.StatusCode)
	}
	resp, _ = doPanelJSON(t, http.MethodDelete, fmt.Sprintf("%s/api/panel/folders/%d", base, int64(id)), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	resp, body = doPanelJSON(t, http.MethodGet, base+"/api/panel/folders", nil)
	if folders, _ := body["folders"].([]any); len(folders) != 0 {
		t.Errorf("folders after delete = %v", folders)
	}
}

func TestPanelUserGrants(t *testing.T) {
	srv, base := panelGroupTestServer(t, 19899)
	operator, err := srv.db.GetUser("operator-grp")
	if err != nil || operator == nil {
		t.Fatalf("GetUser: %v", err)
	}

	resp, body := doPanelJSON(t, http.MethodPut,
		fmt.Sprintf("%s/api/panel/users/%d/grants", base, operator.ID),
		map[string]any{"peers": []string{"PEER-A", "PEER-B"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put grants status %d (%v)", resp.StatusCode, body)
	}
	if total, _ := body["total"].(float64); int(total) != 2 {
		t.Errorf("grants total = %v", body["total"])
	}

	resp, body = doPanelJSON(t, http.MethodGet,
		fmt.Sprintf("%s/api/panel/users/%d/grants", base, operator.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get grants status %d", resp.StatusCode)
	}
	if peers, _ := body["peers"].([]any); len(peers) != 2 {
		t.Errorf("grants = %v", body["peers"])
	}

	// An invalid id is refused, not treated as user 0.
	resp, _ = doPanelJSON(t, http.MethodGet, base+"/api/panel/users/0/grants", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid id status %d, want 400", resp.StatusCode)
	}
}

// TestPanelGroupRoutesRequirePermission proves the endpoints are not open to a
// session without device.edit, so the new writes cannot become a privilege
// escalation path.
func TestPanelGroupRoutesRequirePermission(t *testing.T) {
	srv, base := panelGroupTestServer(t, 19900)
	if err := srv.db.SetRolePermission(auth.RoleOperator, auth.PermDeviceEdit, false); err != nil {
		t.Fatal(err)
	}
	key := "operator-key-900"
	if err := srv.db.CreateAPIKey(&db.APIKey{
		KeyHash:   hashAPIKey(key),
		KeyPrefix: key[:8],
		Name:      "operator-900",
		Role:      auth.RoleOperator,
	}); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, base+"/api/panel/device-groups",
		bytes.NewReader([]byte(`{"name":"nope"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403 without device.edit", resp.StatusCode)
	}
}

// TestPanelGroupUpdateKeepsNoteAndTeam: an update whose body leaves out
// the note and the team id keeps the stored ones, as the console's own
// writer did; one that names them writes them (BUGS UM31).
func TestPanelGroupUpdateKeepsNoteAndTeam(t *testing.T) {
	srv, base := panelGroupTestServer(t, 19930)
	resp, body := doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{
		"name": "Support", "note": "A note", "team_id": "team-1",
	})
	guid, _ := body["guid"].(string)
	if resp.StatusCode != http.StatusCreated || guid == "" {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}
	stored := func() string {
		t.Helper()
		g, err := srv.db.GetDeviceGroup(guid)
		if err != nil || g == nil {
			t.Fatalf("GetDeviceGroup: %v %v", g, err)
		}
		return g.Name + "|" + g.Note + "|" + g.TeamID
	}
	if resp, body = doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{"guid": guid, "name": "Support 2"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %v", resp.StatusCode, body)
	}
	if got := stored(); got != "Support 2|A note|team-1" {
		t.Errorf("after an update without note and team id: %q", got)
	}
	if resp, body = doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{"guid": guid, "name": "Support 3", "note": "", "team_id": "team-2"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %v", resp.StatusCode, body)
	}
	if got := stored(); got != "Support 3||team-2" {
		t.Errorf("after an update with an empty note and another team: %q", got)
	}
}

// TestPanelGroupSaveAnswersTheStoredAccessList: the answer of a save lists
// the accounts and user groups the access list stored, not the request's:
// names of no account and guids of no user group are left out, in the
// stored order (BUGS UM32).
func TestPanelGroupSaveAnswersTheStoredAccessList(t *testing.T) {
	srv, base := panelGroupTestServer(t, 19931)
	group := &db.UserGroup{Name: "Ops"}
	if err := srv.db.CreateUserGroup(group); err != nil {
		t.Fatal(err)
	}
	resp, body := doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{
		"name":           "Support",
		"allowed_users":  []string{"nobody-has-this-name", "operator-grp", " operator-grp "},
		"allowed_groups": []string{"no-such-user-group", group.GUID},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}
	if got := fmt.Sprint(body["allowed_users"], body["allowed_groups"]); got != fmt.Sprint([]any{"operator-grp"}, []any{group.GUID}) {
		t.Errorf("answered access list: %s", got)
	}
	resp, body = doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{
		"guid": body["guid"], "name": "Support", "allowed_users": []string{"nobody-has-this-name"}, "allowed_groups": []string{},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %v", resp.StatusCode, body)
	}
	if got := fmt.Sprint(body["allowed_users"], body["allowed_groups"]); got != "[] []" {
		t.Errorf("answered access list after an update with an unknown name: %s", got)
	}
}

// TestPanelGroupUpdateKeepsAccessListsItLeavesOut: an update whose body
// leaves an access list out keeps the stored one, as the console's own
// writer did; a list the body sends is written, an empty one clears it, in
// either of the two forms (BUGS DGR7).
func TestPanelGroupUpdateKeepsAccessListsItLeavesOut(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	for _, name := range []string{"op-a", "op-b"} {
		if err := srv.db.CreateUser(&db.User{Username: name, PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
			t.Fatal(err)
		}
	}
	group := &db.UserGroup{Name: "Ops"}
	if err := srv.db.CreateUserGroup(group); err != nil {
		t.Fatal(err)
	}
	status, answer := asConsole(t, srv, http.MethodPost, "/api/panel/device-groups",
		`{"name":"Support","allowed_users":["op-a"],"allowed_groups":["`+group.GUID+`"]}`)
	var created struct{ GUID string }
	if err := json.Unmarshal([]byte(answer), &created); status != http.StatusCreated || err != nil {
		t.Fatalf("create: %d %s", status, answer)
	}
	stored := func() string {
		t.Helper()
		acl, err := srv.db.(db.PanelGroupStore).GetDeviceGroupACL(created.GUID)
		if err != nil || acl == nil {
			t.Fatalf("GetDeviceGroupACL: %v %v", acl, err)
		}
		return fmt.Sprint(acl.AllowedUsers, acl.AllowedUserGroups)
	}
	for _, c := range []struct{ lists, want string }{
		{``, "[op-a] [" + group.GUID + "]"},
		{`,"allowed_users":["op-b"]`, "[op-b] [" + group.GUID + "]"},
		{`,"allowed_groups_csv":""`, "[op-b] []"},
		{`,"allowed_users_csv":"op-a,op-b"`, "[op-a op-b] []"},
		{`,"allowed_users":[]`, "[] []"},
	} {
		body := `{"guid":"` + created.GUID + `","name":"Support"` + c.lists + `}`
		if status, answer := asConsole(t, srv, http.MethodPost, "/api/panel/device-groups", body); status != http.StatusOK {
			t.Fatalf("update %s: %d %s", body, status, answer)
		}
		if got := stored(); got != c.want {
			t.Errorf("access lists after an update with %q: %s, want %s", c.lists, got, c.want)
		}
	}
}

// TestPanelDeviceManualGroup: one call sets a device's manual group: the
// device goes into the chosen group and out of the other groups named, in
// one transaction; a chosen group that does not exist is refused with 404
// and changes nothing (BUGS UM33).
func TestPanelDeviceManualGroup(t *testing.T) {
	srv, base := panelGroupTestServer(t, 19932)
	store := srv.db.(db.PanelGroupStore)
	var guids []string
	for _, name := range []string{"A", "B"} {
		resp, body := doPanelJSON(t, http.MethodPost, base+"/api/panel/device-groups", map[string]any{"name": name})
		guid, _ := body["guid"].(string)
		if resp.StatusCode != http.StatusCreated || guid == "" {
			t.Fatalf("create %s: %d %v", name, resp.StatusCode, body)
		}
		guids = append(guids, guid)
	}
	a, b := guids[0], guids[1]
	if err := store.SetDeviceGroupPeers(a, []string{"PEER1", "PEER2"}); err != nil {
		t.Fatal(err)
	}
	members := func() string {
		t.Helper()
		var out []string
		for _, guid := range guids {
			peers, err := store.ListDeviceGroupPeerIDs(guid)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, strings.Join(peers, ","))
		}
		return strings.Join(out, " | ")
	}
	url := base + "/api/panel/devices/PEER1/device-group"

	resp, body := doPanelJSON(t, http.MethodPut, url, map[string]any{"guid": b, "groups": []string{a, b}})
	if resp.StatusCode != http.StatusOK || body["device_id"] != "PEER1" || body["guid"] != b {
		t.Fatalf("move into B: %d %v", resp.StatusCode, body)
	}
	if got := members(); got != "PEER2 | PEER1" {
		t.Errorf("members after moving PEER1 into B: %q", got)
	}
	resp, body = doPanelJSON(t, http.MethodPut, url, map[string]any{"guid": "no-such-group", "groups": []string{a, b}})
	if resp.StatusCode != http.StatusNotFound || body["error"] != "group not found" {
		t.Errorf("a chosen group that does not exist: %d %v", resp.StatusCode, body)
	}
	if got := members(); got != "PEER2 | PEER1" {
		t.Errorf("members after a refused write: %q", got)
	}
	if resp, body = doPanelJSON(t, http.MethodPut, url, "not an object"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a body that is no object: %d %v", resp.StatusCode, body)
	}
	if resp, body = doPanelJSON(t, http.MethodPut, url, map[string]any{"guid": "", "groups": []string{a, b}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("take PEER1 out: %d %v", resp.StatusCode, body)
	}
	if got := members(); got != "PEER2 | " {
		t.Errorf("members after taking PEER1 out: %q", got)
	}
}
