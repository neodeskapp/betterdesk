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
