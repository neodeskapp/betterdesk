package remote

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestViewerNameIsThePersons: the viewer names its account to the computer
// by the person's name, as the quick support window names the supporter,
// and falls back to the user name (L554).
func TestViewerNameIsThePersons(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role, first_name, last_name) VALUES (5, 'oop', 'x', 'operator', 'Olga', 'Operator'), (6, 'vee', 'x', 'viewer', '', '')"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{5: "Olga Operator", 6: "vee", 7: "fallback"} {
		if got := viewerName(ctx, db, id, "fallback"); got != want {
			t.Errorf("user %d: %q, want %q", id, got, want)
		}
	}
}
