package impersonate_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The impersonation cookie's value put into the session cookie is no
// session: before, it loaded the impersonation row as a plain session of
// the person viewed, writable, without the time limit or the audit trail
// (found in the review of 26 Nov 2026). Now the two cookies are signed
// apart, so the value is no session cookie at all, and a session cookie
// that does name the impersonation row (TestAnImpersonationRowIsNoSession)
// is refused and ends the row. The other way round, the admin's session
// cookie as an impersonation cookie, is no impersonation and leaves the
// admin signed in.
func TestTheImpersonationCookieIsNoSessionCookie(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)

	swapped := w.SignedOut()
	swapped.SetCookie(sidCookie, a.Cookie(impCookie))
	if name, res := whoAmI(t, swapped); name != "" {
		t.Errorf("the swapped cookie signs in as %q: %d", name, res.Status)
	}
	res := swapped.Do("PUT", "/api/users/me/profile", `{"email":"attacker@evil.test","first_name":"Owned"}`)
	if res.Status != http.StatusUnauthorized && res.Status != http.StatusForbidden {
		t.Errorf("a write with the swapped cookie: %d %s", res.Status, res.Body)
	}
	var email string
	_ = w.DB.QueryRow(context.Background(), `SELECT COALESCE(email, '') FROM users WHERE id = ?`, idOperator).Scan(&email)
	if email == "attacker@evil.test" {
		t.Fatalf("the operator's email address is now %q", email)
	}

	b := w.As("super_admin")
	own := b.Cookie(sidCookie)
	b.SetCookie(impCookie, own)
	if name, res := whoAmI(t, b); name != "e2e_super_admin" || res.Status != http.StatusOK {
		t.Errorf("the session cookie as an impersonation cookie: %q %d", name, res.Status)
	}
}

// A session cookie signed the plain way for the impersonation row's id (a
// leaked id, or a cookie from before the two were signed apart) is no
// session: the row is refused through the session cookie and ends, which
// ends the admin's impersonation too.
func TestAnImpersonationRowIsNoSession(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := start(t, w, "super_admin", idOperator)
	raw, err := url.QueryUnescape(a.Cookie(impCookie))
	if err != nil || !strings.HasPrefix(raw, "s:") || !strings.Contains(raw, ".") {
		t.Fatalf("impersonation cookie %q", a.Cookie(impCookie))
	}
	id := raw[2:strings.LastIndexByte(raw, '.')]
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	mac.Write([]byte(id))
	forged := url.QueryEscape("s:" + id + "." + base64.RawStdEncoding.EncodeToString(mac.Sum(nil)))

	other := w.SignedOut()
	other.SetCookie(sidCookie, forged)
	if name, res := whoAmI(t, other); name != "" {
		t.Errorf("a session cookie for the impersonation row signs in as %q: %d", name, res.Status)
	}
	if res := other.Do("PUT", "/api/users/me/profile", `{"first_name":"Owned"}`); res.Status != http.StatusUnauthorized && res.Status != http.StatusForbidden {
		t.Errorf("a write with it: %d %s", res.Status, res.Body)
	}
	// The impersonation ended: the next API call says so, then the browser
	// is the admin again
	if _, res := whoAmI(t, a); res.Status != http.StatusConflict || code(t, res) != "impersonation_ended" {
		t.Errorf("the admin's next call after the forged cookie: %d %s, want 409 impersonation_ended", res.Status, res.Body)
	}
	if name, _ := whoAmI(t, a); name != "e2e_super_admin" {
		t.Errorf("the admin's browser after that: %q, want the admin", name)
	}
}

// sessionSecret is the session secret of the route test world.
const sessionSecret = "test-secret-0123456789abcdef0123456789abcdef"
