package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
)

// TestConsoleSignInsCountPerBrowser: the web console's in-process sign-ins
// name the browser's address (WithSignInAddress), and the login and
// directory checks count that address, not the console's own: six
// sign-ins from six addresses all reach the password check, the sixth of
// six from one address is refused (BUGS G8). A request from the listener
// cannot name an address.
func TestConsoleSignInsCountPerBrowser(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	h := srv.InternalHandler()
	bridge := Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin}
	call := func(path, user, ip string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"username":"`+user+`","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:0"
		ctx := WithIdentity(req.Context(), bridge)
		if ip != "" {
			ctx = WithSignInAddress(ctx, ip)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code
	}
	for n, path := range []string{"/api/auth/login", "/api/auth/ldap/verify"} {
		for i := 0; i < 6; i++ {
			if status := call(path, fmt.Sprintf("per-browser-%d", i), fmt.Sprintf("198.51.10%d.%d", n, i+1)); status == http.StatusTooManyRequests {
				t.Errorf("%s from browser %d: 429; each browser has its own budget", path, i+1)
			}
		}
		var statuses []int
		for i := 0; i < 6; i++ {
			statuses = append(statuses, call(path, fmt.Sprintf("one-browser-%d", i), fmt.Sprintf("203.0.113.%d", n+1)))
		}
		if statuses[4] == http.StatusTooManyRequests || statuses[5] != http.StatusTooManyRequests {
			t.Errorf("%s six times from one browser: %v; want the sixth refused", path, statuses)
		}
	}
}

// signInIP takes a named address only from an in-process caller.
func TestSignInAddressOnlyInProcess(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "192.0.2.10:4000"
	req = req.WithContext(WithSignInAddress(req.Context(), "198.51.100.1"))
	if got := srv.signInIP(req); got != "192.0.2.10" {
		t.Errorf("a request from the listener counts as %q", got)
	}
}
