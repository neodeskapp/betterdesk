package api

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestOrgPolicyIsCheckedBeforeItIsStored: a policy group is stored only
// with known settings of the right kind and range, as the Policies page
// offers them; anything else is refused with 400 and a reason naming the
// setting, and the stored group stays as it was (BUGS L395).
func TestOrgPolicyIsCheckedBeforeItIsStored(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateOrganization(&db.Organization{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	good := map[string]string{
		"connection": `{"mode":"attended","max_session_duration":480,"idle_lock_timeout":15,"wallpaper":"hide_during_session"}`,
		"features":   `{"allow_file_transfer":true,"allow_clipboard":false,"allow_audio":false,"allow_recording":true}`,
		"security":   `{"require_2fa":true,"password_min_length":12,"password_require_special":true,"password_max_age_days":90,"allowed_operators":["admin","op1"],"allow_uninstall":false}`,
		"network":    `{"block_direct_p2p":true,"allowed_relay_servers":["relay.example.com:21117","relay2.example.com","[2001:db8::1]:21117"],"ip_allowlist":["192.168.1.0/24","10.0.0.5","2001:db8::/32"]}`,
		"update":     `{"auto_update":false,"channel":"beta"}`,
	}
	for category, body := range good {
		if status, answer := asConsole(t, srv, http.MethodPut, "/api/org/org-a/policy/"+category, body); status != http.StatusOK {
			t.Fatalf("%s %s: %d %s, want 200", category, body, status, answer)
		}
	}
	for _, c := range []struct{ category, body, reason string }{
		{"connection", `{"mode":"whatever"}`, "mode"},
		{"connection", `{"max_session_duration":-5}`, "max_session_duration"},
		{"connection", `{"max_session_duration":1441}`, "max_session_duration"},
		{"connection", `{"idle_lock_timeout":"abc"}`, "idle_lock_timeout"},
		{"connection", `{"idle_lock_timeout":2.5}`, "idle_lock_timeout"},
		{"connection", `{"wallpaper":7}`, "wallpaper"},
		{"connection", `["attended","unattended"]`, "JSON object"},
		{"connection", `{"mode":"attended","typo_setting":1}`, "typo_setting"},
		{"features", `{"allow_clipboard":"no"}`, "allow_clipboard"},
		{"security", `{"password_min_length":5}`, "password_min_length"},
		{"security", `{"password_max_age_days":400}`, "password_max_age_days"},
		{"security", `{"allowed_operators":"admin"}`, "allowed_operators"},
		{"security", `{"allow_uninstall":"no"}`, "allow_uninstall"},
		{"network", `{"block_direct_p2p":"yes"}`, "block_direct_p2p"},
		{"network", `{"allowed_relay_servers":"not a list"}`, "allowed_relay_servers"},
		{"network", `{"allowed_relay_servers":["relay with space"]}`, "allowed_relay_servers"},
		{"network", `{"allowed_relay_servers":["relay.example.com:99999"]}`, "allowed_relay_servers"},
		{"network", `{"ip_allowlist":["not-a-cidr"]}`, "ip_allowlist"},
		{"network", `{"ip_allowlist":["999.1.1.1/77"]}`, "ip_allowlist"},
		{"update", `{"channel":"nightly"}`, "channel"},
	} {
		status, answer := asConsole(t, srv, http.MethodPut, "/api/org/org-a/policy/"+c.category, c.body)
		if status != http.StatusBadRequest || !strings.Contains(answer, c.reason) {
			t.Errorf("%s %s: %d %s, want 400 naming %q", c.category, c.body, status, answer, c.reason)
		}
		stored, err := database.GetOrgSetting("org-a", "policy_"+c.category)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stored, strings.Trim(good[c.category], "{}")[:10]) {
			t.Errorf("%s: after a refused save the stored group is %s, want the earlier one", c.category, stored)
		}
	}
	// An empty group clears it, as the page's "Not set" needs
	if status, answer := asConsole(t, srv, http.MethodPut, "/api/org/org-a/policy/update", `{}`); status != http.StatusOK {
		t.Errorf("empty group: %d %s, want 200", status, answer)
	}
}

// TestPolicyFieldsAreTheCheckedSettings: the field list the console's
// resolved policies answer with is the list the check takes, per group in
// the order of the categories and sorted within a group.
func TestPolicyFieldsAreTheCheckedSettings(t *testing.T) {
	want := []PolicyGroupFields{
		{"connection", []string{"idle_lock_timeout", "max_session_duration", "mode", "wallpaper"}},
		{"features", []string{"allow_audio", "allow_clipboard", "allow_file_transfer", "allow_recording"}},
		{"security", []string{"allow_uninstall", "allowed_operators", "password_max_age_days", "password_min_length", "password_require_special", "require_2fa"}},
		{"network", []string{"allowed_relay_servers", "block_direct_p2p", "ip_allowlist"}},
		{"update", []string{"auto_update", "channel"}},
	}
	got := PolicyFields()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("policy fields:\n got  %v\n want %v", got, want)
	}
	got[0].Fields[0] = "changed"
	if PolicyFields()[0].Fields[0] != "idle_lock_timeout" {
		t.Error("the list handed out is shared")
	}
}
