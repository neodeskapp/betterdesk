package chatstore

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// TestTeams: a team's members are the people picked by hand and the
// members of its user groups; someone added to or taken out of the user
// group joins or leaves the team at once, and a deleted team takes its
// messages along.
func TestTeams(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		for _, a := range []struct {
			id   int64
			name string
		}{{5, "anna"}, {6, "ben"}, {8, "cleo"}, {9, "dora"}} {
			if _, err := s.db.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', 'operator')`, a.id, a.name); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO user_groups (guid, name) VALUES ('g-desk', 'Service desk')`); err != nil {
			t.Fatal(err)
		}
		var desk int64
		if err := s.db.QueryRow(ctx, `SELECT id FROM user_groups WHERE guid = 'g-desk'`).Scan(&desk); err != nil {
			t.Fatal(err)
		}
		join := func(user int64) {
			t.Helper()
			if _, err := s.db.Exec(ctx, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, desk, user); err != nil {
				t.Fatal(err)
			}
		}
		join(8)

		if _, err := s.NewTeam(ctx, "  ", 5, []int64{6}, nil); !errors.Is(err, ErrEmpty) {
			t.Errorf("a team without a name: %v", err)
		}
		team := must(s.NewTeam(ctx, " Service desk ", 5, []int64{6}, []string{"g-desk", "g-gone"}))
		if team.Kind != ConvTeam || team.Title != "Service desk" || KindOf(team.ID) != ConvTeam {
			t.Fatalf("team: %+v", team)
		}
		// Anna made it for others and is no member
		if m := must(s.Members(ctx, team.ID)); !slices.Equal(m, []int64{6, 8}) {
			t.Errorf("members %v, want ben by hand and cleo by her user group", m)
		}
		if p := must(s.TeamPeople(ctx, team.ID)); !slices.Equal(p, []int64{6}) {
			t.Errorf("people by hand %v, want ben", p)
		}
		if g := must(s.TeamGroups(ctx, team.ID)); len(g) != 1 || g[0].GUID != "g-desk" || g[0].Name != "Service desk" {
			t.Errorf("user groups %+v, want the one that exists", g)
		}

		// Dora joins the user group: she is in the team and lists it
		if must(s.IsMember(ctx, team.ID, 9)) {
			t.Error("dora is a member before she joined the user group")
		}
		join(9)
		if !must(s.IsMember(ctx, team.ID, 9)) {
			t.Error("dora is no member after she joined the user group")
		}
		forDora := must(s.List(ctx, Filter{Kinds: []string{ConvDirect, ConvGroup, ConvTeam}, Member: 9}))
		if len(forDora) != 1 || forDora[0].ID != team.ID {
			t.Errorf("dora's list %+v, want the team", forDora)
		}
		if l := must(s.List(ctx, Filter{Kinds: []string{ConvTeam}, Member: 5})); len(l) != 0 {
			t.Errorf("anna lists %d teams, want none", len(l))
		}

		// Taking the user group off leaves cleo and dora out
		post(t, s, team.ID, ben, "morning")
		if _, err := s.SetTeam(ctx, team.ID, "", nil, []string{}); err != nil {
			t.Fatal(err)
		}
		if m := must(s.Members(ctx, team.ID)); !slices.Equal(m, []int64{6}) {
			t.Errorf("members after the user group was taken off %v, want ben", m)
		}
		c := must(s.SetTeam(ctx, team.ID, "Desk", []int64{5, 6}, nil))
		if c.Title != "Desk" || !slices.Equal(must(s.Members(ctx, team.ID)), []int64{5, 6}) {
			t.Errorf("after the change: %+v, %v", c, must(s.Members(ctx, team.ID)))
		}
		if _, err := s.SetTeam(ctx, "group:nope", "x", nil, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("SetTeam on a group: %v", err)
		}

		if err := s.DeleteTeam(ctx, team.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Conversation(ctx, team.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("the deleted team: %v", err)
		}
		if m := must(s.Members(ctx, team.ID)); len(m) != 0 {
			t.Errorf("the deleted team keeps members %v", m)
		}
		var left int
		if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM chat_messages WHERE conversation_id = ?`, team.ID).Scan(&left); err != nil || left != 0 {
			t.Errorf("%d messages left (%v)", left, err)
		}
		if err := s.DeleteTeam(ctx, team.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleting it again: %v", err)
		}
	})
}
