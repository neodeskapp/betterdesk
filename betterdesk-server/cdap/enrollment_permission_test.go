package cdap

import (
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// dbPermissions answers like the API's permission check: the role
// overrides stored in the database, else the built-in map.
type dbPermissions struct{ db db.Database }

func (p dbPermissions) RoleHasPermission(role, permission string) (bool, error) {
	overrides, err := p.db.ListRolePermissions(role)
	if err != nil {
		return false, err
	}
	for _, o := range overrides {
		if o != nil && o.Permission == permission {
			return o.Granted, nil
		}
	}
	return auth.RoleHasPermission(role, permission), nil
}

// TestPasswordAndKeyRegistrationNeedEnrollmentApprove: registering on the
// CDAP port with an API key or an account's password needs
// enrollment.approve, as the role overrides decide it; a device token
// registers its own device as before (RV157, A95).
func TestPasswordAndKeyRegistrationNeedEnrollmentApprove(t *testing.T) {
	w := newCDAPWorld(t)
	createAPIKey(t, w.db, "apikey-operator-0123456789abcdef", "op", auth.RoleOperator, "")
	createAPIKey(t, w.db, "apikey-global-0123456789abcdef", "ga", auth.RoleGlobalAdmin, "")
	hash, err := auth.HashPassword("right-anna")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.db.CreateUser(&db.User{Username: "anna", PasswordHash: hash, Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	signIn := func(payload AuthPayload) Message {
		t.Helper()
		c := w.dial(t)
		defer func() {
			c.conn.CloseNow()
			w.settle(t, 0)
		}()
		c.send("auth", payload)
		return c.read()
	}
	refused := func(what string, payload AuthPayload) {
		t.Helper()
		m := signIn(payload)
		if m.Type != "error" || !strings.Contains(string(m.Payload), "enrollment.approve") {
			t.Errorf("%s: %s %s, want an error naming enrollment.approve", what, m.Type, m.Payload)
		}
	}

	refused("an operator's API key", AuthPayload{Method: "api_key", Key: "apikey-operator-0123456789abcdef", DeviceID: "plc-1"})
	refused("an operator's password", AuthPayload{Method: "user_password", Username: "anna", Password: "right-anna", DeviceID: "plc-1"})
	if p, _ := w.db.GetPeer("plc-1"); p != nil {
		t.Fatalf("a refused registration left the device %+v", p)
	}

	c := w.connect(t, "apikey-global-0123456789abcdef", "plc-2")
	c.drain()
	c.conn.Close(websocket.StatusNormalClosure, "")
	w.settle(t, 0)

	if err := w.db.SetRolePermission(auth.RoleOperator, auth.PermEnrollmentApprove, true); err != nil {
		t.Fatal(err)
	}
	if m := signIn(AuthPayload{Method: "api_key", Key: "apikey-operator-0123456789abcdef", DeviceID: "plc-3"}); m.Type != "auth_result" {
		t.Errorf("an operator's API key with enrollment.approve granted: %s %s, want auth_result", m.Type, m.Payload)
	}
	if err := w.db.SetRolePermission(auth.RoleGlobalAdmin, auth.PermEnrollmentApprove, false); err != nil {
		t.Fatal(err)
	}
	refused("a global admin's API key with enrollment.approve taken away", AuthPayload{Method: "api_key", Key: "apikey-global-0123456789abcdef", DeviceID: "plc-4"})

	// Without a permission check attached nothing registers with a key.
	w.g.SetPermissionChecker(nil)
	refused("an API key without a permission check", AuthPayload{Method: "api_key", Key: "apikey-operator-0123456789abcdef", DeviceID: "plc-5"})
}
