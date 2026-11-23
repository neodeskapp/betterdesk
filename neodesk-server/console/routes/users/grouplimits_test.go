package users_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// settingsLevel is the Settings section level of an account's effective
// panel access, as an admin reads it.
func settingsLevel(t *testing.T, w *routetest.World, admin *routetest.Agent, id string) string {
	t.Helper()
	got := admin.Do("GET", "/api/panel/access/users/"+id, "")
	var out struct {
		Data struct {
			Effective struct {
				Sections map[string]struct {
					Access string `json:"access"`
				} `json:"sections"`
			} `json:"effective"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &out); err != nil || got.Status != 200 {
		t.Fatalf("panel access of %s: %d %s", id, got.Status, got.Body)
	}
	return out.Data.Effective.Sections["settings"].Access
}

// TestDeletingAGroupKeepsItsLimits: a user group that limits a server
// admin's Settings to none is deleted only by an account that may change
// every member, as saving its panel access is; and once deleted, the
// limit stays with the members, so deleting a group lifts no limit
// (L391).
func TestDeletingAGroupKeepsItsLimits(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-limit', 'Limited', '', '')`); err != nil {
		t.Fatal(err)
	}
	admin := w.As("admin")
	if got := admin.Do("PUT", "/api/panel/access/groups/ug-limit", `{"sections":[{"resource_key":"settings","access_level":"none"}],"features":[{"feature_key":"terminal","enabled":false}]}`); got.Status != 200 {
		t.Fatalf("limit the group: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("PATCH", "/api/users/3", `{"groupGuids":["ug-limit"]}`); got.Status != 200 {
		t.Fatalf("put the server admin in the group: %d %s", got.Status, got.Body)
	}
	if level := settingsLevel(t, w, admin, "3"); level != "none" {
		t.Fatalf("the server admin's Settings in the group: %s, want none", level)
	}

	if got := w.As("global_admin").Do("DELETE", "/api/panel/user-groups/ug-limit", ""); got.Status != 403 {
		t.Errorf("global_admin deletes the group of a server admin: %d %s, want 403", got.Status, got.Body)
	}
	if level := settingsLevel(t, w, admin, "3"); level != "none" {
		t.Errorf("the server admin's Settings after the refused delete: %s, want none", level)
	}

	if got := admin.Do("DELETE", "/api/panel/user-groups/ug-limit", ""); got.Status != 200 {
		t.Fatalf("admin deletes the group: %d %s", got.Status, got.Body)
	}
	if level := settingsLevel(t, w, admin, "3"); level != "none" {
		t.Errorf("the server admin's Settings once the group is gone: %s, want none", level)
	}
	off, err := w.Server.Deps().Access.ClientFeaturesOff(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	terminalOff := false
	for _, f := range off {
		terminalOff = terminalOff || f == "terminal"
	}
	if !terminalOff {
		t.Errorf("the server admin's terminal once the group is gone: on, want off (features off %v)", off)
	}
}
