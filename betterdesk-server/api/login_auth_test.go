package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// mockLDAPProvider implements ldapAuthProvider for unit tests (Issue #218).
type mockLDAPProvider struct {
	enabled bool
	fn      func(username, password string) (*auth.LDAPResult, error)
}

func (m *mockLDAPProvider) IsEnabled() bool { return m.enabled }

func (m *mockLDAPProvider) Authenticate(username, password string) (*auth.LDAPResult, error) {
	if m.fn != nil {
		return m.fn(username, password)
	}
	return &auth.LDAPResult{Authenticated: false}, nil
}

func (m *mockLDAPProvider) UpdateConfig(*auth.LDAPConfig) {}

func (m *mockLDAPProvider) TestConnection() error { return nil }

func (m *mockLDAPProvider) Config() auth.LDAPConfig {
	return auth.LDAPConfig{Enabled: m.enabled}
}

func createLDAPTestUser(t *testing.T, database db.Database, username, role string) {
	t.Helper()
	randomSecret, err := auth.GenerateRandomString(32)
	if err != nil {
		t.Fatal(err)
	}
	unusable, err := auth.HashPassword(randomSecret)
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{
		Username:     username,
		PasswordHash: unusable,
		Role:         role,
		AuthProvider: db.AuthProviderLDAP,
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticatePasswordLoginLDAPUserSuccess(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createLDAPTestUser(t, database, "ad.user", auth.RoleOperator)

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			if username == "ad.user" && password == "ad-pass" {
				return &auth.LDAPResult{Authenticated: true, Role: auth.RoleOperator}, nil
			}
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	result := srv.authenticatePasswordLogin("ad.user", "ad-pass", "127.0.0.1")
	if result.Status != passwordLoginOK {
		t.Fatalf("status = %v, want OK", result.Status)
	}
	if result.User == nil || result.User.Username != "ad.user" {
		t.Fatalf("user = %#v", result.User)
	}
	if result.AuthMethod != "ldap" {
		t.Fatalf("AuthMethod = %q, want ldap", result.AuthMethod)
	}
}

func TestAuthenticatePasswordLoginLDAPUserWrongPassword(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createLDAPTestUser(t, database, "ad.user", auth.RoleOperator)

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	result := srv.authenticatePasswordLogin("ad.user", "wrong", "127.0.0.1")
	if result.Status != passwordLoginInvalid {
		t.Fatalf("status = %v, want invalid", result.Status)
	}
}

func TestAuthenticatePasswordLoginLDAPAutoProvision(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			if username == "new.ad" && password == "ad-pass" {
				return &auth.LDAPResult{Authenticated: true, Role: auth.RoleViewer}, nil
			}
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	result := srv.authenticatePasswordLogin("new.ad", "ad-pass", "127.0.0.1")
	if result.Status != passwordLoginOK {
		t.Fatalf("status = %v, want OK", result.Status)
	}
	if result.User == nil || result.User.AuthProvider != db.AuthProviderLDAP {
		t.Fatalf("user provider = %q, want ldap", result.User.AuthProvider)
	}

	stored, err := database.GetUser("new.ad")
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil {
		t.Fatal("expected auto-provisioned user in database")
	}
}

func TestHandleClientLoginLDAPReturnsAccessToken(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createLDAPTestUser(t, database, "ad.user", auth.RoleViewer)

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			if username == "ad.user" && password == "ad-pass" {
				return &auth.LDAPResult{Authenticated: true, Role: auth.RoleViewer}, nil
			}
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	rec, resp := postClientLogin(t, srv, map[string]any{
		"username": "ad.user",
		"password": "ad-pass",
		"type":     "account",
		"id":       "test",
		"uuid":     "test",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp["type"] != "access_token" {
		t.Fatalf("type = %#v, want access_token", resp["type"])
	}
	if resp["access_token"] == "" {
		t.Fatal("expected access_token")
	}
}

func TestHandleClientLoginLDAPWrongPassword(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createLDAPTestUser(t, database, "ad.user", auth.RoleViewer)

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	rec, resp := postClientLogin(t, srv, map[string]any{
		"username": "ad.user",
		"password": "wrong",
		"type":     "account",
		"id":       "test",
		"uuid":     "test",
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if resp["error"] != "Invalid credentials" {
		t.Fatalf("error = %#v, want Invalid credentials", resp["error"])
	}
}

func TestHandleClientLoginLDAPAutoProvision(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			if username == "new.ad" && password == "ad-pass" {
				return &auth.LDAPResult{Authenticated: true, Role: auth.RoleViewer}, nil
			}
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	rec, resp := postClientLogin(t, srv, map[string]any{
		"username": "new.ad",
		"password": "ad-pass",
		"type":     "account",
		"id":       "test",
		"uuid":     "test",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp["type"] != "access_token" {
		t.Fatalf("type = %#v, want access_token", resp["type"])
	}
}

func TestHandleClientLoginLDAPWithTOTPReturnsChallenge(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	randomSecret, err := auth.GenerateRandomString(32)
	if err != nil {
		t.Fatal(err)
	}
	unusable, err := auth.HashPassword(randomSecret)
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{
		Username:     "ad.totp",
		PasswordHash: unusable,
		Role:         auth.RoleViewer,
		AuthProvider: db.AuthProviderLDAP,
		TOTPSecret:   auth.GenerateTOTPSecret(),
		TOTPEnabled:  true,
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			if username == "ad.totp" && password == "ad-pass" {
				return &auth.LDAPResult{Authenticated: true, Role: auth.RoleViewer}, nil
			}
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	rec, resp := postClientLogin(t, srv, map[string]any{
		"username": "ad.totp",
		"password": "ad-pass",
		"type":     "account",
		"id":       "test",
		"uuid":     "test",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp["type"] != "email_check" {
		t.Fatalf("type = %#v, want email_check", resp["type"])
	}
	if resp["secret"] == "" {
		t.Fatal("expected TFA session secret")
	}
}

func TestHandleClientLoginLocalUserStillUsesLocalPassword(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createClientLoginTestUser(t, database, "local.user", "local-pass", false)

	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			// LDAP would accept this password — local account must not use it.
			if username == "local.user" && password == "ldap-only-pass" {
				return &auth.LDAPResult{Authenticated: true, Role: auth.RoleAdmin}, nil
			}
			return &auth.LDAPResult{Authenticated: false}, nil
		},
	}

	rec, resp := postClientLogin(t, srv, map[string]any{
		"username": "local.user",
		"password": "ldap-only-pass",
		"type":     "account",
		"id":       "test",
		"uuid":     "test",
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if resp["error"] != "Invalid credentials" {
		t.Fatalf("error = %#v, want Invalid credentials", resp["error"])
	}

	rec, resp = postClientLogin(t, srv, map[string]any{
		"username": "local.user",
		"password": "local-pass",
		"type":     "account",
		"id":       "test",
		"uuid":     "test",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("local password: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp["type"] != "access_token" {
		t.Fatalf("type = %#v, want access_token", resp["type"])
	}
}

// TestLDAPSignInUsesTheDirectoryName: a directory sign-in acts as the
// account the directory entry names, so another spelling of the name signs
// in as that account and creates no second one; a local account of that
// name is refused (RV117).
func TestLDAPSignInUsesTheDirectoryName(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createLDAPTestUser(t, database, "alice", auth.RoleViewer)
	if err := database.CreateUser(&db.User{Username: "carol", PasswordHash: "hash", Role: auth.RoleAdmin, AuthProvider: db.AuthProviderLocal}); err != nil {
		t.Fatal(err)
	}
	srv := newClientLoginTestServer(database)
	srv.ldapProvider = &mockLDAPProvider{
		enabled: true,
		fn: func(username, password string) (*auth.LDAPResult, error) {
			// The directory matches names without regard to case.
			return &auth.LDAPResult{Authenticated: true, Username: strings.ToLower(strings.TrimSpace(username)), Role: auth.RoleViewer}, nil
		},
	}

	result := srv.authenticatePasswordLogin("Alice", "dir-pass", "127.0.0.1")
	if result.Status != passwordLoginOK || result.User == nil || result.User.Username != "alice" {
		t.Fatalf("sign-in as Alice: status %v user %+v, want the account alice", result.Status, result.User)
	}
	if u, _ := database.GetUser("Alice"); u != nil {
		t.Errorf("a second account Alice was created: %+v", u)
	}

	if result := srv.authenticatePasswordLogin("Carol", "dir-pass", "127.0.0.1"); result.Status != passwordLoginInvalid {
		t.Errorf("sign-in as Carol for the local account carol: status %v, want invalid", result.Status)
	}
	if u, _ := database.GetUser("Carol"); u != nil {
		t.Errorf("an account Carol was created: %+v", u)
	}
	if u, _ := database.GetUser("carol"); u == nil || u.AuthProvider != db.AuthProviderLocal || u.Role != auth.RoleAdmin {
		t.Errorf("the local account carol after the refused sign-in: %+v", u)
	}
}
