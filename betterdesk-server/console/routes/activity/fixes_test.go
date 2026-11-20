package activity_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// activityFixes are the steps of testdata/node_*.json that the bug fixes
// change.
var activityFixes replay.Fixes

// A device with a note is listed under its note, also when the console
// cannot read its info (BUGS AI5).
func TestTheNoteNamesADeviceWhoseInfoIsUnreadable(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for id, info := range map[string]string{"D1": db.Pick("not json", `"not json"`), "D2": db.Pick("null", `"null"`)} {
			w.Exec("INSERT INTO peer (id, note, info) VALUES (?, 'front desk', ?)", []any{id, info})
			w.Exec("INSERT INTO activity_summaries (device_id, idle_seconds, session_count, total_active_secs, reported_at) VALUES (?, 0, 1, 60, '2019-03-04T00:00:00.000Z')", []any{id})
		}
		for _, path := range []string{"/api/activity", "/api/activity/D1", "/api/activity/D2"} {
			got := w.Do(replay.Request{Role: "admin", Path: path})
			if got.Status != 200 || !strings.Contains(got.Body, `"hostname":"front desk"`) || strings.Contains(got.Body, `"hostname":"D`) {
				t.Errorf("GET %s: %d %s, want every device under its note", path, got.Status, got.Body)
			}
		}
	})
}

// notesOf reads the notes of the devices the seed of a recording puts
// into the console's table.
func notesOf(t *testing.T, rec replay.Recording) map[string]string {
	t.Helper()
	notes := map[string]string{}
	for _, seed := range rec.Seed {
		if !strings.HasPrefix(seed.SQL, "INSERT INTO peer (id, note") {
			continue
		}
		id, _ := seed.Params[0].(string)
		note, _ := seed.Params[1].(string)
		notes[id] = note
	}
	if len(notes) < 10 {
		t.Fatalf("the seed has the notes of %d devices", len(notes))
	}
	return notes
}

// renamed checks the steps whose answer differs from Node's only in the
// names devices are shown under, and counts them. A device with a note
// is shown under its note where Node showed its id because it could not
// read the device's info (BUGS AI5).
func renamed(notes map[string]string, count *int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Status != 200 || got.Status != 200 || got.Body == step.Answer {
			return false
		}
		node, have := decoded(step.Answer), decoded(got.Body)
		if node == nil || have == nil {
			return false
		}
		rename(node, have, notes)
		if !reflect.DeepEqual(node, have) {
			return false
		}
		*count++
		return true
	}
}

// decoded is a JSON text as Go values, nil when it is no JSON.
func decoded(text string) any {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil
	}
	return v
}

// rename walks Node's answer beside the Go console's and puts the name
// the fixes give a device into each object of Node's that names one.
func rename(node, have any, notes map[string]string) {
	switch n := node.(type) {
	case map[string]any:
		h, ok := have.(map[string]any)
		if !ok {
			return
		}
		if id, isText := n["device_id"].(string); isText {
			if name, ok := n["hostname"]; ok && name == id && notes[id] != "" && h["hostname"] == notes[id] {
				n["hostname"] = notes[id]
			}
		}
		for key, value := range n {
			rename(value, h[key], notes)
		}
	case []any:
		h, ok := have.([]any)
		if !ok || len(h) != len(n) {
			return
		}
		for i := range n {
			rename(n[i], h[i], notes)
		}
	}
}
