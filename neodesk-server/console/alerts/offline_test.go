package alerts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// neverOnline returns the ids of the devices of a recorded device list
// whose last_online is a time before 1970, as JSON (a dump of the id).
// ambiguous is true when one of those ids is in the list a second time
// with another last_online: what that device raised cannot be told apart.
func neverOnline(t testing.TB, devices string) (never map[string]bool, ambiguous bool) {
	t.Helper()
	never, seen := map[string]bool{}, map[string]bool{}
	for _, d := range deviceObjects(t, devices) {
		if d == nil {
			continue
		}
		id, _ := json.Marshal(d.Get("id"))
		text, isText := d.Get("last_online").(string)
		if at, ok := dateParse(text, time.UTC); isText && ok && at < 0 {
			never[string(id)] = true
		} else {
			seen[string(id)] = true
		}
	}
	for id := range never {
		ambiguous = ambiguous || seen[id]
	}
	return never, ambiguous
}

// withoutDevices drops the lines of an expected outcome that name one of
// the devices: a JSON dump of a device id in it, or an id at the end of a
// log line.
func withoutDevices[T any](items []T, devices map[string]bool, text func(T) string) []T {
	var out []T
	for _, item := range items {
		s := text(item)
		named := false
		for id := range devices {
			plain := strings.Trim(id, `"`)
			if strings.Contains(s, id) || strings.HasSuffix(s, " for "+plain) {
				named = true
			}
		}
		if !named {
			out = append(out, item)
		}
	}
	return out
}

// A device that was never online has Go's zero time, or another time
// before 1970, as its last_online: it raises no offline alert, as a device
// without a last_online does (BUGS AE1).
func TestADeviceNeverOnlineRaisesNoOfflineAlert(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "offline_duration", "gte", 60, nil)
		engine := New(Options{DB: db, Logger: &logLines{}, Devices: listOf([]*goapi.Object{
			device("id", "zero", "online", false, "last_online", "0001-01-01T00:00:00Z"),
			device("id", "old", "online", false, "last_online", "1969-12-31T23:59:59Z"),
			device("id", "epoch", "online", false, "last_online", "1970-01-01T00:00:00Z"),
			device("id", "off-2h", "online", false, "last_online", "2026-11-09T10:00:00Z"),
		})})
		if err := engine.PeriodicCheckAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		got := alertsIn(t, db)
		if len(got) != 2 || got[0].ruleID != id || got[0].deviceID != "epoch" || got[1].deviceID != "off-2h" {
			t.Fatalf("alerts %+v, want epoch and off-2h", got)
		}
	})
}

// sweepNeverOnline is neverOnline for the device list of a recorded sweep,
// and nothing for the other functions, which have no device list.
func sweepNeverOnline(t testing.TB, fn, devices string) (map[string]bool, bool) {
	if fn != "sweep" {
		return nil, false
	}
	return neverOnline(t, devices)
}
