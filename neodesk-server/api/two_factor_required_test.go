package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestMandatoryTwoFactorAtTheAPI: with two-factor sign-in mandatory, the
// API's sign-in and the desktop client's refuse a local account that has
// none yet, with a clear answer and no token, since they have no step for
// the set-up. An account that has it and a directory account are not
// refused for it (A153).
func TestMandatoryTwoFactorAtTheAPI(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("required-2fa-test-secret", time.Hour))
	if err := database.CreateUser(&db.User{Username: "sam", PasswordHash: mustHash(t, "sam-pass-1"), Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateUser(&db.User{Username: "tia", PasswordHash: mustHash(t, "tia-pass-1"), Role: auth.RoleOperator,
		TOTPSecret: auth.GenerateTOTPSecret(), TOTPEnabled: true}); err != nil {
		t.Fatal(err)
	}
	const due = `{"code":"totp_setup_required","error":"Two-factor sign-in is required for this account. Sign in to the web console once to set it up."}`
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"sam","password":"sam-pass-1"}`, nil); status != http.StatusOK {
		t.Fatalf("switch off: %d %s", status, body)
	}
	if err := database.SetConfig(configRequireTwoFactor, "true"); err != nil {
		t.Fatal(err)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"sam","password":"sam-pass-1"}`, nil); status != http.StatusForbidden || body != due {
		t.Errorf("API sign-in: %d %s", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/login", `{"username":"sam","password":"sam-pass-1","id":"DEVSAM1","uuid":"uuid-sam"}`, nil); status != http.StatusForbidden || body != due {
		t.Errorf("client sign-in: %d %s", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"tia","password":"tia-pass-1"}`, nil); status != http.StatusOK {
		t.Errorf("account with two-factor sign-in: %d %s", status, body)
	}
	if srv.twoFactorSetupDue(&db.User{Username: "dir", AuthProvider: db.AuthProviderLDAP}) {
		t.Error("a directory account is held to the switch")
	}
}

// TestMandatoryTwoFactorLeavesProAccounts: a Pro license account signs in
// only through this API and the desktop client; the web console, where
// two-factor sign-in is set up, refuses it. The switch leaves it alone, as
// it leaves directory and single sign-on accounts to their provider, so it
// is not locked out (BUGS L222).
func TestMandatoryTwoFactorLeavesProAccounts(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("required-2fa-pro-test-secret", time.Hour))
	if err := database.CreateUser(&db.User{Username: "lic", PasswordHash: mustHash(t, "lic-pass-1"), Role: auth.RolePro}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig(configRequireTwoFactor, "true"); err != nil {
		t.Fatal(err)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"lic","password":"lic-pass-1"}`, nil); status != http.StatusOK {
		t.Errorf("API sign-in of the Pro account: %d %s", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/login", `{"username":"lic","password":"lic-pass-1","id":"DEVLIC1","uuid":"uuid-lic"}`, nil); status != http.StatusOK {
		t.Errorf("client sign-in of the Pro account: %d %s", status, body)
	}
}
