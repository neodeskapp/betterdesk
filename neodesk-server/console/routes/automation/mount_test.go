package automation_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// At /api/automation every route can answer: the device's list of
// commands is registered at /api/bd only, because at /api/automation the
// people's list of the same path answers every request first (BUGS I5).
func TestAutomationMountHasNoUnreachableRoute(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	seen := map[string]int{}
	for _, route := range w.Server.Router().Routes() {
		if strings.HasPrefix(route.Path, "/api/automation/") {
			seen[route.Method+" "+route.Path]++
		}
	}
	for route, n := range seen {
		if n > 1 {
			t.Errorf("%s is registered %d times at /api/automation; only the first can answer", route, n)
		}
	}
	if seen["GET /api/automation/commands"] != 1 {
		t.Errorf("GET /api/automation/commands is registered %d times, want once", seen["GET /api/automation/commands"])
	}
}
