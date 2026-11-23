package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
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

// Only failed sign-ins count against an address: behind one office
// address everyone signs in, while five wrong passwords in five minutes
// still lock the address (L352).
func TestSuccessfulSignInsDoNotCountAgainstTheAddress(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetJWTManager(auth.NewJWTManager("signin-limit-test-secret", time.Hour))
	hash := mustHash(t, "office-pass-1")
	for i := 0; i < 7; i++ {
		if err := database.CreateUser(&db.User{Username: fmt.Sprintf("office-%d", i), PasswordHash: hash, Role: auth.RoleOperator}); err != nil {
			t.Fatal(err)
		}
	}
	h := srv.InternalHandler()
	bridge := Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin}
	call := func(user, password string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+user+`","password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:0"
		ctx := WithSignInAddress(WithIdentity(req.Context(), bridge), "203.0.113.50")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code
	}
	for i := 0; i < 7; i++ {
		if status := call(fmt.Sprintf("office-%d", i), "office-pass-1"); status != http.StatusOK {
			t.Errorf("sign-in %d of seven people from one address: %d", i+1, status)
		}
	}
	for i := 0; i < 5; i++ {
		if status := call("office-0", "wrong"); status != http.StatusUnauthorized {
			t.Errorf("wrong password %d: %d", i+1, status)
		}
	}
	if status := call("office-1", "office-pass-1"); status != http.StatusTooManyRequests {
		t.Errorf("after five wrong passwords from the address: %d, want 429", status)
	}
}
