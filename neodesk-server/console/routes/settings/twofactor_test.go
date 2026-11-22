package settings_test

import (
	"net/http"
	"testing"
)

// Settings, Sign-in, "Require two-factor sign-in": the switch lives in the
// server's configuration; anything but "true" there is off, a save writes
// true or false and notes it in the audit log, and a body without the
// value is refused (A153).
func TestTwoFactorPolicy(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	s.API.On("GET /api/config/require_two_factor", http.StatusOK, `{"value":""}`)
	if res := admin.Do("GET", "/api/settings/two-factor", ""); res.Body != `{"success":true,"data":{"required":false}}` {
		t.Errorf("never saved: %s", res.Body)
	}
	s.API.On("GET /api/config/require_two_factor", http.StatusOK, `{"value":"true"}`)
	if res := admin.Do("GET", "/api/settings/two-factor", ""); res.Body != `{"success":true,"data":{"required":true}}` {
		t.Errorf("on: %s", res.Body)
	}
	s.API.Calls()

	s.API.On("PUT /api/config/require_two_factor", http.StatusOK, `{"status":"ok"}`)
	if res := admin.Do("PUT", "/api/settings/two-factor", `{"required":false}`); res.Body != `{"success":true}` {
		t.Errorf("save: %s", res.Body)
	}
	calls := s.API.Calls()
	if len(calls) != 2 || calls[1] != `PUT /api/config/require_two_factor {"value":"false"}` {
		t.Errorf("calls: %q", calls)
	}
	if row := lastAudit(t, s.World); row != "two_factor_policy_changed|Two-factor sign-in required: false|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	if res := admin.Do("PUT", "/api/settings/two-factor", `{}`); res.Status != http.StatusBadRequest {
		t.Errorf("empty body: %d %s", res.Status, res.Body)
	}
}
