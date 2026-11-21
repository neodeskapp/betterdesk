package authn

import (
	"context"
	"errors"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestDeviceSocketRefusesAnUnboundTokenForAKnownDevice: an enrollment token
// bound to no device opens a device socket only for a device id the signal
// server has no row for yet. A device that exists, also a soft-deleted
// one, speaks only with a token bound to it (BUGS.md I8).
func TestDeviceSocketRefusesAnUnboundTokenForAKnownDevice(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		for _, token := range []struct{ token, peer string }{
			{"unbound-token-1", ""},
			{"bound-to-known", "known-device"},
		} {
			mustExec(t, db, `INSERT INTO device_tokens (token_hash, peer_id, status, max_uses, use_count) VALUES (?, ?, 'active', 0, 0)`,
				HashDeviceToken(token.token), token.peer)
		}
		mustExec(t, db, `INSERT INTO peers (id) VALUES ('known-device')`)
		mustExec(t, db, `INSERT INTO peers (id, soft_deleted) VALUES ('gone-device', `+db.Pick("1", "TRUE")+`)`)

		for _, c := range []struct {
			device, token string
			want          bool
		}{
			{"known-device", "unbound-token-1", false},
			{"gone-device", "unbound-token-1", false},
			{"new-device", "unbound-token-1", true},
			{"known-device", "bound-to-known", true},
			{"new-device", "bound-to-known", false},
		} {
			ok, err := s.VerifyDeviceWSAuth(ctx, c.device, c.token)
			if err != nil || ok != c.want {
				t.Errorf("device %s with %s: %v, %v; want %v", c.device, c.token, ok, err, c.want)
			}
			ok, err = s.VerifyEnrollmentToken(ctx, c.device, c.token)
			if err != nil || ok != c.want {
				t.Errorf("enrollment check of %s with %s: %v, %v; want %v", c.device, c.token, ok, err, c.want)
			}
		}
	})
}

// TestABannedOrDeletedDeviceProvesNothing: a device token of a device the
// signal server holds as banned or soft-deleted is refused on the device
// routes and the device sockets, whichever port banned or deleted it; a
// device the signal server has no row for yet keeps its token, and the
// device comes back with it when the ban ends (BUGS RV227).
func TestABannedOrDeletedDeviceProvesNothing(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "admin", "pw", "admin", "local")
		set := db.Pick("1", "TRUE")
		mustExec(t, db, `INSERT INTO peers (id) VALUES ('dev-ok')`)
		mustExec(t, db, `INSERT INTO peers (id, banned) VALUES ('dev-banned', `+set+`)`)
		mustExec(t, db, `INSERT INTO peers (id, soft_deleted) VALUES ('dev-binned', `+set+`)`)
		tokens := map[string]string{}
		for _, device := range []string{"dev-ok", "dev-banned", "dev-binned", "dev-new"} {
			token, err := s.IssueDeviceToken(ctx, id, device, "")
			if err != nil {
				t.Fatal(err)
			}
			tokens[device] = token
		}
		check := func(device string, want bool) {
			t.Helper()
			_, err := s.RequireDeviceToken(ctx, "Bearer "+tokens[device])
			var refused *DeviceAuthError
			if want && err != nil {
				t.Errorf("device request of %s: %v, want it through", device, err)
			}
			if !want && (!errors.As(err, &refused) || refused.Status != 401 || refused.Text != "Invalid or unbound access token") {
				t.Errorf("device request of %s: %v, want 401 Invalid or unbound access token", device, err)
			}
			if ok, err := s.VerifyDeviceWSAuth(ctx, device, tokens[device]); ok != want || err != nil {
				t.Errorf("device socket of %s: %v, %v; want %v", device, ok, err, want)
			}
		}
		check("dev-ok", true)
		check("dev-new", true)
		check("dev-banned", false)
		check("dev-binned", false)

		mustExec(t, db, `UPDATE peers SET banned = `+db.Pick("0", "FALSE")+` WHERE id = 'dev-banned'`)
		check("dev-banned", true)
	})
}

// TestServerDeletesAndBansEndDeviceTokens: a delete, soft or hard, and a
// ban end the device's device tokens whichever port they come through,
// since the signal server's own delete and ban revoke them in the shared
// database. A restore or an unban gives none back; the tokens of accounts
// and of other devices stay (BUGS RV231).
func TestServerDeletesAndBansEndDeviceTokens(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		server := storetest.GoServer(t, db)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "admin", "pw", "admin", "local")
		devices := []string{"dev-soft", "dev-hard", "dev-ban", "dev-other"}
		tokens := map[string]string{}
		for _, device := range devices {
			mustExec(t, db, `INSERT INTO peers (id) VALUES (?)`, device)
			token, err := s.IssueDeviceToken(ctx, id, device, "")
			if err != nil {
				t.Fatal(err)
			}
			tokens[device] = token
		}
		account, err := s.GenerateSignInToken(ctx, id, "dev-ban", "uuid-1", "")
		if err != nil {
			t.Fatal(err)
		}

		for _, step := range []struct {
			name string
			run  func() error
		}{
			{"soft delete", func() error { return server.DeletePeer("dev-soft") }},
			{"restore", func() error { return server.RestorePeer("dev-soft") }},
			{"hard delete", func() error { return server.HardDeletePeer("dev-hard") }},
			{"ban", func() error { return server.BanPeer("dev-ban", "test") }},
			{"unban", func() error { return server.UnbanPeer("dev-ban") }},
		} {
			if err := step.run(); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
		}

		for _, device := range devices {
			want := device == "dev-other"
			if _, err := s.RequireDeviceToken(ctx, "Bearer "+tokens[device]); (err == nil) != want {
				t.Errorf("device request of %s: %v, want through %v", device, err, want)
			}
			if ok, err := s.VerifyDeviceWSAuth(ctx, device, tokens[device]); ok != want || err != nil {
				t.Errorf("device socket of %s: %v, %v; want %v", device, ok, err, want)
			}
		}
		if user, err := s.ValidateAccessToken(ctx, account); err != nil || user == nil {
			t.Errorf("account token after the ban of its client id: %v, %v; want it live", user, err)
		}
	})
}

// Only a token the console issued for a device proves the device: an
// account's sign-in token proves none, whatever client id it stored, and a
// device token signs no account in. The revocations of an account's
// tokens leave the device tokens alone; a new device token revokes the
// device's earlier one (BUGS RV1).
func TestOnlyDeviceTokensProveADevice(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "viewer", "pw", "viewer", "local")
		signIn, err := s.GenerateSignInToken(ctx, id, "900100001", "uuid-a", "")
		if err != nil {
			t.Fatal(err)
		}
		noUUID, err := s.IssueAccessToken(ctx, id, "900100001", "", "")
		if err != nil {
			t.Fatal(err)
		}
		device, err := s.IssueDeviceToken(ctx, id, "900100001", "")
		if err != nil {
			t.Fatal(err)
		}

		var refused *DeviceAuthError
		for name, token := range map[string]string{"sign-in": signIn, "sign-in without a UUID": noUUID} {
			if _, err := s.RequireDeviceToken(ctx, "Bearer "+token); !errors.As(err, &refused) || refused.Status != 401 || refused.Text != "Invalid or unbound access token" {
				t.Errorf("device request with the %s token: %v", name, err)
			}
			if ok, err := s.VerifyDeviceWSAuth(ctx, "900100001", token); ok || err != nil {
				t.Errorf("device socket with the %s token: %v, %v", name, ok, err)
			}
			if user, err := s.ValidateAccessToken(ctx, token); user == nil || err != nil {
				t.Errorf("the %s token signs no one in: %v", name, err)
			}
		}
		got, err := s.RequireDeviceToken(ctx, "Bearer "+device)
		if err != nil || got.DeviceID != "900100001" {
			t.Errorf("device request with the device token: %+v, %v", got, err)
		}
		if ok, err := s.VerifyDeviceWSAuth(ctx, "900100001", device); !ok || err != nil {
			t.Errorf("device socket with the device token: %v, %v", ok, err)
		}
		if ok, err := s.VerifyDeviceWSAuth(ctx, "900100002", device); ok || err != nil {
			t.Errorf("another device's socket with the device token: %v, %v", ok, err)
		}
		if user, err := s.ValidateAccessToken(ctx, device); user != nil || err != nil {
			t.Errorf("the device token signs %+v in (%v)", user, err)
		}

		if err := s.RevokeClientTokens(ctx, id, "", ""); err != nil {
			t.Fatal(err)
		}
		if user, _ := s.ValidateAccessToken(ctx, signIn); user != nil {
			t.Error("the sign-out left the sign-in token live")
		}
		if _, err := s.RequireDeviceToken(ctx, "Bearer "+device); err != nil {
			t.Errorf("the account's sign-out revoked the device token: %v", err)
		}
		again, err := s.IssueDeviceToken(ctx, id, "900100001", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RequireDeviceToken(ctx, "Bearer "+device); err == nil {
			t.Error("the device's earlier token is live after a new one")
		}
		if _, err := s.RequireDeviceToken(ctx, "Bearer "+again); err != nil {
			t.Errorf("the new device token: %v", err)
		}
		if _, err := s.IssueDeviceToken(ctx, id, "a b", ""); err == nil {
			t.Error("a device token for an id that is no device id")
		}
	})
}
