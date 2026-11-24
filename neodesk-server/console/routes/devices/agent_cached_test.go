package devices_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestCachedLiveViewAsksForANewOne: a NeoDesk client has no live agent
// connection, so its Live data views come from the last snapshot. A
// snapshot older than a minute is answered and a new collection is queued
// with it, so the view does not stay at what the device sent days ago; a
// fresh one is answered alone. Before, the old snapshot came back and
// nothing was asked for (L480).
func TestCachedLiveViewAsksForANewOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		received string
		queued   bool
	}{
		{"old snapshot", "2026-11-23 21:07:24", true},
		{"fresh snapshot", time.Now().UTC().Add(-10 * time.Second).Format("2006-01-02 15:04:05"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := storetest.Migrated(t)
			api := routetest.NewAPI()
			api.Fallback = func(c routetest.Call) routetest.Reply {
				switch {
				case c.Method == "GET" && c.Target == "/api/peers/A1001":
					return routetest.Reply{Status: 200, Body: `{"id":"A1001","hostname":"a","online":true}`}
				case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/peers/A1001/telemetry"):
					return routetest.Reply{Status: 200, Body: `{"snapshots":{"services":{"data":{"services":[{"name":"ALG"}]},` +
						`"collected_at":"2026-11-23T21:07:22Z","received_at":"` + tc.received + `"}}}`}
				case c.Method == "POST" && c.Target == "/api/peers/A1001/telemetry/command":
					return routetest.Reply{Status: 200, Body: `{"status":"queued","id":7}`}
				}
				return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
			}
			w := routetest.New(t, routetest.Options{DB: db, API: api, Env: raisedLimits, Config: nodeLanguages})
			api.Calls()
			got := w.As("admin").Do("GET", "/api/devices/A1001/services", "")
			if got.Status != http.StatusOK || !strings.Contains(got.Body, `"cached":true`) || !strings.Contains(got.Body, `"name":"ALG"`) {
				t.Fatalf("services: %d %s, want 200 with the cached snapshot", got.Status, got.Body)
			}
			queued := false
			for _, call := range api.Calls() {
				if strings.HasPrefix(call, "POST /api/peers/A1001/telemetry/command") {
					queued = true
				}
			}
			if queued != tc.queued {
				t.Errorf("collection queued: %v, want %v (answer %s)", queued, tc.queued, got.Body)
			}
			if pending := strings.Contains(got.Body, `"pending":true`); pending != tc.queued {
				t.Errorf("answer says pending: %v, want %v (%s)", pending, tc.queued, got.Body)
			}
		})
	}
}
