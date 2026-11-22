package access

import (
	"context"
	"slices"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// ClientFeaturesOff names the features an account may not use, by the
// rules of the access sheet: its own row, else any group that turns the
// feature on, else on.
func TestClientFeaturesOff(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		createSchema(t, db)
		ctx := context.Background()
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "op1", "x", "operator")
		mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, "boss", "x", "super_admin")
		mustExec(t, db, `INSERT INTO user_groups (guid, name) VALUES (?, ?)`, "ops", "Operators")
		mustExec(t, db, `INSERT INTO user_groups (guid, name) VALUES (?, ?)`, "lab", "Lab")
		mustExec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, 1, 1)
		mustExec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, 2, 1)
		s := New(db, &fakeRoles{}, Options{})
		save := func(subjectType, subjectID string, features ...any) {
			t.Helper()
			if _, err := s.SavePolicy(ctx, subjectType, subjectID, []any{}, features); err != nil {
				t.Fatal(err)
			}
		}
		row := func(key string, enabled bool) any { return map[string]any{"feature_key": key, "enabled": enabled} }

		off, err := s.ClientFeaturesOff(ctx, 1)
		if err != nil || len(off) != 0 {
			t.Fatalf("nothing stored: %v, %v", off, err)
		}

		save("user", "1", row("file_transfer", false), row("audio", true))
		save("group", "ops", row("audio", false), row("clipboard", false), row("terminal", false))
		save("group", "lab", row("terminal", true))
		off, err = s.ClientFeaturesOff(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		// file_transfer from the account, clipboard from Operators; audio is
		// the account's own on, terminal is on in Lab.
		if want := []string{"file_transfer", "clipboard"}; !slices.Equal(off, want) {
			t.Errorf("off = %v, want %v", off, want)
		}

		// The role does not count: a super admin's rows apply.
		save("user", "2", row("remote_control", false))
		if off, err := s.ClientFeaturesOff(ctx, 2); err != nil || !slices.Equal(off, []string{"remote_control"}) {
			t.Errorf("super admin: %v, %v", off, err)
		}

		if _, err := s.ClientFeaturesOff(ctx, 0); err == nil {
			t.Error("no account: no error")
		}
	})
}

func TestClientFeaturesOffFailsWithTheStore(t *testing.T) {
	s := fakeService(&fakeData{failing: "userGroups"}, &fakeRoles{})
	if off, err := s.ClientFeaturesOff(context.Background(), 3); err == nil {
		t.Fatalf("a failing lookup gave %v", off)
	}
}

// Rows saved before Tunnel and Port forwarding became one switch
// (DECISIONS A162) stay readable: a Port forwarding row counts for the
// merged switch when the subject has no Tunnel row, per account and per
// group; a Client actions row is left out.
func TestOldFeatureRowsCountForTheMergedSwitch(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		createSchema(t, db)
		ctx := context.Background()
		for _, name := range []string{"pf-only", "both", "grouped"} {
			mustExec(t, db, `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, name, "x", "operator")
		}
		mustExec(t, db, `INSERT INTO user_groups (guid, name) VALUES (?, ?)`, "old", "Old rows")
		mustExec(t, db, `INSERT INTO user_groups (guid, name) VALUES (?, ?)`, "new", "New rows")
		mustExec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, 1, 3)
		mustExec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`, 2, 3)
		row := func(subjectType, subjectID, key string, enabled bool) {
			t.Helper()
			mustExec(t, db, `INSERT INTO client_feature_policies (subject_type, subject_id, feature_key, enabled) VALUES (?, ?, ?, ?)`,
				subjectType, subjectID, key, enabled)
		}
		row("user", "1", "port_forwarding", false)
		row("user", "1", "client_actions", true)
		row("user", "2", "port_forwarding", false)
		row("user", "2", "tunnel", true)
		row("group", "old", "port_forwarding", true)
		row("group", "new", "tunnel", false)
		s := New(db, &fakeRoles{permissions: map[string][]string{"operator": {"device.view"}}}, Options{})

		rows, err := s.ClientFeatureRows(ctx, "user", "1")
		if err != nil || len(rows) != 1 || rows[0] != (FeatureRow{FeatureKey: "tunnel", Enabled: false}) {
			t.Errorf("Port forwarding alone reads as %+v, %v", rows, err)
		}
		if off, err := s.ClientFeaturesOff(ctx, 1); err != nil || !slices.Equal(off, []string{"tunnel"}) {
			t.Errorf("Port forwarding off alone: off %v, %v", off, err)
		}
		policy, err := s.ResolveForUser(ctx, PolicyUser{ID: 1, Role: "operator"})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := policy.Features.Get("tunnel"); got != (FeatureAccess{Enabled: false, Source: SourceUser}) {
			t.Errorf("resolved tunnel: %+v", got)
		}

		rows, err = s.ClientFeatureRows(ctx, "user", "2")
		if err != nil || len(rows) != 1 || rows[0] != (FeatureRow{FeatureKey: "tunnel", Enabled: true}) {
			t.Errorf("a Tunnel row wins over Port forwarding: %+v, %v", rows, err)
		}
		if off, err := s.ClientFeaturesOff(ctx, 2); err != nil || len(off) != 0 {
			t.Errorf("Tunnel on: off %v, %v", off, err)
		}

		// The old group's Port forwarding on counts as the merged switch
		// on, which wins over the other group's off.
		if off, err := s.ClientFeaturesOff(ctx, 3); err != nil || len(off) != 0 {
			t.Errorf("groups: off %v, %v", off, err)
		}
		group, err := s.ResolveForGroup(ctx, "old")
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := group.Features.Get("tunnel"); got != (FeatureAccess{Enabled: true, Source: SourceGroup}) {
			t.Errorf("old group's tunnel: %+v", got)
		}
	})
}
