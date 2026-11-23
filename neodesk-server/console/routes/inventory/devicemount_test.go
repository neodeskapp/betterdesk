package inventory_test

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The console mounts the group at /api/nd before every other group there,
// and its read routes step aside for that mount in any letter case, as the
// mount itself matches in any letter case (BUGS DV8). So a GET below
// /API/ND in another spelling reaches the route a later group has for the
// address, as the lower-case address does; GET /API/ND/Operator-Info/ was
// the case that showed it. The later route here stands for such a route,
// with an address no group of the console has; the test adds it after the
// console registered every group.
func TestTheDeviceMountInAnotherSpelling(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	// stands for the route of a later group
	w.Server.Router().Get("/api/nd/later-info", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"answered": "later"})
	})

	signedOut := w.Do(replay.Request{Path: "/API/ND/Later-Info/", Headers: map[string]string{"Accept": "text/html"}})
	if signedOut.Status != http.StatusOK || signedOut.Body != `{"answered":"later"}` {
		t.Errorf("signed out: %d %s, want the later route", signedOut.Status, signedOut.Body)
	}
	for _, path := range []string{"/API/ND/Later-Info/", "/API/ND/later-info", "/Api/Nd/Later-Info", "/api/nD/later-info", "/api/nd/later-info", "/api/nd/Later-Info/"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusOK || got.Body != `{"answered":"later"}` {
			t.Errorf("GET %s as admin: %d %s, want the later route", path, got.Status, got.Body)
		}
	}
	for _, path := range []string{"/API/ND", "/api/nd"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusNotFound || got.Body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("GET %s as admin: %d %s, want the console's 404", path, got.Status, got.Body)
		}
	}
	// At its own mount the group answers in any spelling
	if got := w.Do(replay.Request{Role: "admin", Path: "/API/Inventory"}); got.Status != http.StatusOK || got.Body != `{"devices":[],"total":0,"page":1,"limit":50,"total_pages":0}` {
		t.Errorf("GET /API/Inventory as admin: %d %s, want the inventory list", got.Status, got.Body)
	}
}
