package policies_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
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

// deviceFixes are the vectors of device_vectors.json whose answer a bug
// fix of BUGS.md changed on purpose (proxytest.Replay).
var deviceFixes = slices.Concat(proxytest.Fixes("DV1",
	// The policy is the token's device's: a request that names its own
	// device in the body gets that policy whatever the query or
	// X-Device-Id names, at both mounts.
	14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
	41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53,
), proxytest.Fixes("DV4",
	// Only text names a device: a number or a list is refused as another
	// device, where Node compared the text String() made of it.
	92, 93, 114, 115,
), proxytest.Fixes("FL6",
	// The body goes to the API server with every member, also __proto__,
	// constructor and prototype.
	97, 119,
))

// TestDeviceVectors replays what the device routes of the Node file
// answered and asked the API server, with the device token checks in front
// (testdata/nodeoracle/device.js).
func TestDeviceVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "device_vectors.json"), withDevices(t), deviceFixes...)
}

// TestDevicePolicyIsTheTokensDevice: the route fetches the policy of the
// device the token is bound to. A request that names its own device in the
// body may name others in the query and X-Device-Id; they do not choose
// the policy (BUGS.md DV1).
func TestDevicePolicyIsTheTokensDevice(t *testing.T) {
	w := proxytest.NewWorld(t, withDevices(t))
	policy := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"rules":{}}`}
	own := `{"device_id":"device-a"}`
	for _, req := range []proxytest.Request{
		{Path: "/api/bd/device-policy?device_id=device-b", Body: &own},
		{Path: "/api/bd/device-policy", Headers: map[string]string{"X-Device-Id": "device-b"}, Body: &own},
		{Path: "/api/bd/device-policy?device_id=device-c", Headers: map[string]string{"X-Device-Id": "device-b"}, Body: &own},
		{Path: "/api/bd/device-policy?device_id[]=device-c", Body: &own},
		{Path: "/api/bd/device-policy?device_id[toString]=x", Body: &own},
		{Path: "/api/bd/device-policy?device_id=bad%20id", Body: &own},
		{Path: "/api/bd/api/bd/device-policy?device_id=device-b", Body: &own},
	} {
		req.Type = "application/json"
		req.Headers = withToken(req.Headers, "tok-device-a")
		rec, calls := w.Do(req, policy)
		if rec.Code != 200 || rec.Body.String() != `{"rules":{}}` || len(calls) != 1 || calls[0].URL != "/api/peers/device-a/policy" {
			t.Errorf("GET %s %v: %d %s after %+v; want the policy of device-a", req.Path, req.Headers, rec.Code, rec.Body, calls)
		}
	}
}

// withToken adds the device token to a request's headers.
func withToken(headers map[string]string, token string) map[string]string {
	out := map[string]string{"Authorization": "Bearer " + token}
	for name, value := range headers {
		out[name] = value
	}
	return out
}
