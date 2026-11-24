package admintoken

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// setConfig writes a server_config row as the API server does.
func setConfig(t *testing.T, db *store.DB, key, value string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO server_config (key, value) VALUES (?, ?)`, key, value); err != nil {
		t.Fatal(err)
	}
}

func config(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(context.Background(), `SELECT COALESCE(MAX(value), '') FROM server_config WHERE key = ?`, key).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// A share link made with a token ends with its revoke; one made without
// the token stays. What names a token whose row is gone (deleted, or not
// brought back by a restore) ends at the next sweep, with an ending that
// says so; the sweep ends it once.
func TestShareLinksAndWhatADeletedTokenMadeEnd(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		storetest.GoServerMigrate(t, db)
		ctx := context.Background()
		s := newService(t, db, nil)
		_, tok, err := s.Create(ctx, Creator{ID: 7, Username: "ann", Holds: holdsAll},
			Input{Name: "share", Permissions: []string{"device.view"}, ExpiresAt: time.Now().Add(time.Hour)}, known)
		if err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		setConfig(t, db, "mesh_share_aaa", `{"peer_id":"MESH1","created_by":"apikey:console-bridge","expires_at":"`+later+`","view_only":false,"created_by_token":"`+tok.ID+`"}`)
		setConfig(t, db, "mesh_share_bbb", `{"peer_id":"MESH1","created_by":"apikey:console-bridge","expires_at":"`+later+`","view_only":false}`)
		setConfig(t, db, "gal_gone", `{"id":"g1","peer_ids":["P1"],"created_by":"ann","expires_at":"`+later+`","created_at":"`+later+`","view_only":false,"created_by_token":"zzzzzzzzzzzz"}`)

		open, err := s.OpenMade(ctx, []string{tok.ID})
		if err != nil || open[tok.ID].ShareLinks != 1 || open[tok.ID].Total() != 1 {
			t.Fatalf("open before the revoke: %+v, %v", open, err)
		}
		ending, err := s.Revoke(ctx, tok.ID, "ann")
		if err != nil || ending == nil || ending.Ended.ShareLinks != 1 {
			t.Fatalf("revoke: %+v, %v", ending, err)
		}
		if config(t, db, "mesh_share_aaa") != "" {
			t.Error("the share link made with the token still works")
		}
		if config(t, db, "mesh_share_bbb") == "" {
			t.Error("the share link made without the token ended")
		}

		endings, err := s.Sweep(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(endings) != 1 || endings[0].Why != EndDeleted || endings[0].Ended.GuestLinks != 1 || endings[0].GuestLinks[0] != "g1" {
			t.Fatalf("sweep: %+v", endings)
		}
		if text := endings[0].AuditText(); text != "API token ndt_zzzzzzzzzzzz_… was deleted. Ended with it: 1 guest link" {
			t.Errorf("audit text %q", text)
		}
		if link := config(t, db, "gal_gone"); !strings.Contains(link, `"revoked_at":`) {
			t.Errorf("the deleted token's guest link is not revoked: %s", link)
		}
		if again, err := s.Sweep(ctx); err != nil || len(again) != 0 {
			t.Errorf("a second sweep ends again: %+v, %v", again, err)
		}
	})
}
