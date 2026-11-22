package network

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The monitor's state is kept in the settings table: started, it runs
// again after a restart of the console with the interval it had; stopped,
// it stays stopped (BUGS.md NM10).
func TestMonitorRunsAgainAfterARestart(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		first := newWorld(t, db, names{})
		start, stop := `{"interval_ms":3600000}`, `{}`
		if rec := first.do("adm", "POST", "/api/network/monitor/start", &start, ""); rec.Code != 200 {
			t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
		}
		again := newWorld(t, db, names{})
		if !again.monitor.Running() || again.monitor.PollInterval() != 3600000 {
			t.Errorf("after a restart: running %v, interval %s, want running every 3600000 ms", again.monitor.Running(), again.monitor.PollInterval())
		}
		if rec := again.do("vera", "GET", "/api/network/monitor/status", nil, ""); rec.Body.String() != `{"running":true,"interval_ms":3600000}` {
			t.Errorf("status after a restart: %s", rec.Body.String())
		}
		if rec := again.do("adm", "POST", "/api/network/monitor/stop", &stop, ""); rec.Code != 200 {
			t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
		}
		if third := newWorld(t, db, names{}); third.monitor.Running() {
			t.Error("a stopped monitor runs after a restart")
		}
	})
}
