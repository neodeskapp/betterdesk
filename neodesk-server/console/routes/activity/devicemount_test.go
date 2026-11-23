package activity_test

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The read routes step aside for the device mount in any letter case, as
// the mount itself matches in any letter case (BUGS DV8): below /API/ND
// they answer nothing, and neither does the inventory group, which the
// console mounts at /api/nd before this one. At the group's own mount
// they answer in any spelling.
func TestTheDeviceMountInAnotherSpelling(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)

	for _, path := range []string{"/API/ND/D1/top", "/Api/nd", "/Api/nd/D1", "/api/nd", "/api/nd/D1", "/api/nd/D1/top"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusNotFound || got.Body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("GET %s as admin: %d %s, want the console's 404", path, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/API/Activity/D1/top"}); got.Status != http.StatusOK || got.Body != `{"device_id":"D1","top_apps":[]}` {
		t.Errorf("GET /API/Activity/D1/top as admin: %d %s, want the top apps", got.Status, got.Body)
	}
}
