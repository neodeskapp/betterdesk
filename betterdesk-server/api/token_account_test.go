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

// nextSecond waits until the clock's second changes, so a credentials
// change comes strictly after a token issued before the call (both count
// in whole seconds).
func nextSecond() {
	time.Sleep(time.Until(time.Unix(time.Now().Unix()+1, 0)))
}

// TestPasswordChangeEndsTheAccountsTokens: a sign-in token issued before
// the account's password changed is refused, also a partial token still
// waiting for its second factor; a token issued after the change works
// (RV181).
func TestPasswordChangeEndsTheAccountsTokens(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("password-change-test-secret", time.Hour))
	user := &db.User{Username: "pwchange", PasswordHash: mustHash(t, "first-pass-1"), Role: auth.RoleOperator}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	login := func(password string) string {
		t.Helper()
		status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"pwchange","password":"`+password+`"}`, nil)
		var out struct {
			Token        string `json:"token"`
			PartialToken string `json:"partial_token"`
		}
		if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.Token+out.PartialToken == "" {
			t.Fatalf("login: %d %s", status, body)
		}
		return out.Token + out.PartialToken
	}
	patch := func(token string) int {
		t.Helper()
		status, _ := listenerCall(t, srv, http.MethodPatch, "/api/peers/PEERAAA1", `{"note":"x"}`, bearer(token))
		return status
	}
	setPassword := func(password string) {
		t.Helper()
		user.PasswordHash = mustHash(t, password)
		if err := database.UpdateUser(user); err != nil {
			t.Fatal(err)
		}
	}

	old := login("first-pass-1")
	if got := patch(old); got != http.StatusOK {
		t.Fatalf("token before the change: %d, want 200", got)
	}
	nextSecond()
	setPassword("second-pass-2")
	if got := patch(old); got != http.StatusUnauthorized {
		t.Errorf("token issued before the password change: %d, want 401", got)
	}
	if got := patch(login("second-pass-2")); got != http.StatusOK {
		t.Errorf("token issued after the password change: %d, want 200", got)
	}

	secret := auth.GenerateTOTPSecret()
	user.TOTPSecret, user.TOTPEnabled = secret, true
	if err := database.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	nextSecond()
	partial := login("second-pass-2")
	nextSecond()
	setPassword("third-pass-3")
	code, err := auth.ComputeTOTP(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login/2fa", `{"partial_token":"`+partial+`","code":"`+code+`"}`, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("second factor of a sign-in begun before the password change: %d %s, want 401", status, body)
	}
}

// TestPasswordChangeEndsDesktopClientSessions: a desktop client's session
// opened before the account's password changed is refused (RV181).
func TestPasswordChangeEndsDesktopClientSessions(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.EnsureClientSessionsSchema(); err != nil {
		t.Fatal(err)
	}
	user := &db.User{Username: "deskuser", PasswordHash: mustHash(t, "first-pass-1"), Role: auth.RoleOperator}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	token, err := srv.issueClientSession(user, "DESK0001", "desk-uuid-1", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := srv.authenticateClientSession(token); !ok {
		t.Fatal("session before the change refused")
	}
	nextSecond()
	user.PasswordHash = mustHash(t, "second-pass-2")
	if err := database.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := srv.authenticateClientSession(token); ok {
		t.Error("session opened before the password change still accepted")
	}
	fresh, err := srv.issueClientSession(user, "DESK0001", "desk-uuid-1", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := srv.authenticateClientSession(fresh); !ok {
		t.Error("session opened after the password change refused")
	}
}
