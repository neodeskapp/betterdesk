package authn

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestServerDeviceTokensProveTheirDevice: a device token the API server
// issued for a device (device_tokens, created_by "system", active, bound)
// passes the device routes as that device, like the console's own. An
// operator's enrollment token, an unbound, revoked, pending, used up or
// expired one and one of a banned device do not (L46).
func TestServerDeviceTokensProveTheirDevice(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		set := db.Pick("1", "TRUE")
		mustExec(t, db, `INSERT INTO peers (id) VALUES ('srv-dev')`)
		mustExec(t, db, `INSERT INTO peers (id, banned) VALUES ('srv-banned', `+set+`)`)
		for _, row := range []struct{ token, peer, status, createdBy, expires string }{
			{"issued-for-srv-dev", "srv-dev", "active", "system", ""},
			{"issued-unbound", "", "active", "system", ""},
			{"operator-made", "srv-dev", "active", "admin", ""},
			{"issued-revoked", "srv-dev", "revoked", "system", ""},
			{"issued-pending", "srv-dev", "pending", "system", ""},
			{"issued-expired", "srv-dev", "active", "system", "2020-01-01T00:00:00Z"},
			{"issued-banned", "srv-banned", "active", "system", ""},
		} {
			var expires any
			if row.expires != "" {
				expires = row.expires
			}
			mustExec(t, db, `INSERT INTO device_tokens (token_hash, peer_id, status, max_uses, use_count, created_by, expires_at) VALUES (?, ?, ?, 0, 0, ?, ?)`,
				HashDeviceToken(row.token), row.peer, row.status, row.createdBy, expires)
		}
		mustExec(t, db, `INSERT INTO device_tokens (token_hash, peer_id, status, max_uses, use_count, created_by) VALUES (?, 'srv-dev', 'active', 1, 1, 'system')`,
			HashDeviceToken("issued-used-up"))

		if got, err := s.RequireDeviceToken(ctx, "Bearer issued-for-srv-dev"); err != nil || got.DeviceID != "srv-dev" || got.Token != nil {
			t.Errorf("device request with the server's device token: %+v, %v; want it through as srv-dev", got, err)
		}
		for _, token := range []string{"issued-unbound", "operator-made", "issued-revoked", "issued-pending", "issued-expired", "issued-used-up", "issued-banned"} {
			if got, err := s.RequireDeviceToken(ctx, "Bearer "+token); err == nil {
				t.Errorf("device request with %s: through as %s, want it refused", token, got.DeviceID)
			}
		}
		if device, err := s.ServerDeviceToken(ctx, "operator-made"); device != "" || err != nil {
			t.Errorf("an operator's enrollment token names %q (%v), want no device", device, err)
		}
	})
}

// TestEveryNewDeviceTokenEndsTheOthers: a device token the console issues
// goes into device_tokens on behalf of no account and ends the device's
// earlier tokens, the ones in device_tokens and one an older console kept
// in access_tokens; RevokeDeviceTokens ends both kinds. The row keeps only
// the token's hash and its first eight characters (L46, L146).
func TestEveryNewDeviceTokenEndsTheOthers(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "admin", "pw", "admin", "local")
		mustExec(t, db, `INSERT INTO peers (id) VALUES ('both-kinds')`)
		passes := func(token string) bool {
			got, err := s.RequireDeviceToken(ctx, "Bearer "+token)
			return err == nil && got.DeviceID == "both-kinds"
		}
		older := func() string {
			t.Helper()
			token, err := s.issue(ctx, TokenKindDevice, id, "both-kinds", "", "", store.DeviceTokenExpiry())
			if err != nil {
				t.Fatal(err)
			}
			return token
		}

		console := older()
		first, err := s.IssueDeviceToken(ctx, "both-kinds", "test")
		if err != nil || first == "" {
			t.Fatalf("issue a device token: %q, %v", first, err)
		}
		if passes(console) || !passes(first) {
			t.Errorf("after the new token: the older console token passes %v, the new token %v; want false, true", passes(console), passes(first))
		}
		var prefix, createdBy, note string
		if err := db.QueryRow(ctx, `SELECT token_prefix, created_by, note FROM device_tokens WHERE token_hash = ?`, HashDeviceToken(first)).Scan(&prefix, &createdBy, &note); err != nil ||
			prefix != first[:8] || createdBy != ServerDeviceTokenCreator || note != "test" {
			t.Errorf("stored prefix %q, created_by %q, note %q (%v), want %q, system, test", prefix, createdBy, note, err, first[:8])
		}
		var held int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM access_tokens WHERE token_hash = ?`, HashAccessToken(first)).Scan(&held); err != nil || held != 0 {
			t.Errorf("the new device token has %d rows in access_tokens (%v), want none", held, err)
		}

		again, err := s.IssueDeviceToken(ctx, "both-kinds", "test")
		if err != nil {
			t.Fatal(err)
		}
		if passes(first) || !passes(again) {
			t.Errorf("after a second token: the first passes %v, the second %v; want false, true", passes(first), passes(again))
		}

		console = older()
		if err := s.RevokeDeviceTokens(ctx, "both-kinds"); err != nil {
			t.Fatal(err)
		}
		if passes(again) || passes(console) {
			t.Error("a token of the device passes after RevokeDeviceTokens")
		}
		if ok, err := s.VerifyDeviceWSAuth(ctx, "both-kinds", again); ok || err != nil {
			t.Errorf("device socket with a revoked device_tokens token: %v, %v", ok, err)
		}
	})
}

// TestServerChangesEndTheServersDeviceTokens: the signal server's delete,
// ban and id change end the device's device tokens in device_tokens too,
// and leave an operator's enrollment token bound to the device alone
// (L44, L46).
func TestServerChangesEndTheServersDeviceTokens(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		server := storetest.GoServer(t, db)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		devices := []string{"srv-soft", "srv-hard", "srv-ban", "srv-rename", "srv-other"}
		tokens := map[string]string{}
		for _, device := range devices {
			mustExec(t, db, `INSERT INTO peers (id) VALUES (?)`, device)
			token, err := s.IssueDeviceToken(ctx, device, "test")
			if err != nil || token == "" {
				t.Fatalf("issue for %s: %q, %v", device, token, err)
			}
			tokens[device] = token
		}
		mustExec(t, db, `INSERT INTO device_tokens (token_hash, peer_id, status, max_uses, use_count, created_by) VALUES (?, 'srv-ban', 'active', 0, 0, 'admin')`,
			HashDeviceToken("operator-made-for-srv-ban"))

		for _, step := range []struct {
			name string
			run  func() error
		}{
			{"soft delete", func() error { return server.DeletePeer("srv-soft") }},
			{"restore", func() error { return server.RestorePeer("srv-soft") }},
			{"hard delete", func() error { return server.HardDeletePeer("srv-hard") }},
			{"ban", func() error { return server.BanPeer("srv-ban", "test") }},
			{"unban", func() error { return server.UnbanPeer("srv-ban") }},
			{"change id", func() error { return server.ChangePeerID("srv-rename", "srv-renamed", "test") }},
		} {
			if err := step.run(); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
		}

		for _, device := range devices {
			want := device == "srv-other"
			if _, err := s.RequireDeviceToken(ctx, "Bearer "+tokens[device]); (err == nil) != want {
				t.Errorf("device request of %s after the change: %v, want through %v", device, err, want)
			}
		}
		if _, err := s.RequireDeviceToken(ctx, "Bearer "+tokens["srv-rename"]); err == nil {
			t.Error("the renamed device's token passes under its new id")
		}
		var status string
		if err := db.QueryRow(ctx, `SELECT status FROM device_tokens WHERE token_hash = ?`, HashDeviceToken("operator-made-for-srv-ban")).Scan(&status); err != nil || status != "active" {
			t.Errorf("an operator's enrollment token after the ban: %q, %v; want it left active", status, err)
		}
	})
}
