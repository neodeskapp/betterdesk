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
