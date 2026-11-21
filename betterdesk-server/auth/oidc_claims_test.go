package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// oidcIssuer is the issuer the test provider's ID tokens name.
const oidcIssuer = "https://idp.example.com"

// exchangeClaims runs a panel sign-in against a token endpoint whose ID
// token carries claims. The members nonce, iss and aud are filled with the
// right values unless claims names them itself (nil leaves one out).
func exchangeClaims(t *testing.T, claims map[string]any) (*OIDCResult, error) {
	t.Helper()
	var idToken string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access-token", "id_token": idToken})
	}))
	t.Cleanup(tokenSrv.Close)
	p := newTestOIDCProvider(t, &OIDCConfig{
		Enabled:          true,
		ClientID:         "console",
		IssuerURL:        oidcIssuer,
		RedirectURL:      "http://localhost/callback",
		AuthorizationURL: "https://idp.example.com/authorize",
		TokenURL:         tokenSrv.URL + "/token",
	})
	authURL, state, err := p.BuildAuthURL("/")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]any{"nonce": parsed.Query().Get("nonce"), "iss": oidcIssuer, "aud": "console"}
	for k, v := range claims {
		if v == nil {
			delete(full, k)
			continue
		}
		full[k] = v
	}
	payload, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	idToken = "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	return p.ExchangeCode(context.Background(), "code", state)
}

// testIDToken is an unsigned ID token with the claims and the nonce of the
// sign-in that authURL starts.
func testIDToken(t *testing.T, authURL string, claims map[string]interface{}) string {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	claims["nonce"] = parsed.Query().Get("nonce")
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// The part of an e-mail address before the @ is a user name only when the
// provider says the address is verified (RV116).
func TestExchangeCodeEmailPrefixNeedsAVerifiedAddress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"unverified", map[string]any{"email": "admin@attacker.example", "sub": "idp-42"}, "idp-42"},
		{"verified false", map[string]any{"email": "admin@attacker.example", "email_verified": false, "sub": "idp-42"}, "idp-42"},
		{"verified as text false", map[string]any{"email": "admin@attacker.example", "email_verified": "false", "sub": "idp-42"}, "idp-42"},
		{"verified", map[string]any{"email": "alice@corp.example", "email_verified": true, "sub": "idp-42"}, "alice"},
		{"verified as text", map[string]any{"email": "alice@corp.example", "email_verified": "true", "sub": "idp-42"}, "alice"},
		{"name claim first", map[string]any{"preferred_username": "bob", "email": "alice@corp.example", "email_verified": true}, "bob"},
	} {
		result, err := exchangeClaims(t, tc.claims)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if result.Username != tc.want {
			t.Errorf("%s: user name %q, want %q", tc.name, result.Username, tc.want)
		}
	}
	if result, err := exchangeClaims(t, map[string]any{"email": "admin@attacker.example"}); err == nil {
		t.Errorf("an unverified address and no sub gave the user name %q", result.Username)
	}
}

// The ID token's nonce must be the one sent with the sign-in, its audience
// must name the client and its issuer must be the provider's (RV120).
func TestExchangeCodeChecksNonceIssuerAndAudience(t *testing.T) {
	base := map[string]any{"preferred_username": "alice"}
	with := func(k string, v any) map[string]any {
		claims := map[string]any{k: v}
		for key, value := range base {
			claims[key] = value
		}
		return claims
	}
	for _, tc := range []struct {
		name   string
		claims map[string]any
		ok     bool
	}{
		{"all right", base, true},
		{"wrong nonce", with("nonce", "not-the-nonce"), false},
		{"no nonce", with("nonce", nil), false},
		{"foreign issuer", with("iss", "https://attacker.example"), false},
		{"no issuer", with("iss", nil), false},
		{"issuer with a slash", with("iss", oidcIssuer+"/"), true},
		{"foreign audience", with("aud", "someone-else"), false},
		{"no audience", with("aud", nil), false},
		{"audience list", with("aud", []any{"other", "console"}), true},
		{"audience list without the client", with("aud", []any{"other"}), false},
	} {
		result, err := exchangeClaims(t, tc.claims)
		if tc.ok && (err != nil || result.Username != "alice") {
			t.Errorf("%s: %+v, %v", tc.name, result, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: accepted as %q", tc.name, result.Username)
		}
	}
}

// An ID token that cannot be read stops the sign-in.
func TestExchangeCodeRefusesAnUnreadableIDToken(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "a", "id_token": "not-a-jwt"})
	}))
	t.Cleanup(tokenSrv.Close)
	p := newTestOIDCProvider(t, &OIDCConfig{
		Enabled: true, ClientID: "console", RedirectURL: "http://localhost/callback",
		AuthorizationURL: "https://idp.example.com/authorize", TokenURL: tokenSrv.URL + "/token",
	})
	_, state, err := p.BuildAuthURL("/")
	if err != nil {
		t.Fatal(err)
	}
	if result, err := p.ExchangeCode(context.Background(), "code", state); err == nil {
		t.Errorf("accepted: %+v", result)
	}
}

// The issuer of the discovery document counts over the configured URL,
// and "{tenantid}" in it stands for the token's tid.
func TestCheckIDTokenIssuerFromDiscovery(t *testing.T) {
	p := newTestOIDCProvider(t, &OIDCConfig{ClientID: "console", IssuerURL: "https://login.microsoftonline.com/common/v2.0"})
	p.mu.Lock()
	p.discovery = &oidcDiscovery{Issuer: "https://login.microsoftonline.com/{tenantid}/v2.0"}
	p.mu.Unlock()
	claims := map[string]any{"nonce": "n", "aud": "console", "iss": "https://login.microsoftonline.com/t-1/v2.0", "tid": "t-1"}
	if err := p.checkIDTokenClaims(claims, "n", p.GetConfig()); err != nil {
		t.Errorf("tenant issuer: %v", err)
	}
	claims["tid"] = "t-2"
	if err := p.checkIDTokenClaims(claims, "n", p.GetConfig()); err == nil {
		t.Error("an issuer of another tenant was accepted")
	}
	p.mu.Lock()
	p.discovery = &oidcDiscovery{Issuer: "https://idp.example.com"}
	p.mu.Unlock()
	claims = map[string]any{"nonce": "n", "aud": "console", "iss": "https://login.microsoftonline.com/common/v2.0"}
	if err := p.checkIDTokenClaims(claims, "n", p.GetConfig()); err == nil {
		t.Error("the configured issuer counted over the discovered one")
	}
}

// A one-time code remembers the state of its sign-in for the exchange.
func TestAuthCodeKeepsTheState(t *testing.T) {
	p := newTestOIDCProvider(t, &OIDCConfig{})
	code, err := p.StoreAuthCodeForState("jwt", "alice", "viewer", "/", "", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	if entry := p.ConsumeAuthCode(code); entry == nil || entry.State != "state-1" || entry.Username != "alice" {
		t.Errorf("entry: %+v", entry)
	}
	plain, err := p.StoreAuthCode("jwt", "bob", "viewer", "/", "")
	if err != nil {
		t.Fatal(err)
	}
	if entry := p.ConsumeAuthCode(plain); entry == nil || entry.State != "" {
		t.Errorf("entry without a state: %+v", entry)
	}
}
