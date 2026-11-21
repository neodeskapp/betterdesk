package resourcecontrol_test

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
var deviceFixes = proxytest.Fixes("DV1",
	// The resource policy is the token's device's: a request that names
	// its own device in the query or the body gets that policy whatever
	// X-Device-Id or the query names, at both mounts.
	11, 12, 14, 15, 16, 17, 18, 19, 20, 21, 22,
	34, 35, 37, 38, 39, 40, 41, 42, 43, 44, 45,
)

// TestDeviceVectors replays what the device routes of the Node file
// answered and asked the API server, with the device token checks in front
// (testdata/nodeoracle/device.js).
func TestDeviceVectors(t *testing.T) {
	proxytest.Replay(t, filepath.Join("testdata", "device_vectors.json"), withDevices(t), deviceFixes...)
}

// TestResourcePolicyIsTheTokensDevice: the route fetches the resource
// policy of the device the token is bound to. A request that names its own
// device in the query may name another in X-Device-Id, and one that names
// it in the body may name others in the query; they do not choose the
// policy (BUGS.md DV1).
func TestResourcePolicyIsTheTokensDevice(t *testing.T) {
	w := proxytest.NewWorld(t, withDevices(t))
	policy := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"usb":{}}`}
	own := `{"device_id":"device-a"}`
	for _, req := range []proxytest.Request{
		{Path: "/api/bd/resource-policy?device_id=device-a", Headers: map[string]string{"X-Device-Id": "device-b"}},
		{Path: "/api/bd/resource-policy?device_id=device-a", Headers: map[string]string{"X-Device-Id": " device-c "}},
		{Path: "/api/bd/resource-policy?device_id=device-a", Headers: map[string]string{"X-Device-Id": "bad id"}},
		{Path: "/api/bd/resource-policy?device_id=device-c", Body: &own},
		{Path: "/api/bd/resource-policy?device_id[]=device-c", Body: &own},
		{Path: "/api/bd/resource-policy?device_id[toString]=x", Body: &own},
		{Path: "/api/bd/api/bd/resource-policy?device_id=device-a", Headers: map[string]string{"X-Device-Id": "device-b"}},
	} {
		if req.Body != nil {
			req.Type = "application/json"
		}
		headers := map[string]string{"Authorization": "Bearer tok-device-a"}
		for name, value := range req.Headers {
			headers[name] = value
		}
		req.Headers = headers
		rec, calls := w.Do(req, policy)
		if rec.Code != 200 || rec.Body.String() != `{"usb":{}}` || len(calls) != 1 || calls[0].URL != "/api/devices/device-a/resources" {
			t.Errorf("GET %s %v: %d %s after %+v; want the policy of device-a", req.Path, req.Headers, rec.Code, rec.Body, calls)
		}
	}
}
