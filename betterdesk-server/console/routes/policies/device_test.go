package policies_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// withDevices mounts the group as register does, with the device token
// check on the group's database and the token rows of
// testdata/nodeoracle/device.js.
func withDevices(t *testing.T) func(d *kit.Deps) {
	return func(d *kit.Deps) {
		ctx := context.Background()
		service, err := authn.New(authn.Options{DB: d.Store, GoAPI: goapi.New(http.NotFoundHandler())})
		if err != nil {
			t.Fatal(err)
		}
		d.Authn = service
		if _, err := d.Store.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'device-owner', 'x', 'operator')"); err != nil {
			t.Fatal(err)
		}
		for _, row := range [][2]string{{"tok-device-a", "device-a"}, {"tok-device-b", "device-b"}, {"tok-numeric", "123"}, {"tok-unbound", ""}} {
			if _, err := d.Store.Exec(ctx, "INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at) VALUES (?, ?, 1, ?, '', '2999-01-01 00:00:00')",
				row[0], authn.HashAccessToken(row[0]), row[1]); err != nil {
				t.Fatal(err)
			}
		}
		register(d)
	}
}

// TestDeviceVectors replays what the device routes of the Node file
// answered and asked the API server, with the device token checks in front
// (testdata/nodeoracle/device.js).
func TestDeviceVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "device_vectors.json"), withDevices(t))
}
