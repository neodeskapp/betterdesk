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

// Settings, Sign-in, Security notices: a switch never saved is on, a save
// writes true or false for the switches it names and notes it in the audit
// log; an empty body and a value that is not true or false are refused.
func TestSecurityNoticeSwitches(t *testing.T) {
	s := newSite(t, nil, nil, nil)
	admin := s.As("admin")
	for _, key := range []string{"new_signin", "password_changed", "two_factor_changed", "email_changed"} {
		s.API.On("GET /api/config/security_notice_"+key, http.StatusOK, `{"value":""}`)
	}
	s.API.On("GET /api/config/security_notice_password_changed", http.StatusOK, `{"value":"false"}`)
	if res := admin.Do("GET", "/api/settings/security-notices", ""); res.Body != `{"success":true,"data":{"new_signin":true,"password_changed":false,"two_factor_changed":true,"email_changed":true}}` {
		t.Errorf("read: %s", res.Body)
	}
	s.API.Calls()
	s.API.On("PUT /api/config/security_notice_new_signin", http.StatusOK, `{"status":"ok"}`)
	if res := admin.Do("PUT", "/api/settings/security-notices", `{"new_signin":false}`); res.Body != `{"success":true}` {
		t.Errorf("save: %s", res.Body)
	}
	if calls := s.API.Calls(); len(calls) != 2 || calls[1] != `PUT /api/config/security_notice_new_signin {"value":"false"}` {
		t.Errorf("calls: %q", calls)
	}
	if row := lastAudit(t, s.World); row != "security_notices_changed|Security notices: new_signin=false|127.0.0.1|1" {
		t.Errorf("audit %s", row)
	}
	if res := admin.Do("PUT", "/api/settings/security-notices", `{}`); res.Status != http.StatusBadRequest {
		t.Errorf("empty body: %d %s", res.Status, res.Body)
	}
	if res := admin.Do("PUT", "/api/settings/security-notices", `{"new_signin":"no"}`); res.Status != http.StatusBadRequest {
		t.Errorf("text value: %d %s", res.Status, res.Body)
	}
	if res := s.As("operator").Do("GET", "/api/settings/security-notices", ""); res.Status != http.StatusForbidden {
		t.Errorf("operator: %d", res.Status)
	}
}
