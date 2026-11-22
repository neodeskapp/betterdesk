package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A single sign-on that was never saved uses PKCE, and the settings show it
// on; a saved configuration keeps the value it was saved with.
func TestOIDCConfigUsesPKCEUnlessSavedOff(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	s := newClientLoginTestServer(database)

	if !s.loadOIDCConfigFromDB().UsePKCE {
		t.Fatal("a configuration that was never saved must use PKCE")
	}
	rec := httptest.NewRecorder()
	s.handleGetOIDCConfig(rec, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/config", nil))
	var shown map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if shown["use_pkce"] != true {
		t.Fatalf("use_pkce of a configuration that was never saved = %v, want true", shown["use_pkce"])
	}

	for _, saved := range []bool{false, true} {
		body := `{"enabled":false,"use_pkce":` + map[bool]string{false: "false", true: "true"}[saved] + `}`
		rec := httptest.NewRecorder()
		s.handleSaveOIDCConfig(rec, httptest.NewRequest(http.MethodPut, "/api/auth/oidc/config", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if got := s.loadOIDCConfigFromDB().UsePKCE; got != saved {
			t.Errorf("use_pkce saved as %v reads back as %v", saved, got)
		}
	}
}
