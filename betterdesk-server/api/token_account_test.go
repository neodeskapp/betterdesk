package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// TestServerTokenFollowsTheAccount: a server sign-in token acts with the
// role the account has now, and stops working when the account is renamed
// or deleted (RV181).
func TestServerTokenFollowsTheAccount(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	jwt := auth.NewJWTManager("server-token-test-secret", time.Hour)
	srv.SetJWTManager(jwt)
	user := &db.User{Username: "opsy", PasswordHash: mustHash(t, "opsy-pass-1"), Role: auth.RoleOperator}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"opsy","password":"opsy-pass-1"}`, nil)
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.Token == "" {
		t.Fatalf("login: %d %s", status, body)
	}
	patch := func() int {
		status, _ := listenerCall(t, srv, http.MethodPatch, "/api/peers/PEERAAA1", `{"note":"x"}`, bearer(out.Token))
		return status
	}
	if got := patch(); got != http.StatusOK {
		t.Fatalf("operator token before the change: %d, want 200", got)
	}

	user.Role = auth.RoleViewer
	if err := database.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	if got := patch(); got != http.StatusForbidden {
		t.Errorf("token of an account demoted to viewer: %d, want 403", got)
	}

	if err := database.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if got := patch(); got != http.StatusUnauthorized {
		t.Errorf("token of a deleted account: %d, want 401", got)
	}

	// A token whose claim carries a role the account does not have gets
	// the stored role.
	viewer := &db.User{Username: "watcher", PasswordHash: "x", Role: auth.RoleViewer}
	if err := database.CreateUser(viewer); err != nil {
		t.Fatal(err)
	}
	forged, err := jwt.Generate("watcher", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/keys", "", bearer(forged)); status != http.StatusForbidden {
		t.Errorf("admin claim of a viewer account: %d %s, want 403", status, body)
	}
}
