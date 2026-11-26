package settings_test

import (
	"net/http"
	"testing"
)

// Settings, Sign-in, "Disable inactive accounts": off until saved, a
// number of days from 14 to 3650 or 0 for off, kept in the server's
// configuration and noted in the audit log; anything else is refused and
// saves nothing. It needs server.config.
func TestInactiveAccountsSetting(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	s.API.On("GET /api/config/inactive_disable_days", http.StatusOK, `{"value":""}`)
	if res := admin.Do("GET", "/api/settings/inactive-accounts", ""); res.Body != `{"success":true,"data":{"days":0,"notice_days":7,"min_days":14,"max_days":3650}}` {
		t.Errorf("never saved: %s", res.Body)
	}
	s.API.On("GET /api/config/inactive_disable_days", http.StatusOK, `{"value":"90"}`)
	if res := admin.Do("GET", "/api/settings/inactive-accounts", ""); res.Body != `{"success":true,"data":{"days":90,"notice_days":7,"min_days":14,"max_days":3650}}` {
		t.Errorf("90: %s", res.Body)
	}
	s.API.Calls()
	for _, body := range []string{`{"days":5}`, `{"days":30.5}`, `{"days":"60"}`, `{"days":9999}`} {
		if res := admin.Do("PUT", "/api/settings/inactive-accounts", body); res.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
	}
	if calls := s.API.Calls(); len(calls) != 0 {
		t.Errorf("refused saves wrote: %q", calls)
	}
	s.API.On("PUT /api/config/inactive_disable_days", http.StatusOK, `{"status":"ok"}`)
	if res := admin.Do("PUT", "/api/settings/inactive-accounts", `{"days":60}`); res.Body != `{"success":true}` {
		t.Errorf("save: %s", res.Body)
	}
	calls := s.API.Calls()
	if len(calls) != 2 || calls[1] != `PUT /api/config/inactive_disable_days {"value":"60"}` {
		t.Errorf("calls: %q", calls)
	}
	if row := lastAudit(t, s.World); row != "inactive_policy_changed|Disable accounts that have not signed in for 60 days|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	if res := s.As("viewer").Do("PUT", "/api/settings/inactive-accounts", `{"days":60}`); res.Status != http.StatusForbidden {
		t.Errorf("viewer: %d", res.Status)
	}
}
