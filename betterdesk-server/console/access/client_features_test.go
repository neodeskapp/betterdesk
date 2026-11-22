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
// feature on, else on; Client actions is always off.
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
		if err != nil || !slices.Equal(off, []string{"client_actions"}) {
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
		if want := []string{"file_transfer", "clipboard", "client_actions"}; !slices.Equal(off, want) {
			t.Errorf("off = %v, want %v", off, want)
		}

		// The role does not count: a super admin's rows apply.
		save("user", "2", row("remote_control", false))
		if off, err := s.ClientFeaturesOff(ctx, 2); err != nil || !slices.Equal(off, []string{"remote_control", "client_actions"}) {
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
