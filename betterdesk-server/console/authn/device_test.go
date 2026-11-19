package authn

import (
	"context"
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
