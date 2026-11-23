package automation_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// knownDevices stands in for the API server: it knows the devices D1 to
// D9 and no other.
var knownDevices = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id, ok := strings.CutPrefix(r.URL.Path, "/api/peers/")
	if ok && r.Method == http.MethodGet && len(id) == 2 && id[0] == 'D' && id[1] >= '1' && id[1] <= '9' {
		_, _ = w.Write([]byte(`{"id":"` + id + `","hostname":"pc-` + id + `"}`))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"Peer not found"}`))
})

// newWorld is the console of the tests, with the devices of knownDevices.
func newWorld(t *testing.T, db *store.DB) *replay.World {
	t.Helper()
	return replay.NewWorldWithAPI(t, db, nil, nil, knownDevices)
}

// A command goes to a device the API server knows, named by its id as
// text (BUGS AU8). Anything else is 400 and stores nothing, where the old
// route stored any value that is not empty: the number 7 waited for the
// device "7.0" on SQLite, which no agent asks for.
func TestACommandNeedsAKnownDevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		for device, want := range map[string]string{
			`7`: "Invalid device_id", `1.5`: "Invalid device_id", `true`: "Invalid device_id", `["D1"]`: "Invalid device_id", `[7]`: "Invalid device_id",
			`{"id":"D1"}`: "Invalid device_id", `{"toString":1}`: "Invalid device_id", `[["x"],{"toString":null}]`: "Invalid device_id",
			`"unknown"`: "Unknown device", `" D1 "`: "Unknown device", `"d1"`: "Unknown device", `"D10"`: "Unknown device", `"D1/../D2"`: "Unknown device",
			`""`: "device_id is required", `0`: "device_id is required", `null`: "device_id is required", `false`: "device_id is required",
		} {
			got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":` + device + `,"payload":"whoami"}`)})
			if got.Status != 400 || got.Body != `{"error":"`+want+`"}` {
				t.Errorf("a command for %s: %d %s, want 400 %s", device, got.Status, got.Body, want)
			}
		}
		if got := w.Rows("SELECT id FROM remote_commands", nil) + w.Rows("SELECT action FROM audit_log", nil); got != "[][]" {
			t.Errorf("the refused commands left %s", got)
		}
		got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/automation/commands", Body: replay.Text(`{"device_id":"D1","payload":"whoami"}`)})
		if got.Status != 201 {
			t.Fatalf("a command for D1: %d %s", got.Status, got.Body)
		}
		if got := w.Rows("SELECT device_id FROM remote_commands", nil) + w.Rows("SELECT action, details FROM audit_log", nil); got != `[{"device_id":"D1"}][{"action":"remote_command_sent","details":"shell to D1"}]` {
			t.Errorf("after the command for D1: %s", got)
		}
	})
}
