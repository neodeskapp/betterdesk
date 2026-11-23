package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

func change(current, next, confirm string) string {
	return fmt.Sprintf(`{"currentPassword":%q,"newPassword":%q,"confirmPassword":%q}`, current, next, confirm)
}

func TestPasswordChange(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		a := w.agent()
		want(t, "signed out", a.post("/api/auth/password", change(password, otherPassword, otherPassword)), http.StatusUnauthorized, notSignedIn)
		a.signIn("alice")
		other := w.agent()
		other.signIn("alice")
		a.signIn("alice")

		res := a.post("/api/auth/password", change(password, otherPassword, otherPassword))
		want(t, "change", res, http.StatusOK, `{"success":true}`)
		// The API server's form of hash, which its sign-in keeps (BUGS S12)
		if hash := w.text(`SELECT password_hash FROM users WHERE id = ?`, alice); !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") || !auth.VerifyPassword(hash, otherPassword) || auth.NeedsRehash(hash) {
			t.Errorf("the new hash is not the API server's PBKDF2 hash")
		}
		rows := w.audit()
		equal(t, "audit", rows[len(rows)-1:], []string{fmt.Sprintf("password_changed|Password changed|127.0.0.1|%d", alice)})
		// The session that changed the password goes on
		if res := a.get("/api/auth/verify"); res.status != http.StatusOK {
			t.Errorf("the session after the change: %d %s", res.status, res.body)
		}
		fresh := w.agent()
		want(t, "old password", fresh.post("/api/auth/login", login("alice", password)), http.StatusUnauthorized, invalidCredentials)
		if res := fresh.post("/api/auth/login", login("alice", otherPassword)); res.status != http.StatusOK {
			t.Errorf("new password: %d %s", res.status, res.body)
		}
	})
}

func TestPasswordChangeRefusals(t *testing.T) {
	// Every request counts against the limit of three in five minutes per
	// account, so every three cases get an account of their own
	const (
		required = `{"success":false,"error":"Password is required"}`
		mismatch = `{"success":false,"error":"Passwords do not match"}`
		wrong    = `{"success":false,"error":"Current password is incorrect"}`
		short    = `{"success":false,"error":"Password must be at least 8 characters"}`
	)
	cases := []struct {
		name, body string
		status     int
		want       string
	}{
		{"empty body", `{}`, 400, required},
		{"no new password", `{"currentPassword":"x"}`, 400, required},
		{"no current password", `{"newPassword":"x","confirmPassword":"x"}`, 400, required},
		{"empty current password", change("", otherPassword, otherPassword), 400, required},
		{"confirmation differs", change(password, otherPassword, otherPassword+"x"), 400, mismatch},
		{"no confirmation", fmt.Sprintf(`{"currentPassword":%q,"newPassword":%q}`, password, otherPassword), 400, mismatch},
		{"confirmation of another type", fmt.Sprintf(`{"currentPassword":%q,"newPassword":"12345678","confirmPassword":12345678}`, password), 400, mismatch},
		{"two equal lists are not the same", fmt.Sprintf(`{"currentPassword":%q,"newPassword":["a"],"confirmPassword":["a"]}`, password), 400, mismatch},
		{"wrong current password", change("not-the-password", otherPassword, otherPassword), 400, wrong},
		{"wrong current password before a short new one", change("not-the-password", "short", "short"), 400, wrong},
		{"new password of 7", change(password, "Short-7", "Short-7"), 400, short},
		// Values that are not text: a current password is a wrong one, a new
		// one is too short (BUGS.md AR2)
		{"number as current password", fmt.Sprintf(`{"currentPassword":12345678,"newPassword":%q,"confirmPassword":%q}`, otherPassword, otherPassword), 400, wrong},
		{"equal numbers as new password", fmt.Sprintf(`{"currentPassword":%q,"newPassword":123456789,"confirmPassword":123456789}`, password), 400, short},
		{"number as new password, wrong current one", `{"currentPassword":"not-the-password","newPassword":123456789,"confirmPassword":123456789}`, 400, wrong},
		{"true as new password", fmt.Sprintf(`{"currentPassword":%q,"newPassword":true,"confirmPassword":true}`, password), 400, short},
	}
	w := newWorld(t, nil, setup{trustProxy: "loopback"})
	var accounts []int64
	for i, c := range cases {
		// A new account for every three requests
		name := fmt.Sprintf("alice%d", i/3)
		if i%3 == 0 {
			accounts = append(accounts, w.addUser(user{name: name}))
		}
		a := w.agentFrom("198.51.100.1")
		a.signIn(name)
		want(t, c.name, a.post("/api/auth/password", c.body), c.status, c.want)
	}
	for _, id := range accounts {
		if hash := w.text(`SELECT password_hash FROM users WHERE id = ?`, id); hash != passwordHash {
			t.Error("a refused change replaced the password")
		}
	}
	for _, row := range w.audit() {
		if strings.HasPrefix(row, "password_changed|") {
			t.Errorf("a refused change wrote the audit entry %s", row)
		}
	}
}

func TestPasswordChangeLimit(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice"})
	a := w.agent()
	const tooMany = `{"success":false,"error":"Too many password change attempts. Please try again later."}`

	// Who is not signed in is refused before the limit and does not count
	for i := 0; i < 5; i++ {
		want(t, "signed out", a.post("/api/auth/password", `{}`), http.StatusUnauthorized, notSignedIn)
	}
	a.signIn("alice")
	// The limit is in front of the validation: refused requests count
	for i := 0; i < 3; i++ {
		res := a.post("/api/auth/password", `{}`)
		want(t, "within the limit", res, http.StatusBadRequest, `{"success":false,"error":"Password is required"}`)
		if got := res.header.Get("RateLimit-Limit") + " " + res.header.Get("RateLimit-Remaining"); got != fmt.Sprintf("3 %d", 2-i) {
			t.Errorf("RateLimit headers of request %d: %q", i+1, got)
		}
	}
	want(t, "the fourth", a.post("/api/auth/password", change(password, otherPassword, otherPassword)), http.StatusTooManyRequests, tooMany)
	if hash := w.text(`SELECT password_hash FROM users WHERE id = ?`, alice); hash != passwordHash {
		t.Error("a limited request changed the password")
	}
}

func TestPasswordChangeForAnAccountThatIsGone(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice"})
	a := w.agent()
	a.signIn("alice")
	w.exec(`DELETE FROM users WHERE id = ?`, alice)
	const gone = `{"success":false,"error":"User not found"}`
	want(t, "texts", a.post("/api/auth/password", change(password, otherPassword, otherPassword)), http.StatusBadRequest, gone)
	want(t, "a number", a.post("/api/auth/password", `{"currentPassword":1,"newPassword":"x","confirmPassword":"x"}`), http.StatusBadRequest, gone)
}

func TestPasswordChangeWhenTheAuditLogFails(t *testing.T) {
	w := newWorld(t, nil, setup{})
	alice := w.addUser(user{name: "alice"})
	a := w.agent()
	a.signIn("alice")
	w.exec(`DROP TABLE audit_log`)
	want(t, "change", a.post("/api/auth/password", change(password, otherPassword, otherPassword)), http.StatusInternalServerError, serverErrorBody)
	// The password was changed before the entry failed
	if hash := w.text(`SELECT password_hash FROM users WHERE id = ?`, alice); hash == passwordHash {
		t.Error("the password is the old one")
	}
}

// A new password longer than the sign-in takes (128 UTF-16 units) is
// refused, so nobody locks themselves out with it; 128 itself signs in
// (RV123).
func TestPasswordChangeRefusesWhatTheSignInRefuses(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		alice := w.addUser(user{name: "alice"})
		a := w.agent()
		a.signIn("alice")
		long := "Aa1!" + strings.Repeat("x", 125)
		want(t, "129 characters", a.post("/api/auth/password", change(password, long, long)), http.StatusBadRequest,
			`{"success":false,"error":"Password must be at most 128 characters"}`)
		if hash := w.text(`SELECT password_hash FROM users WHERE id = ?`, alice); hash != passwordHash {
			t.Error("the refused password was stored")
		}
		fits := long[:128]
		want(t, "128 characters", a.post("/api/auth/password", change(password, fits, fits)), http.StatusOK, `{"success":true}`)
		if res := w.agent().post("/api/auth/login", login("alice", fits)); res.status != http.StatusOK {
			t.Errorf("sign-in with the new password: %d %s", res.status, res.body)
		}
	})
}
