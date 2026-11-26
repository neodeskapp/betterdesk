package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// A disabled Super admin can't sign in, so it does not keep the server
// administrable: the last Super admin that can sign in is not deleted or
// demoted while the other one is disabled, and is once it can sign in
// again.
func TestLastActiveSuperAdminStays(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("last-super-admin-test-secret", time.Hour))
	ada := &db.User{Username: "ada", PasswordHash: mustHash(t, "ada-pass-123"), Role: auth.RoleSuperAdmin}
	bob := &db.User{Username: "bob", PasswordHash: mustHash(t, "bob-pass-123"), Role: auth.RoleSuperAdmin}
	for _, u := range []*db.User{ada, bob} {
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetUserDisabled(bob.ID, true); err != nil {
		t.Fatal(err)
	}
	status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"ada","password":"ada-pass-123"}`, nil)
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.Token == "" {
		t.Fatalf("login: %d %s", status, body)
	}
	self := fmt.Sprintf("/api/users/%d", ada.ID)
	if status, body := listenerCall(t, srv, http.MethodDelete, self, "", bearer(out.Token)); status != http.StatusConflict {
		t.Errorf("deleting the last Super admin that can sign in: %d %s", status, body)
	}
	if u, _ := database.GetUserByID(ada.ID); u == nil {
		t.Fatal("the last Super admin was deleted")
	}

	if err := database.SetUserDisabled(bob.ID, false); err != nil {
		t.Fatal(err)
	}
	if status, body := listenerCall(t, srv, http.MethodPut, fmt.Sprintf("/api/users/%d", bob.ID), `{"role":"operator"}`, bearer(out.Token)); status != http.StatusOK {
		t.Errorf("demoting one of two Super admins: %d %s", status, body)
	}
}
