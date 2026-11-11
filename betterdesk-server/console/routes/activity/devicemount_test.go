package activity_test

import (
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/activity"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The read routes step aside only for the device mount written "/api/bd"
// letter for letter, as in the old console. In any other spelling of that
// mount they answer.
func TestTheDeviceMountInAnotherSpelling(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	activity.Register(w.Server.Deps().At("/api/bd"))

	if got := w.Do(replay.Request{Role: "admin", Path: "/API/BD/D1/top"}); got.Status != http.StatusOK || got.Body != `{"device_id":"D1","top_apps":[]}` {
		t.Errorf("GET /API/BD/D1/top as admin: %d %s, want the top apps", got.Status, got.Body)
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/Api/bd"}); got.Status != http.StatusOK || got.Body != `{"summaries":[],"total":0}` {
		t.Errorf("GET /Api/bd as admin: %d %s, want the list", got.Status, got.Body)
	}
	for _, path := range []string{"/api/bd", "/api/bd/D1", "/api/bd/D1/top"} {
		if got := w.Do(replay.Request{Role: "admin", Path: path}); got.Status != http.StatusNotFound || got.Body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("GET %s as admin: %d %s, want the console's 404", path, got.Status, got.Body)
		}
	}
}
