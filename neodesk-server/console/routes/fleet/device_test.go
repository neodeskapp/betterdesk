package fleet_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// withDevices mounts the group as register does, with the device token
// check on the group's database and the token rows
// testdata/device_vectors.json was recorded with.
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
			if _, err := d.Store.Exec(ctx, "INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, token_kind) VALUES (?, ?, 1, ?, '', '2999-01-01 00:00:00', 'device')",
				row[0], authn.HashAccessToken(row[0]), row[1]); err != nil {
				t.Fatal(err)
			}
		}
		register(d)
	}
}

// deviceFixes are the vectors of device_vectors.json whose answer a bug
// fix of BUGS.md changed on purpose (proxytest.Replay).
var deviceFixes = slices.Concat(proxytest.Fixes("DV3",
	// A device_id that cannot be text names no device: the guard refuses
	// it as another device, at both mounts.
	15, 37, 59, 81,
), proxytest.Fixes("DV4",
	// Only text names a device: a number or a list is refused as another
	// device, where Node compared the text String() made of it.
	13, 14, 35, 36, 57, 58, 79, 80,
), proxytest.Fixes("FL6",
	// The body goes to the API server with every member, also __proto__,
	// constructor and prototype.
	19, 41, 63, 85,
))

// TestDeviceVectors replays what the device routes of the Node file
// answered and asked the API server, with the device token checks in front
// (testdata/device_vectors.json).
func TestDeviceVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "device_vectors.json"), withDevices(t), deviceFixes...)
}
