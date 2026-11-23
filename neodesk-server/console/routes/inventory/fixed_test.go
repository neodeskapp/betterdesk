package inventory_test

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
)

// deviceFixes are the steps of the device routes' recordings whose
// outcome a bug fix changed on purpose (see replay.Fixes).
var deviceFixes replay.Fixes

// memoryAlerts are the alerts of the recordings' memory rule (rule 3).
const memoryAlerts = "SELECT rule_id, device_id, severity, message FROM alert_history WHERE rule_id = 3 ORDER BY id"

// AE5: the alert engine checks memory at 0 bytes used, where Node took 0
// for "no value". The telemetry is answered and stored as before; only
// the memory alert differs, and After puts Node's alerts back for the
// steps that read them. The engine checks no memory for a total below 0
// either (console/alerts tests that), but no upload brings it one: the
// route refuses such a total before it stores or checks anything (BUGS
// AI15, typedUploads in stored_test.go).
var _ = deviceFixes.Add("AE5", map[string]replay.Fix{
	` POST /api/nd/telemetry {"device_id":"device-a","cpu_usage_percent":50,"memory_used_bytes":0,"memory_total_bytes":2097152,"uptime_secs":60,"timestamp":"2011-03-04T10:00:00.000Z"}`: {
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
})

// reportOfPCA is the report of the recordings' uploads without a device
// id in the body, after that member.
const reportOfPCA = `"hardware":{"hostname":"pc-a","os_name":"Windows 11 Pro","os_version":"23H2","cpu":{"brand":"Intel i5","logical_cores":8,"usage_percent":12.5},"memory":{"total_bytes":17179869184,"used_bytes":1048576},"disks":[{"mount_point":"C:","total_bytes":200,"used_bytes":150}]},"software":{"apps":[{"name":"Editor"}]},"collected_at":"2011-03-04T10:00:00.000Z"}`

// AI18: an upload is stored for the device of its token, also when its
// body names no device, where Node answered 400 "Missing device_id or
// hardware" or "Missing device_id". The alert rules see what it stored:
// After checks the alerts it raised for device-a, which has none in the
// recordings at these steps, and removes them, so that the steps after it
// read Node's alerts. What it stored, the device's next upload replaces,
// as the recordings' statements show.
var _ = deviceFixes.Add("AI18", map[string]replay.Fix{
	` POST /api/nd/inventory {` + reportOfPCA:                          storedFor(diskAlert),
	` POST /api/nd/inventory {"device_id":null,` + reportOfPCA:         storedFor(diskAlert),
	` POST /api/nd/inventory {"device_id":"",` + reportOfPCA:           storedFor(diskAlert),
	` POST /api/nd/telemetry {"cpu_usage_percent":5}`:                  storedFor(cpuAlert),
	` POST /api/nd/telemetry {"device_id":null,"cpu_usage_percent":5}`: storedFor(cpuAlert),
	` POST /api/nd/telemetry {"device_id":"","cpu_usage_percent":5}`:   storedFor(cpuAlert),
	` POST /api/nd/telemetry {}`:                                       storedFor(""),
})

// The alerts an upload of device-a raises: a full disk, a CPU load.
const (
	diskAlert = `{"rule_id":1,"device_id":"device-a","severity":"critical","message":"any disk: disk_usage = 75 (neq -0.5)"}`
	cpuAlert  = `{"rule_id":2,"device_id":"device-a","severity":"warning","message":"any cpu: cpu_usage = 5 (neq -0.5)"}`
)

// storedFor is the fix of an upload of device-a that is stored and raises
// the alert raised ("" for none).
func storedFor(raised string) replay.Fix {
	return replay.Fix{
		Status: 200, Answer: `{"success":true}`,
		After: func(t *testing.T, w *replay.World, step replay.Step) {
			w.Server.Deps().Alerts.Wait()
			const alerts = "SELECT rule_id, device_id, severity, message FROM alert_history WHERE device_id = 'device-a' ORDER BY id"
			if have := w.Rows(alerts, nil); have != "["+raised+"]" {
				t.Errorf("%s (AI18): alerts of device-a %s, want [%s]", step.Name, have, raised)
			}
			w.Exec("DELETE FROM alert_history WHERE device_id = 'device-a'", nil)
		},
	}
}
