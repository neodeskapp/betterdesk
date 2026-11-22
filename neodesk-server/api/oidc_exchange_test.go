package api

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
)

func exchangeOIDCCodeForTest(t *testing.T, s *Server, code string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/exchange", strings.NewReader(`{"code":"`+code+`"}`))
	rec := httptest.NewRecorder()
	s.handleOIDCExchange(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The exchange response keeps its fields and adds "picture".
func TestHandleOIDCExchangeIncludesPicture(t *testing.T) {
	p := auth.NewOIDCProvider(&auth.OIDCConfig{Enabled: true, ClientID: "c"})
	t.Cleanup(p.Close)
	s := &Server{oidcProvider: p}

	code, err := p.StoreAuthCode("jwt-token", "alice", "operator", "/devices", "https://idp.example/alice.png")
	if err != nil {
		t.Fatal(err)
	}
	body := exchangeOIDCCodeForTest(t, s, code)
	want := map[string]string{
		"token":      "jwt-token",
		"username":   "alice",
		"role":       "operator",
		"return_url": "/devices",
		"picture":    "https://idp.example/alice.png",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %q", k, body[k], v)
		}
	}
}

// privateIPv4 is an RFC 1918 address of one of this machine's interfaces.
// The test is skipped on a machine without one.
func privateIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.String()
		}
	}
	t.Skip("no private IPv4 address on this machine for a fake identity provider")
	return ""
}

// A panel sign-in that comes back to the API port's callback hands the
// console a one-time code whose exchange names the sign-in's state, the
// value the console's bd.oidc cookie holds for a callback it did not see
// itself (RV120).
func TestOIDCCallbackCodeExchangesWithTheState(t *testing.T) {
	// The provider never fetches from loopback, so the token endpoint
	// listens on a private address of this machine, allowed by name.
	ip := privateIPv4(t)
	ln, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Fatal(err)
	}
	var idToken string
	tokenSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access-token", "id_token": idToken})
	}))
	tokenSrv.Listener.Close()
	tokenSrv.Listener = ln
	tokenSrv.Start()
	t.Cleanup(tokenSrv.Close)

	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("oidc-state-secret-0123456789abcdef", time.Hour))
	for k, v := range map[string]string{
		"oidc.enabled":               "true",
		"oidc.client_id":             "console",
		"oidc.issuer_url":            "https://idp.example.com",
		"oidc.redirect_url":          "http://localhost:21114/api/auth/oidc/callback",
		"oidc.authorization_url":     "https://idp.example.com/authorize",
		"oidc.token_url":             tokenSrv.URL + "/token",
		"oidc.auto_discovery":        "false",
		"oidc.allowed_private_cidrs": ip + "/32",
	} {
		if err := database.SetConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}
	srv.InitOIDC()
	t.Cleanup(srv.oidcProvider.Close)

	authURL, state, err := srv.oidcProvider.BuildAuthURL("/devices")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"nonce": parsed.Query().Get("nonce"), "iss": "https://idp.example.com", "aud": "console",
		"preferred_username": "sso-alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	idToken = "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=idp-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	srv.handleOIDCCallback(rec, req)
	loc, err := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || err != nil || loc.Path != "/api/auth/oidc/session" {
		t.Fatalf("callback: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	body := exchangeOIDCCodeForTest(t, srv, loc.Query().Get("code"))
	if body["state"] != state || body["username"] != "sso-alice" || body["return_url"] != "/devices" {
		t.Errorf("exchange: state %v (want %q), user %v, return_url %v", body["state"], state, body["username"], body["return_url"])
	}
}

// Without a picture the field is present and empty, so the console can clear
// a stored picture.
func TestHandleOIDCExchangeEmptyPicture(t *testing.T) {
	p := auth.NewOIDCProvider(&auth.OIDCConfig{Enabled: true, ClientID: "c"})
	t.Cleanup(p.Close)
	s := &Server{oidcProvider: p}

	code, _ := p.StoreAuthCode("jwt-token", "bob", "viewer", "/", "")
	body := exchangeOIDCCodeForTest(t, s, code)
	pic, ok := body["picture"]
	if !ok {
		t.Fatal("picture field missing")
	}
	if pic != "" {
		t.Fatalf("picture = %v, want empty", pic)
	}
}
