package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// browserSignIns reads GET /api/login-options: the password entry first,
// then the common-oidc list the stock client shows as buttons.
func browserSignIns(t *testing.T, s *Server) []map[string]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/login-options", nil)
	rec := httptest.NewRecorder()
	s.handleClientLoginOptions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var opts []string
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 || opts[0] != "" || !strings.HasPrefix(opts[1], "common-oidc/") {
		t.Fatalf("opts = %#v", opts)
	}
	var list []map[string]string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(opts[1], "common-oidc/")), &list); err != nil {
		t.Fatalf("the common-oidc list %q: %v", opts[1], err)
	}
	return list
}

// The console sign-in is always offered first, with the NeoDesk mark; the
// outside identity provider follows in the same list, because the stock
// client reads only the first common-oidc entry.
func TestHandleClientLoginOptionsOIDC(t *testing.T) {
	s := &Server{
		oidcProvider: auth.NewOIDCProvider(&auth.OIDCConfig{
			Enabled:     true,
			ClientID:    "cid",
			DisplayName: "Keycloak",
		}),
	}
	list := browserSignIns(t, s)
	if len(list) != 2 || list[0]["name"] != "NeoDesk" || list[0]["icon"] != neodeskMarkSVG || list[1]["name"] != "Keycloak" || list[1]["icon"] != "" {
		t.Fatalf("list = %#v", list)
	}
	if !strings.HasPrefix(neodeskMarkSVG, "<svg ") || !strings.Contains(neodeskMarkSVG, `fill="#58a6ff"`) {
		t.Errorf("the icon is not the NeoDesk mark: %s", neodeskMarkSVG)
	}

	// An identity provider called NeoDesk would take the console's op
	s.oidcProvider = auth.NewOIDCProvider(&auth.OIDCConfig{Enabled: true, ClientID: "cid", DisplayName: "NeoDesk"})
	if list := browserSignIns(t, s); len(list) != 2 || list[1]["name"] != "NeoDesk SSO" {
		t.Fatalf("list with a provider called NeoDesk = %#v", list)
	}
}

func TestHandleClientLoginOptionsWithoutIdentityProvider(t *testing.T) {
	list := browserSignIns(t, &Server{})
	if len(list) != 1 || list[0]["name"] != "NeoDesk" {
		t.Fatalf("list = %#v", list)
	}
}

func TestHandleClientOIDCAuthRequiresEnabled(t *testing.T) {
	s := &Server{oidcProvider: auth.NewOIDCProvider(&auth.OIDCConfig{Enabled: false})}
	req := httptest.NewRequest(http.MethodPost, "/api/oidc/auth", strings.NewReader(`{"id":"1","uuid":"u"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleClientOIDCAuth(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandleClientOIDCAuthQueryWaiting(t *testing.T) {
	p := auth.NewOIDCProvider(&auth.OIDCConfig{
		Enabled: true, ClientID: "c",
		AuthorizationURL: "https://idp.example.com/a",
		RedirectURL:      "http://localhost/cb",
	})
	_, code, err := p.BuildClientAuthURL("dev", "uuid", auth.ClientDeviceInfo{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{oidcProvider: p}

	req := httptest.NewRequest(http.MethodGet, "/api/oidc/auth-query?code="+code+"&id=dev&uuid=uuid", nil)
	rec := httptest.NewRecorder()
	s.handleClientOIDCAuthQuery(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "No authed oidc is found" {
		t.Fatalf("body = %#v", body)
	}
}

func TestHandleClientOIDCAuthQueryDeviceMismatch(t *testing.T) {
	p := auth.NewOIDCProvider(&auth.OIDCConfig{
		Enabled: true, ClientID: "c",
		AuthorizationURL: "https://idp.example.com/a",
		RedirectURL:      "http://localhost/cb",
	})
	_, code, err := p.BuildClientAuthURL("dev", "uuid", auth.ClientDeviceInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.CompleteClientPending(code, 1, "alice", "viewer") {
		t.Fatal("complete failed")
	}
	s := &Server{oidcProvider: p}

	req := httptest.NewRequest(http.MethodGet, "/api/oidc/auth-query?code="+code+"&id=other&uuid=uuid", nil)
	rec := httptest.NewRecorder()
	s.handleClientOIDCAuthQuery(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandleClientOIDCAuthQueryOmittingDeviceRejected(t *testing.T) {
	p := auth.NewOIDCProvider(&auth.OIDCConfig{
		Enabled: true, ClientID: "c",
		AuthorizationURL: "https://idp.example.com/a",
		RedirectURL:      "http://localhost/cb",
	})
	_, code, err := p.BuildClientAuthURL("dev", "uuid", auth.ClientDeviceInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.CompleteClientPending(code, 1, "alice", "viewer") {
		t.Fatal("complete failed")
	}
	s := &Server{oidcProvider: p}

	req := httptest.NewRequest(http.MethodGet, "/api/oidc/auth-query?code="+code, nil)
	rec := httptest.NewRecorder()
	s.handleClientOIDCAuthQuery(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandleClientOIDCAuthQuerySuccessIncludesUserInfo(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	createClientLoginTestUser(t, database, "alice", "unused-password", false)

	user, err := database.GetUser("alice")
	if err != nil || user == nil {
		t.Fatalf("GetUser: %v", err)
	}

	p := auth.NewOIDCProvider(&auth.OIDCConfig{
		Enabled: true, ClientID: "c",
		AuthorizationURL: "https://idp.example.com/a",
		RedirectURL:      "http://localhost/cb",
	})
	_, code, err := p.BuildClientAuthURL("dev1", "uuid1", auth.ClientDeviceInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.CompleteClientPending(code, user.ID, user.Username, user.Role) {
		t.Fatal("complete failed")
	}

	s := newClientLoginTestServer(database)
	s.oidcProvider = p

	req := httptest.NewRequest(http.MethodGet, "/api/oidc/auth-query?code="+code+"&id=dev1&uuid=uuid1", nil)
	rec := httptest.NewRecorder()
	s.handleClientOIDCAuthQuery(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != "access_token" {
		t.Fatalf("type = %#v", body["type"])
	}
	token, _ := body["access_token"].(string)
	if token == "" {
		t.Fatal("expected access_token")
	}
	userObj, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("user = %#v", body["user"])
	}
	if _, ok := userObj["info"].(map[string]any); !ok {
		t.Fatalf("user.info missing or wrong type: %#v", userObj["info"])
	}
	if userObj["name"] != "alice" {
		t.Fatalf("user.name = %#v", userObj["name"])
	}
}

func TestRustdeskUserPayloadHasInfo(t *testing.T) {
	payload := rustdeskUserPayload("bob", auth.RoleAdmin)
	info, ok := payload["info"].(map[string]any)
	if !ok || info == nil {
		t.Fatalf("info = %#v", payload["info"])
	}
	if payload["is_admin"] != true {
		t.Fatalf("is_admin = %#v", payload["is_admin"])
	}
}

func TestEnsureOIDCUserPreservesRoleWhenOnlyDefaultApplies(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	hash, err := auth.HashPassword("unused")
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{
		Username:     "oidc-admin",
		PasswordHash: hash,
		Role:         auth.RoleAdmin,
		AuthProvider: db.AuthProviderOIDC,
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: database}
	got, code := s.ensureOIDCUser(&auth.OIDCResult{
		Username:      "oidc-admin",
		Role:          auth.RoleViewer,
		RoleFromGroup: false,
	}, &auth.OIDCConfig{AllowSignup: true, DefaultRole: auth.RoleViewer})
	if code != "" {
		t.Fatalf("ensureOIDCUser code = %q", code)
	}
	if got.Role != auth.RoleAdmin {
		t.Fatalf("role = %q, want %q", got.Role, auth.RoleAdmin)
	}
}

func TestEnsureOIDCUserAppliesExplicitGroupRole(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()

	hash, err := auth.HashPassword("unused")
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{
		Username:     "oidc-group-admin",
		PasswordHash: hash,
		Role:         auth.RoleViewer,
		AuthProvider: db.AuthProviderOIDC,
	}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}

	s := &Server{db: database}
	got, code := s.ensureOIDCUser(&auth.OIDCResult{
		Username:      "oidc-group-admin",
		Role:          auth.RoleAdmin,
		RoleFromGroup: true,
	}, &auth.OIDCConfig{AllowSignup: true, DefaultRole: auth.RoleViewer})
	if code != "" {
		t.Fatalf("ensureOIDCUser code = %q", code)
	}
	if got.Role != auth.RoleAdmin {
		t.Fatalf("role = %q, want %q", got.Role, auth.RoleAdmin)
	}
}

// TestEnsureOIDCUserLeavesOtherAccountsAlone: a single sign-on whose name
// belongs to a local or directory account is refused with
// username_collision and leaves the account as it was (RV116).
func TestEnsureOIDCUserLeavesOtherAccountsAlone(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	s := &Server{db: database}
	for _, provider := range []string{db.AuthProviderLocal, db.AuthProviderLDAP} {
		name := "boss-" + provider
		if err := database.CreateUser(&db.User{Username: name, PasswordHash: "hash", Role: auth.RoleAdmin, AuthProvider: provider}); err != nil {
			t.Fatal(err)
		}
		got, code := s.ensureOIDCUser(&auth.OIDCResult{Username: name, Role: auth.RoleViewer}, &auth.OIDCConfig{AllowSignup: true})
		if got != nil || code != "username_collision" {
			t.Errorf("sign-on as a %q account: user %v, code %q, want username_collision", provider, got, code)
		}
		if u, _ := database.GetUser(name); u == nil || u.AuthProvider != provider || u.Role != auth.RoleAdmin {
			t.Errorf("the %q account after the refused sign-on: %+v", provider, u)
		}
	}
}
