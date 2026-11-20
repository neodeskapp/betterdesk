package rustdeskapi

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/api"
)

// A password the console cannot check makes it ask the API server, and
// the call names the client's address, which the API server's limits
// count (BUGS G8).
func TestLoginNamesTheClientsAddressToTheAPIServer(t *testing.T) {
	var mu sync.Mutex
	var addrs []string
	h, _ := directConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			ip, _ := api.SignInAddress(r.Context())
			mu.Lock()
			addrs = append(addrs, ip)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"Invalid credentials"}`)
	}))
	if got := post(h, "/api/login", []byte(`{"username":"e2e_viewer","password":"pw"}`)); got.Code != http.StatusUnauthorized {
		t.Fatalf("a password the API server refuses: %d %s", got.Code, got.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 1 || addrs[0] != "192.0.2.1" {
		t.Errorf("the API server's sign-in calls named %q; want the client's address", addrs)
	}
}

// A local account whose name the API server's directory also signs in is
// refused with 409, and the attempt counts as a failure.
func TestAUsernameCollisionIsRefused(t *testing.T) {
	h, db := directConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/login" {
			_, _ = io.WriteString(w, `{"token":"t","role":"admin","auth_provider":"ldap"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	got := post(h, "/api/login", []byte(`{"username":"e2e_viewer","password":"pw"}`))
	if got.Code != http.StatusConflict || got.Body.String() != `{"error":"Username collision: a local account with this name already exists","code":"username_collision"}` {
		t.Errorf("collision: %d %s", got.Code, got.Body)
	}
	var action, details string
	if err := db.QueryRow(context.Background(), "SELECT action, details FROM audit_log ORDER BY id DESC LIMIT 1").Scan(&action, &details); err != nil || action != "api_login_failed" || details != "Username collision: e2e_viewer" {
		t.Errorf("audit entry %q %q, %v", action, details, err)
	}
	var failed int
	if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM login_attempts WHERE username = 'e2e_viewer' AND success = 0").Scan(&failed); err != nil || failed != 1 {
		t.Errorf("failed attempts recorded: %d, %v", failed, err)
	}
}
