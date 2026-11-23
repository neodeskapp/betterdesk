package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// TestSingleSignOnStartsAreBounded: the public single sign-on start routes
// take a limited number of calls per address and refuse overlong fields,
// so no caller fills the pending entries; the console's in-process calls
// are not limited (RV186).
func TestSingleSignOnStartsAreBounded(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	srv.oidcProvider = auth.NewOIDCProvider(&auth.OIDCConfig{
		Enabled: true, ClientID: "c",
		AuthorizationURL: "https://idp.example.com/a",
		RedirectURL:      "http://localhost/cb",
	})
	start := `{"id":"PEERAAA1","uuid":"u-1","deviceInfo":{"name":"desk","os":"linux","type":"client"}}`

	long := `{"id":"` + strings.Repeat("A", 300) + `","uuid":"u-1"}`
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/oidc/auth", long, nil); status != http.StatusBadRequest {
		t.Errorf("POST /api/oidc/auth with a 300 character id: %d %s, want 400", status, body)
	}
	limited := func(method, path, body string) bool {
		t.Helper()
		for range 30 {
			if status, _ := listenerCall(t, srv, method, path, body, nil); status == http.StatusTooManyRequests {
				return true
			}
		}
		return false
	}
	if !limited(http.MethodPost, "/api/oidc/auth", start) {
		t.Error("30 starts of the client sign-in from one address were all taken")
	}
	if !limited(http.MethodGet, "/api/auth/oidc/authorize?return_url=/devices", "") {
		t.Error("30 starts of the panel sign-in from one address were all taken")
	}
	for i := range 30 {
		if status, body := asConsole(t, srv, http.MethodGet, "/api/auth/oidc/authorize?return_url=/devices", ""); status != http.StatusFound {
			t.Fatalf("in-process start %d: %d %s, want 302", i, status, body)
		}
	}
}
