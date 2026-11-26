package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestTeams: only Manage teams (admins here) lists, creates, changes and
// deletes teams; a team reaches the people picked by hand and the members
// of its user groups, and nobody else.
func TestTeams(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newInboxWorld(t, db)
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO user_groups (guid, name) VALUES ('g-desk', 'Service desk')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 8 FROM user_groups WHERE guid = 'g-desk'`); err != nil {
			t.Fatal(err)
		}

		if status, _ := w.get("anna", "/api/chat/team/teams"); status != 403 {
			t.Errorf("anna listing the teams: %d, want 403", status)
		}
		if status, _ := w.send("anna", "POST", "/api/chat/team/teams", `{"title":"Mine","people":[5]}`); status != 403 {
			t.Errorf("anna making a team: %d, want 403", status)
		}
		if status, _ := w.send("admin", "POST", "/api/chat/team/teams", `{"title":"Desk","groups":["g-nope"]}`); status != 400 {
			t.Errorf("a team with an unknown user group: %d, want 400", status)
		}
		if status, _ := w.send("admin", "POST", "/api/chat/team/teams", `{"title":" ","people":[6]}`); status != 400 {
			t.Errorf("a team without a name: %d, want 400", status)
		}

		status, made := w.send("admin", "POST", "/api/chat/team/teams", `{"title":"Desk","people":[6],"groups":["g-desk"]}`)
		conv, _ := made["conversation"].(map[string]any)
		id, _ := conv["id"].(string)
		if status != 200 || !strings.HasPrefix(id, "team:") || conv["kind"] != "team" {
			t.Fatalf("making the team: %d %v", status, made)
		}

		status, list := w.get("admin", "/api/chat/team/teams")
		teams, _ := list["teams"].([]any)
		groups, _ := list["user_groups"].([]any)
		if status != 200 || len(teams) != 1 {
			t.Fatalf("admin's list of teams: %d %v", status, list)
		}
		desk := false
		for _, g := range groups {
			g := g.(map[string]any)
			desk = desk || g["guid"] == "g-desk" && len(g["members"].([]any)) == 1
		}
		if !desk {
			t.Errorf("the user groups %v lack Service desk with cleo", groups)
		}

		// Ben by hand and cleo by her user group see it; anna and the
		// admin who made it don't
		for who, sees := range map[string]bool{"ben": true, "cleo": true, "anna": false, "admin": false} {
			_, team := w.get(who, "/api/chat/team")
			convs, _ := team["conversations"].([]any)
			found := false
			for _, c := range convs {
				found = found || c.(map[string]any)["id"] == id
			}
			if found != sees {
				t.Errorf("%s lists the team: %v, want %v", who, found, sees)
			}
			if status, _ := w.get(who, "/api/chat/conversations/"+id); (status == 200) != sees {
				t.Errorf("%s opening the team: %d", who, status)
			}
		}
		_, detail := w.get("ben", "/api/chat/conversations/"+id)
		if g, _ := detail["groups"].([]any); len(g) != 1 || g[0] != "Service desk" {
			t.Errorf("the team's user groups: %v", detail["groups"])
		}
		if p, _ := detail["people"].([]any); len(p) != 2 {
			t.Errorf("the team's people: %v, want ben and cleo", detail["people"])
		}

		if status, _ := w.send("ben", "PATCH", "/api/chat/team/teams/"+id, `{"people":[5]}`); status != 403 {
			t.Errorf("ben changing the team: %d, want 403", status)
		}
		if status, _ := w.send("admin", "PATCH", "/api/chat/team/teams/"+id, `{"groups":[]}`); status != 200 {
			t.Errorf("taking off the user group: %d", status)
		}
		if status, _ := w.get("cleo", "/api/chat/conversations/"+id); status != 404 {
			t.Errorf("cleo after her user group was taken off: %d, want 404", status)
		}

		if status, _ := w.send("ben", "DELETE", "/api/chat/team/teams/"+id, ``); status != 403 {
			t.Errorf("ben deleting the team: %d, want 403", status)
		}
		if status, _ := w.send("admin", "DELETE", "/api/chat/team/teams/"+id, ``); status != 200 {
			t.Errorf("deleting the team: %d", status)
		}
		if status, _ := w.get("ben", "/api/chat/conversations/"+id); status != 404 {
			t.Errorf("ben opening the deleted team: %d, want 404", status)
		}
		if status, _ := w.send("admin", "DELETE", "/api/chat/team/teams/"+id, ``); status != 404 {
			t.Errorf("deleting it again: %d, want 404", status)
		}
	})
}

// TestTeamsAudit: making, changing and deleting a team each write one
// audit entry by the signed-in account that names the team and what
// changed; a refused or failed request writes none (A268).
func TestTeamsAudit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newInboxWorld(t, db)
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO user_groups (guid, name) VALUES ('g-desk', 'Service desk')`); err != nil {
			t.Fatal(err)
		}
		// one asks and expects the status and the audit entry it writes,
		// or none for an empty action
		one := func(what, who, method, target, body string, status int, action string, details map[string]any) map[string]any {
			t.Helper()
			before := len(w.auditLog)
			got, out := w.send(who, method, target, body)
			if got != status {
				t.Fatalf("%s: %d %v, want %d", what, got, out, status)
			}
			entries := w.auditLog[before:]
			if action == "" {
				if len(entries) != 0 {
					t.Errorf("%s wrote %v, want no audit entry", what, entries)
				}
				return out
			}
			if len(entries) != 1 {
				t.Fatalf("%s wrote %v, want one audit entry", what, entries)
			}
			e := entries[0]
			if e.action != action || e.by != scopeAccounts[who].ID {
				t.Errorf("%s: audit %s by %d, want %s by %d", what, e.action, e.by, action, scopeAccounts[who].ID)
			}
			for k, v := range details {
				if e.details[k] != v {
					t.Errorf("%s: audit %s=%v, want %v (all: %v)", what, k, e.details[k], v, e.details)
				}
			}
			for _, k := range []string{"renamed_from", "people_added", "people_removed", "groups_added", "groups_removed"} {
				if _, ok := details[k]; !ok && e.details[k] != nil {
					t.Errorf("%s: audit has %s=%v, want none", what, k, e.details[k])
				}
			}
			return out
		}

		one("anna making a team", "anna", "POST", "/api/chat/team/teams", `{"title":"Mine","people":[5]}`, 403, "", nil)
		one("a team with an unknown user group", "admin", "POST", "/api/chat/team/teams", `{"title":"Desk","groups":["g-nope"]}`, 400, "", nil)
		one("a team without a name", "admin", "POST", "/api/chat/team/teams", `{"title":" "}`, 400, "", nil)

		made := one("making the team", "admin", "POST", "/api/chat/team/teams", `{"title":"Desk","people":[8,6],"groups":["g-desk"]}`, 200,
			"chat_team_created", map[string]any{"title": "Desk", "people": "ben (6), cleo (8)", "groups": "Service desk (g-desk)"})
		id, _ := made["conversation"].(map[string]any)["id"].(string)
		if e := w.auditLog[len(w.auditLog)-1]; e.details["conversation_id"] != id || id == "" {
			t.Errorf("the audit entry of the new team names %v, want %q", e.details["conversation_id"], id)
		}
		team := "/api/chat/team/teams/" + id

		one("ben changing the team", "ben", "PATCH", team, `{"title":"Ben's"}`, 403, "", nil)
		one("changing a team that doesn't exist", "admin", "PATCH", "/api/chat/team/teams/team:00000000", `{"title":"Gone"}`, 404, "", nil)
		one("changing a conversation that is no team", "admin", "PATCH", "/api/chat/team/teams/dev1", `{"title":"Gone"}`, 404, "", nil)
		one("changing the team to someone unknown", "admin", "PATCH", team, `{"people":[999]}`, 400, "", nil)
		one("renaming the team", "admin", "PATCH", team, `{"title":"Front desk"}`, 200,
			"chat_team_updated", map[string]any{"conversation_id": id, "title": "Front desk", "renamed_from": "Desk"})
		one("adding anna, taking off ben and the user group", "admin", "PATCH", team, `{"people":[5,8],"groups":[]}`, 200,
			"chat_team_updated", map[string]any{"conversation_id": id, "title": "Front desk", "people_added": "anna (5)",
				"people_removed": "ben (6)", "groups_removed": "Service desk (g-desk)"})
		one("adding the user group again", "admin", "PATCH", team, `{"groups":["g-desk"]}`, 200,
			"chat_team_updated", map[string]any{"conversation_id": id, "title": "Front desk", "groups_added": "Service desk (g-desk)"})

		one("ben deleting the team", "ben", "DELETE", team, ``, 403, "", nil)
		one("deleting the team", "admin", "DELETE", team, ``, 200,
			"chat_team_deleted", map[string]any{"conversation_id": id, "title": "Front desk"})
		one("deleting it again", "admin", "DELETE", team, ``, 404, "", nil)
		one("deleting a conversation that is no team", "admin", "DELETE", "/api/chat/team/teams/dev1", ``, 404, "", nil)
	})
}
