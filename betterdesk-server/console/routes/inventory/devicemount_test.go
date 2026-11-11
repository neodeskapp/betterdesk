package inventory_test

import (
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/routes/inventory"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The old console mounted the group at /api/bd before every other group
// there, and its read routes stepped aside only for the mount written
// "/api/bd" letter for letter. So a GET below /API/BD in another spelling
// is answered by the inventory detail route (which knows no such device),
// and the route a later group has for that address never sees it; GET
// /API/BD/Operator-Info/ was the case that showed it. With the group
// registered at /api/bd, the Go console answers the same. The later route
// here has an address no group of the console has, because the test adds
// the inventory routes after the console registered its own.
func TestTheDeviceMountInAnotherSpelling(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	inventory.Register(w.Server.Deps().At("/api/bd"))
	// stands for the route of a later group
	w.Server.Router().Get("/api/bd/later-info", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"answered": "later"})
	})

	signedOut := w.Do(replay.Request{Path: "/API/BD/Later-Info/", Headers: map[string]string{"Accept": "text/html"}})
	if signedOut.Status != http.StatusFound || signedOut.Header.Get("Location") != "/login" {
		t.Errorf("signed out: %d to %q, want the inventory route's 302 to /login", signedOut.Status, signedOut.Header.Get("Location"))
	}
	for _, path := range []string{"/API/BD/Later-Info/", "/API/BD/later-info", "/Api/Bd/Later-Info", "/api/bD/later-info"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusNotFound || got.Body != `{"error":"No inventory data for this device"}` {
			t.Errorf("GET %s as admin: %d %s, want the inventory route's 404", path, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/API/BD"}); got.Status != http.StatusOK || got.Body != `{"devices":[],"total":0,"page":1,"limit":50,"total_pages":0}` {
		t.Errorf("GET /API/BD as admin: %d %s, want the inventory list", got.Status, got.Body)
	}
	// The mount written exactly: the group steps aside
	for _, path := range []string{"/api/bd/later-info", "/api/bd/Later-Info/"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusOK || got.Body != `{"answered":"later"}` {
			t.Errorf("GET %s as admin: %d %s, want the later route", path, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/api/bd"}); got.Status != http.StatusNotFound || got.Body != `{"success":false,"error":"Not Found"}` {
		t.Errorf("GET /api/bd as admin: %d %s, want the console's 404", got.Status, got.Body)
	}
}
