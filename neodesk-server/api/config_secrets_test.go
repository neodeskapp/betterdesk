package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/audit"
	"github.com/unitronix/betterdesk-server/auth"
)

const testServerAdminKey = "srvadmin-api-key-12345"

// TestConfigRoutesKeepSecrets: the generic config routes neither answer
// nor write a key that holds a secret or a credential, and the audit entry
// of a write names the key without its value (RV169).
func TestConfigRoutesKeepSecrets(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	addTestAPIKey(t, database, testServerAdminKey, "srvadmin-key", auth.RoleServerAdmin)
	key := map[string]string{"X-API-Key": testServerAdminKey}
	secrets := []string{
		"jwt_secret", "api_key", "telemetry_x25519_private", "ldap.bind_password",
		"oidc.client_secret", "device_totp_secret_PEERAAA1", "device_public_key_PEERAAA1",
		"pairing_code_abc", "mesh_share_abc", "custom.private_pem", "smtp.Password",
	}
	for _, k := range secrets {
		if err := database.SetConfig(k, "stored-"+k); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range secrets {
		status, body := listenerCall(t, srv, http.MethodGet, "/api/config/"+k, "", key)
		if status != http.StatusForbidden || strings.Contains(body, "stored-") {
			t.Errorf("GET %s: %d %s, want 403 without the value", k, status, body)
		}
		status, body = listenerCall(t, srv, http.MethodPut, "/api/config/"+k, `{"value":"written"}`, key)
		if status != http.StatusForbidden {
			t.Errorf("PUT %s: %d %s, want 403", k, status, body)
		}
		if got, _ := database.GetConfig(k); got != "stored-"+k {
			t.Errorf("PUT %s changed the value to %q", k, got)
		}
	}
	// The console's own keys go through, for the console as for a key.
	if status, body := asConsole(t, srv, http.MethodPut, "/api/config/cdap_enabled", `{"value":"true"}`); status != http.StatusOK {
		t.Errorf("console PUT cdap_enabled: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/config/jwt_secret", ""); status != http.StatusForbidden {
		t.Errorf("console GET jwt_secret: %d %s, want 403", status, body)
	}

	if status, body := listenerCall(t, srv, http.MethodPut, "/api/config/sample_key", `{"value":"the-written-value"}`, key); status != http.StatusOK {
		t.Fatalf("PUT sample_key: %d %s", status, body)
	}
	events := srv.auditLog.RecentByAction(audit.ActionConfigChanged, 10)
	if len(events) == 0 {
		t.Fatal("no audit entry for the config write")
	}
	for _, e := range events {
		if strings.Contains(fmt.Sprint(e), "the-written-value") {
			t.Errorf("the audit entry holds the value: %+v", e)
		}
	}
}

// TestAPIKeyRoleNeedsAssignRight: an API key is created only with a role
// the caller could give an account (RV169).
func TestAPIKeyRoleNeedsAssignRight(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	addTestAPIKey(t, database, testServerAdminKey, "srvadmin-key", auth.RoleServerAdmin)
	for _, role := range []string{auth.RoleAdmin, auth.RoleSuperAdmin, auth.RoleViewer} {
		status, body := listenerCall(t, srv, http.MethodPost, "/api/keys", `{"name":"k-`+role+`","role":"`+role+`"}`,
			map[string]string{"X-API-Key": testServerAdminKey})
		if status != http.StatusForbidden || strings.Contains(body, `"key"`) {
			t.Errorf("server_admin creates a %s key: %d %s, want 403", role, status, body)
		}
	}
	if status, body := asConsole(t, srv, http.MethodPost, "/api/keys", `{"name":"k-console","role":"super_admin"}`); status != http.StatusCreated {
		t.Errorf("console creates a super_admin key: %d %s, want 201", status, body)
	}
}

// TestServerAdminListsAndDeletesKeysButCreatesNone: server_admin holds
// server.keys but may give no role, so it lists and deletes API keys and
// creates none: POST /api/keys answers it a clear 403 before the body is
// read (L51).
func TestServerAdminListsAndDeletesKeysButCreatesNone(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	addTestAPIKey(t, database, testServerAdminKey, "srvadmin-key", auth.RoleServerAdmin)
	serverAdmin := map[string]string{"X-API-Key": testServerAdminKey}
	const refusal = `{"error":"This role can list and delete API keys but not create them"}`
	for _, body := range []string{`{}`, `not json`, `{"name":"k-viewer","role":"viewer"}`} {
		if status, answer := listenerCall(t, srv, http.MethodPost, "/api/keys", body, serverAdmin); status != http.StatusForbidden || answer != refusal {
			t.Errorf("server_admin creates a key with %s: %d %s, want 403 %s", body, status, answer, refusal)
		}
	}
	status, body := asConsole(t, srv, http.MethodPost, "/api/keys", `{"name":"k-console","role":"viewer"}`)
	if status != http.StatusCreated {
		t.Fatalf("console creates a viewer key: %d %s, want 201", status, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == 0 {
		t.Fatalf("the created key: %s", body)
	}
	if status, answer := listenerCall(t, srv, http.MethodGet, "/api/keys", "", serverAdmin); status != http.StatusOK || !strings.Contains(answer, `"name":"k-console"`) {
		t.Errorf("server_admin lists the keys: %d %s", status, answer)
	}
	if status, answer := listenerCall(t, srv, http.MethodDelete, fmt.Sprintf("/api/keys/%d", created.ID), "", serverAdmin); status != http.StatusOK {
		t.Errorf("server_admin deletes a key: %d %s, want 200", status, answer)
	}
}
