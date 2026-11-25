package admintoken

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestTokensWithTheOldChatPermissionKeepTheirChat: a token made before
// A242 with chat.access carries Support chat and Team chat after the
// update, in its row and as it is read; its other permissions stay.
func TestTokensWithTheOldChatPermissionKeepTheirChat(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := newService(t, db, nil)
		_, tok, err := s.Create(ctx, Creator{ID: 7, Username: "ann", Holds: holdsAll},
			Input{Name: "old", Permissions: []string{"device.view"}, ExpiresAt: time.Now().Add(time.Hour)}, known)
		if err != nil {
			t.Fatal(err)
		}
		// As a console before A242 stored it
		if _, err := db.Exec(ctx, `UPDATE admin_tokens SET permissions = '["chat.access","device.view"]' WHERE public_id = ?`, tok.ID); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, tok.ID)
		if err != nil || strings.Join(got.Permissions, ",") != "chat.support,chat.team,device.view" {
			t.Fatalf("read before the start: %+v, %v", got, err)
		}
		newService(t, db, nil)
		var raw string
		if err := db.QueryRow(ctx, `SELECT permissions FROM admin_tokens WHERE public_id = ?`, tok.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw != `["chat.support","chat.team","device.view"]` {
			t.Errorf("stored after the start: %s", raw)
		}
	})
}
