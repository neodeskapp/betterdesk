package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestDisabledAccountIsRefusedEverywhere: once an account is disabled its
// sign-in token, its partial token waiting for the second factor and its
// desktop client session stop working, and its password, the client
// sign-in and a single sign-on are refused with a clear answer; enabled
// again, it signs in as before (L107).
func TestDisabledAccountIsRefusedEverywhere(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("disabled-account-test-secret", time.Hour))
	user := &db.User{Username: "dora", PasswordHash: mustHash(t, "dora-pass-1"), Role: auth.RoleOperator}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	login := func() (int, string) {
		return listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"dora","password":"dora-pass-1"}`, nil)
	}
	status, body := login()
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.Token == "" {
		t.Fatalf("login: %d %s", status, body)
	}
	patch := func(token string) int {
		status, _ := listenerCall(t, srv, http.MethodPatch, "/api/peers/PEERAAA1", `{"note":"x"}`, bearer(token))
		return status
	}
	if got := patch(out.Token); got != http.StatusOK {
		t.Fatalf("token before disabling: %d", got)
	}
	session, err := srv.issueClientSession(user, "DEVDORA1", "uuid-dora", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := srv.authenticateClientSession(session); !ok {
		t.Fatal("client session before disabling")
	}

	if err := database.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := patch(out.Token); got != http.StatusUnauthorized {
		t.Errorf("token of a disabled account: %d, want 401", got)
	}
	if _, _, ok := srv.authenticateClientSession(session); ok {
		t.Error("the client session of a disabled account still works")
	}
	status, body = login()
	if status != http.StatusForbidden || body != `{"code":"account_disabled","error":"This account is disabled"}` {
		t.Errorf("sign-in of a disabled account: %d %s", status, body)
	}
	status, body = listenerCall(t, srv, http.MethodPost, "/api/login",
		`{"username":"dora","password":"dora-pass-1","id":"DEVDORA1","uuid":"uuid-dora"}`, nil)
	if status != http.StatusForbidden || body != `{"code":"account_disabled","error":"This account is disabled"}` {
		t.Errorf("client sign-in of a disabled account: %d %s", status, body)
	}
	status, body = listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"dora","password":"wrong-pass-1"}`, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("wrong password of a disabled account: %d %s, want the plain 401", status, body)
	}
	if got, code := srv.ensureOIDCUser(&auth.OIDCResult{Username: "dora"}, &auth.OIDCConfig{AllowSignup: true}); got != nil || code != "username_collision" {
		t.Errorf("single sign-on as the local account: %+v %q", got, code)
	}

	if err := database.SetUserDisabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	if status, body := login(); status != http.StatusOK {
		t.Errorf("sign-in after enabling: %d %s", status, body)
	}
}

// TestDisabledAccountWaitingForItsSecondFactor: an account disabled
// between the password and the code gets no token for the code (L107).
func TestDisabledAccountWaitingForItsSecondFactor(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("disabled-2fa-test-secret", time.Hour))
	secret := auth.GenerateTOTPSecret()
	user := &db.User{Username: "tfa-dora", PasswordHash: mustHash(t, "dora-pass-1"), Role: auth.RoleOperator, TOTPSecret: secret, TOTPEnabled: true}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/login", `{"username":"tfa-dora","password":"dora-pass-1"}`, nil)
	var out struct {
		PartialToken string `json:"partial_token"`
	}
	if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil || out.PartialToken == "" {
		t.Fatalf("login: %d %s", status, body)
	}
	if err := database.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ComputeTOTP(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	status, body = listenerCall(t, srv, http.MethodPost, "/api/auth/login/2fa",
		`{"partial_token":"`+out.PartialToken+`","code":"`+code+`"}`, nil)
	if status != http.StatusForbidden || body != `{"code":"account_disabled","error":"This account is disabled"}` {
		t.Errorf("second factor of a disabled account: %d %s", status, body)
	}
}

// TestDisabledSingleSignOnAccountIsNotMadeAgain: a single sign-on of a
// disabled account is refused with its own code and makes no account
// (L107).
func TestDisabledSingleSignOnAccountIsNotMadeAgain(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	user := &db.User{Username: "sso-dora", PasswordHash: "x", Role: auth.RoleViewer, AuthProvider: db.AuthProviderOIDC}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	if err := database.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	got, code := srv.ensureOIDCUser(&auth.OIDCResult{Username: "sso-dora", Role: auth.RoleAdmin, RoleFromGroup: true}, &auth.OIDCConfig{AllowSignup: true})
	if got != nil || code != "account_disabled" {
		t.Fatalf("single sign-on of a disabled account: %+v %q", got, code)
	}
	after, err := database.GetUser("sso-dora")
	if err != nil || after == nil || !after.Disabled || after.Role != auth.RoleViewer {
		t.Errorf("the account afterwards: %+v, %v", after, err)
	}
}

// TestDisabledAccountAtTheClientsLaterSteps: the desktop client's code step
// and its single sign-on poll give a disabled account no session (L107).
func TestDisabledAccountAtTheClientsLaterSteps(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("disabled-client-test-secret", time.Hour))
	secret := auth.GenerateTOTPSecret()
	user := &db.User{Username: "client-dora", PasswordHash: mustHash(t, "dora-pass-1"), Role: auth.RoleOperator, TOTPSecret: secret, TOTPEnabled: true}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	status, body := listenerCall(t, srv, http.MethodPost, "/api/login",
		`{"username":"client-dora","password":"dora-pass-1","id":"DEVDORA2","uuid":"uuid-dora-2"}`, nil)
	var step struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(body), &step); status != http.StatusOK || err != nil || step.Secret == "" {
		t.Fatalf("password step: %d %s", status, body)
	}
	if err := database.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ComputeTOTP(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	status, body = listenerCall(t, srv, http.MethodPost, "/api/login",
		`{"verificationCode":"`+code+`","secret":"`+step.Secret+`","id":"DEVDORA2","uuid":"uuid-dora-2"}`, nil)
	if status != http.StatusForbidden || body != `{"code":"account_disabled","error":"This account is disabled"}` {
		t.Errorf("code step of a disabled account: %d %s", status, body)
	}

	p := auth.NewOIDCProvider(&auth.OIDCConfig{Enabled: true, ClientID: "c", AuthorizationURL: "https://idp.example.com/a", RedirectURL: "http://localhost/cb"})
	t.Cleanup(p.Close)
	_, poll, err := p.BuildClientAuthURL("DEVDORA2", "uuid-dora-2", auth.ClientDeviceInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.CompleteClientPending(poll, user.ID, user.Username, user.Role) {
		t.Fatal("complete the poll")
	}
	srv.oidcProvider = p
	status, body = listenerCall(t, srv, http.MethodGet, "/api/oidc/auth-query?code="+poll+"&id=DEVDORA2&uuid=uuid-dora-2", "", nil)
	if status != http.StatusForbidden || body != `{"code":"account_disabled","error":"This account is disabled"}` {
		t.Errorf("single sign-on poll of a disabled account: %d %s", status, body)
	}
}
