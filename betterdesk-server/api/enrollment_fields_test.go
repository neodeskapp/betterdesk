package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestEnrollmentRoutesCheckTheDeviceID: registration and pairing redeem
// take a device id only in the pattern the device layer uses and refuse
// overlong fields before anything is stored; a plain id still registers
// (RV178).
func TestEnrollmentRoutesCheckTheDeviceID(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	long := strings.Repeat("h", 5000)
	for _, c := range []struct {
		name, path, body, id string
	}{
		{"markup id", "/api/devices/register", `{"device_id":"<img src=x onerror=alert(1)>/../a b\tc","hostname":"h"}`, "<img src=x onerror=alert(1)>/../a b\tc"},
		{"overlong id", "/api/devices/register", `{"device_id":"` + strings.Repeat("9", 300) + `"}`, strings.Repeat("9", 300)},
		{"overlong hostname", "/api/devices/register", `{"device_id":"NEWDEV01","hostname":"` + long + `"}`, "NEWDEV01"},
		{"overlong tags", "/api/devices/register", `{"device_id":"NEWDEV02","tags":"` + long + `"}`, "NEWDEV02"},
		{"redeem markup id", "/api/pairing/redeem", `{"code":"ABCD-EFGH","device_id":"a/b c"}`, "a/b c"},
		{"redeem overlong hostname", "/api/pairing/redeem", `{"code":"ABCD-EFGH","device_id":"NEWDEV03","hostname":"` + long + `"}`, "NEWDEV03"},
	} {
		// Redeem asks a credential on this port, registration none.
		status, body := listenerCall(t, srv, http.MethodPost, c.path, c.body, map[string]string{"X-API-Key": testViewerKey})
		if status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", c.name, status, body)
		}
		if p, _ := database.GetPeer(c.id); p != nil {
			t.Errorf("%s: a device row was stored", c.name)
		}
		if pending, _ := database.GetConfig(pendingDevicePrefix + c.id); pending != "" {
			t.Errorf("%s: a pending request was stored", c.name)
		}
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/devices/register", `{"device_id":"NEWDEV09","hostname":"box"}`, nil); status != http.StatusOK {
		t.Errorf("a plain device id: %d %s, want 200", status, body)
	}
}
