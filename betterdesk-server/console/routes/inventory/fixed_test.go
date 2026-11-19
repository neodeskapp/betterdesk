package inventory_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
)

// deviceFixes are the steps of the device routes' recordings whose
// outcome a bug fix changed on purpose (see replay.Fixes).
var deviceFixes replay.Fixes

// memoryAlerts are the alerts of the recordings' memory rule (rule 3).
const memoryAlerts = "SELECT rule_id, device_id, severity, message FROM alert_history WHERE rule_id = 3 ORDER BY id"

// AE5: the alert engine checks memory at 0 bytes used, where Node took 0
// for "no value", and not for a total below 0, which Node divided by. The
// telemetry is answered and stored as before; only the memory alert
// differs, and After puts Node's alerts back for the steps that read them.
var _ = deviceFixes.Add("AE5", map[string]replay.Fix{
	` POST /api/bd/telemetry {"device_id":"device-a","cpu_usage_percent":50,"memory_used_bytes":0,"memory_total_bytes":2097152,"uptime_secs":60,"timestamp":"2011-03-04T10:00:00.000Z"}`: {
		Status: 200, Answer: `{"success":true}`,
		After: func(t *testing.T, w *replay.World, step replay.Step) {
			w.Server.Deps().Alerts.Wait()
			want := `[{"rule_id":3,"device_id":"device-a","severity":"info","message":"any memory: memory_usage = 0 (neq -0.5)"}]`
			if have := w.Rows(memoryAlerts, nil); have != want {
				t.Errorf("%s (AE5): memory alerts %s, want %s", step.Name, have, want)
			}
			w.Exec("DELETE FROM alert_history WHERE rule_id = 3", nil)
		},
	},
	` POST /api/bd/telemetry {"device_id":"device-a","cpu_usage_percent":50,"memory_used_bytes":1048576,"memory_total_bytes":-1,"uptime_secs":60,"timestamp":"2011-03-04T10:00:00.000Z"}`: {
		Status: 200, Answer: `{"success":true}`,
		After: func(t *testing.T, w *replay.World, step replay.Step) {
			w.Server.Deps().Alerts.Wait()
			if have := w.Rows(memoryAlerts, nil); have != "[]" {
				t.Errorf("%s (AE5): memory alerts %s, want none", step.Name, have)
			}
			replay.Write(`INSERT INTO alert_history (rule_id, device_id, severity, message, triggered_at)
				VALUES (3, 'device-a', 'info', 'any memory: memory_usage = -104857600 (neq -0.5)', <now>)`)(t, w, step)
		},
	},
})
