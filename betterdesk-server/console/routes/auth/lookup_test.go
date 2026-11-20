package auth_test

import (
	"net/http"
	"testing"
)

// The typeahead names accounts and leaves their roles out: a visitor who
// is not signed in learns no administrator from it (BUGS UM26).
func TestLookup(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		for _, name := range []string{"Operator", "op_one", "viewer", "op_two"} {
			w.addUser(user{name: name})
		}
		w.exec(`UPDATE users SET role = 'super_admin' WHERE username = 'Operator'`)
		w.exec(`UPDATE users SET role = '' WHERE username = 'op_two'`)
		a := w.agent()
		want(t, "prefix", a.get("/login/lookup?q=%20OP%20"), http.StatusOK,
			`{"success":true,"matches":[{"username":"Operator"},{"username":"op_one"},{"username":"op_two"}]}`)
		want(t, "one letter", a.get("/login/lookup?q=o"), http.StatusOK, `{"success":true,"matches":[]}`)
		want(t, "twice", a.get("/login/lookup?q=op&q=vi"), http.StatusOK, `{"success":true,"matches":[]}`)
		// Signed in or not, the answer is the same and sets no session
		if res := a.get("/login/lookup?q=vi"); res.cookie("bd.sid") != "" || res.body != `{"success":true,"matches":[{"username":"viewer"}]}` {
			t.Errorf("lookup: %q %s", res.cookies(), res.body)
		}
	})
}

// The typeahead counts against the limit of sign-ins of the address.
func TestLookupSharesTheSignInLimiter(t *testing.T) {
	w := newWorld(t, nil, setup{env: map[string]string{"LOGIN_RATE_LIMIT_MAX": "3"}})
	w.addUser(user{name: "alice"})
	a := w.agent()
	const tooMany = `{"success":false,"error":"Too many login attempts. Please try again in a minute."}`
	want(t, "first", a.get("/login/lookup?q=al"), http.StatusOK, `{"success":true,"matches":[{"username":"alice"}]}`)
	if res := a.post("/api/auth/login", login("alice", "not-the-password")); res.status != http.StatusUnauthorized {
		t.Errorf("second: %d %s", res.status, res.body)
	}
	if got := a.get("/login/lookup?q=al").header.Get("RateLimit-Remaining"); got != "0" {
		t.Errorf("RateLimit-Remaining after three: %q", got)
	}
	want(t, "fourth", a.get("/login/lookup?q=al"), http.StatusTooManyRequests, tooMany)
	want(t, "sign-in after it", a.post("/api/auth/login", login("alice", password)), http.StatusTooManyRequests, tooMany)
}
