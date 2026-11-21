package cdap

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWidgetValuesKeepToTheManifest: a device keeps values only for the
// widgets of its manifest, each at most 64 KiB written as JSON, from
// heartbeats, state updates and bulk updates alike (BUGS RV166).
func TestWidgetValuesKeepToTheManifest(t *testing.T) {
	w := newCDAPWorld(t)
	createAPIKey(t, w.db, "apikey-w-0123456789abcdef", "w", "global_admin", "")
	dev := w.connect(t, "apikey-w-0123456789abcdef", "dev-W")
	dev.drain()
	w.settle(t, 1)
	dc := w.g.GetDeviceConn("dev-W")
	big := strings.Repeat("x", 70<<10)

	dev.send("state_update", map[string]any{"widget_id": "temp", "value": 21})
	dev.send("state_update", map[string]any{"widget_id": "temp", "value": big})
	dev.send("state_update", map[string]any{"widget_id": "nope", "value": 1})
	dev.send("heartbeat", map[string]any{"widget_values": map[string]any{"ghost": 2, "temp": big}})
	dev.send("bulk_update", map[string]any{"updates": []any{
		map[string]any{"widget_id": "phantom", "value": 3},
		map[string]any{"widget_id": "temp", "value": big},
	}})
	time.Sleep(500 * time.Millisecond)
	state := collectWidgetState(dc)
	if len(state) != 1 || state["temp"] != float64(21) {
		t.Errorf("widget state %v, want only temp at 21", strings.Join(keysOf(state), ","))
	}
	dev.send("bulk_update", map[string]any{"updates": []any{map[string]any{"widget_id": "temp", "value": 22}}})
	time.Sleep(300 * time.Millisecond)
	if got := collectWidgetState(dc)["temp"]; got != float64(22) {
		t.Errorf("temp after a bulk update = %v, want 22", got)
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k, v := range m {
		s, _ := v.(string)
		out = append(out, k+"="+strconv.Itoa(len(s)))
	}
	return out
}

// TestAlertsAreCapped: a manifest has at most as many alerts as widgets
// (BUGS RV166).
func TestAlertsAreCapped(t *testing.T) {
	manifest := func(n int) *Manifest {
		m := &Manifest{Device: ManifestDevice{Name: "d"}}
		for i := 0; i < n; i++ {
			m.Alerts = append(m.Alerts, AlertDef{ID: "a" + strconv.Itoa(i), Severity: "info", Condition: "x > 1"})
		}
		return m
	}
	if err := ValidateManifest(manifest(maxWidgets)); err != nil {
		t.Fatalf("%d alerts: %v", maxWidgets, err)
	}
	if err := ValidateManifest(manifest(maxWidgets + 1)); err == nil {
		t.Fatalf("%d alerts were taken", maxWidgets+1)
	}
}
