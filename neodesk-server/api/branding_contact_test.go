package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestBrandingContactIsChecked: Email must be an address and Website an
// http or https address; anything else is refused and stays out of the
// public GET /api/branding every client reads. Empty clears either (L385).
func TestBrandingContactIsChecked(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, body := range []string{
		`{"email":"not-an-email"}`,
		`{"email":"Support <support@example.com>"}`,
		`{"website":"javascript:alert(1)"}`,
		`{"website":"example.com"}`,
		`{"website":"data:text/html,x"}`,
		`{"website":"https://"}`,
	} {
		if status, answer := asConsole(t, srv, http.MethodPost, "/api/branding", body); status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", body, status, answer)
		}
	}
	for _, key := range []string{"branding_email", "branding_website"} {
		if v, _ := database.GetConfig(key); v != "" {
			t.Errorf("%s stored %q after refused saves", key, v)
		}
	}
	if status, answer := asConsole(t, srv, http.MethodPost, "/api/branding", `{"email":" support@example.com ","website":"https://example.com/help"}`); status != http.StatusOK {
		t.Fatalf("a good contact: %d %s, want 200", status, answer)
	}
	status, public := listenerCall(t, srv, http.MethodGet, "/api/branding", "", nil)
	if status != http.StatusOK || !strings.Contains(public, `"support@example.com"`) || !strings.Contains(public, `"https://example.com/help"`) {
		t.Errorf("the public branding: %d %s", status, public)
	}
	if status, answer := asConsole(t, srv, http.MethodPost, "/api/branding", `{"email":"","website":""}`); status != http.StatusOK {
		t.Errorf("clearing the contact: %d %s, want 200", status, answer)
	}
}
