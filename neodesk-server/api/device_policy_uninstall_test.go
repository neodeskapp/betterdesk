package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// The device's policy says whether people may remove the app: allowed
// unless its organization turned it off, also for a device without one.
func TestDevicePolicySaysWhetherTheAppMayBeRemoved(t *testing.T) {
	placementStores(t, config.EnrollmentModeOpen, func(t *testing.T, f *placementFixture) {
		policyOf := func(device string) map[string]any {
			t.Helper()
			status, raw := asConsole(t, f.srv, http.MethodGet, "/api/peers/"+device+"/policy", "")
			var out struct {
				Policies map[string]map[string]any `json:"policies"`
			}
			if err := json.Unmarshal([]byte(raw), &out); err != nil || status != http.StatusOK {
				t.Fatalf("policy of %s: %d %s", device, status, raw)
			}
			return out.Policies["security"]
		}
		f.register(t, map[string]any{"device_id": "LONE00001"})
		if got := policyOf("LONE00001"); got["allow_uninstall"] != true {
			t.Errorf("device without an organization: security %v, want allow_uninstall true", got)
		}
		f.register(t, map[string]any{"device_id": "ORG000001", "token": f.token(t, 0, f.org)})
		if got := policyOf("ORG000001"); got["allow_uninstall"] != true {
			t.Errorf("organization without the rule: security %v, want allow_uninstall true", got)
		}
		if status, raw := asConsole(t, f.srv, http.MethodPut, "/api/org/"+f.org+"/policy/security", `{"require_2fa":true,"allow_uninstall":false}`); status != http.StatusOK {
			t.Fatalf("save the security policy: %d %s", status, raw)
		}
		if got := policyOf("ORG000001"); got["allow_uninstall"] != false || got["require_2fa"] != true {
			t.Errorf("organization that blocks removal: security %v, want allow_uninstall false and the rest kept", got)
		}
	})
}
