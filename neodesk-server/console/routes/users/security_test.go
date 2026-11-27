package users_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// listed is an account as GET /api/users lists its sign-in.
type listed struct {
	ID          json.Number `json:"id"`
	Username    string      `json:"username"`
	Locked      bool        `json:"locked"`
	LockedUntil *string     `json:"locked_until"`
	Locks       []struct {
		IP       string `json:"ip"`
		Until    string `json:"until"`
		Failures int    `json:"failures"`
	} `json:"locks"`
	MustChangePassword bool    `json:"must_change_password"`
	TwoFactorRequired  bool    `json:"two_factor_required"`
	TwoFactorState     string  `json:"two_factor_state"`
	PasswordResetUntil *string `json:"password_reset_until"`
}

// listedAs reads the account of a name from the users list.
func listedAs(t *testing.T, admin *routetest.Agent, name string) listed {
	t.Helper()
	got := admin.Do("GET", "/api/users", "")
	var out struct {
		Data struct {
			Users []listed `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &out); err != nil {
		t.Fatalf("users list: %d %s", got.Status, got.Body)
	}
	for _, u := range out.Data.Users {
		if u.Username == name {
			return u
		}
	}
	t.Fatalf("%s is not in the list", name)
	return listed{}
}

// auditAbout lists the audit entries about an account as
// "action|details|actor".
func auditRowsAbout(t *testing.T, w *routetest.World, id string) []string {
	t.Helper()
	rows, err := w.DB.Query(context.Background(), `SELECT action, COALESCE(details, ''), COALESCE(user_id, 0) FROM audit_log WHERE target_user_id = ? ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var action, details string
		var actor int64
		if err := rows.Scan(&action, &details, &actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, action+"|"+details+"|"+strconv.FormatInt(actor, 10))
	}
	return out
}

// mailsTo waits a moment for the mails the outbox got for an address and
// returns their subjects.
func mailsTo(box *outbox, address string, wait time.Duration) []mail.Outgoing {
	deadline := time.Now().Add(wait)
	for {
		box.mu.Lock()
		var out []mail.Outgoing
		for _, m := range box.sent {
			if strings.Join(m.To, ",") == address {
				out = append(out, m)
			}
		}
		box.mu.Unlock()
		if time.Now().After(deadline) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// subjects are the subjects of mails.
func subjects(ms []mail.Outgoing) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Subject)
	}
	return out
}

func signIn(w *routetest.World, name, password string) routetest.Answer {
	return w.SignedOut().Do("POST", "/api/auth/login", `{"username":"`+name+`","password":"`+password+`"}`)
}

var temporaryForm = regexp.MustCompile(`^[A-Za-z0-9]{4}-[A-Za-z0-9]{4}-[A-Za-z0-9]{4}-[A-Za-z0-9]{4}$`)

// A temporary password: made up when none is given and answered once; the
// account's sessions end, its next sign-in asks for a new password before
// it gets a session, the temporary one is refused as the new one, and the
// new one signs in plainly. The person gets a mail; the audit log names the
// account. Not for the caller's own account, a directory account, or
// someone without user.security.
func TestTemporaryPassword(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "carol", "carol@example.test")
	carol := w.SignedOut()
	if got := carol.Do("POST", "/api/auth/login", `{"username":"carol","password":"Old-Pass-123!"}`); got.Status != http.StatusOK {
		t.Fatalf("sign in: %d %s", got.Status, got.Body)
	}

	got := admin.Do("POST", "/api/users/"+id+"/temporary-password", `{}`)
	var out struct {
		Data struct {
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &out); err != nil || got.Status != http.StatusOK || !temporaryForm.MatchString(out.Data.Password) {
		t.Fatalf("temporary password: %d %s", got.Status, got.Body)
	}
	temporary := out.Data.Password
	if got := carol.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
		t.Errorf("carol's session goes on: %d", got.Status)
	}
	if !listedAs(t, admin, "carol").MustChangePassword {
		t.Error("the list doesn't say carol must change her password")
	}

	person := w.SignedOut()
	expect(t, "sign-in with the temporary password", person.Do("POST", "/api/auth/login", `{"username":"carol","password":"`+temporary+`"}`),
		200, `{"success":true,"passwordChangeRequired":true}`)
	if got := person.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusUnauthorized {
		t.Errorf("signed in before the new password: %d %s", got.Status, got.Body)
	}
	expect(t, "the temporary one again", person.Do("POST", "/api/auth/password/required", `{"newPassword":"`+temporary+`","confirmPassword":"`+temporary+`"}`), 400,
		`{"success":false,"error":"Choose a password other than the one you signed in with."}`)
	expect(t, "not the same twice", person.Do("POST", "/api/auth/password/required", `{"newPassword":"Chosen-Pass-1!","confirmPassword":"Chosen-Pass-2!"}`), 400, "")
	got = person.Do("POST", "/api/auth/password/required", `{"newPassword":"Chosen-Pass-1!","confirmPassword":"Chosen-Pass-1!"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"username":"carol"`) {
		t.Fatalf("new password: %d %s", got.Status, got.Body)
	}
	if got := person.Do("GET", "/api/auth/verify", ""); got.Status != http.StatusOK {
		t.Errorf("not signed in after the new password: %d", got.Status)
	}
	if listedAs(t, admin, "carol").MustChangePassword {
		t.Error("the list still says carol must change her password")
	}
	if got := signIn(w, "carol", "Chosen-Pass-1!"); got.Status != http.StatusOK || strings.Contains(got.Body, "passwordChangeRequired") {
		t.Errorf("sign-in with the new password: %d %s", got.Status, got.Body)
	}
	if got := signIn(w, "carol", temporary); got.Status != http.StatusUnauthorized {
		t.Errorf("the temporary password still works: %d", got.Status)
	}
	rows := strings.Join(auditRowsAbout(t, w, id), "\n")
	if !strings.Contains(rows, "temporary_password_set|Set a temporary password for user: carol|1") ||
		!strings.Contains(rows, "password_changed|Password changed at sign-in, as required|"+id) {
		t.Errorf("audit about carol:\n%s", rows)
	}
	mails := subjects(mailsTo(box, "carol@example.test", 300*time.Millisecond))
	if len(mails) < 2 || mails[0] != "The password of carol was changed" {
		t.Errorf("mails to carol: %q", mails)
	}
	if m := mailsTo(box, "carol@example.test", 0); !strings.Contains(*m[0].Text, "temporary password") || !strings.Contains(*m[0].Text, "tell your administrator") {
		t.Errorf("the notice:\n%s", *m[0].Text)
	}

	// A password the admin gives is taken as it is
	got = admin.Do("POST", "/api/users/"+id+"/temporary-password", `{"password":"Given-Temp-9!"}`)
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"password":"Given-Temp-9!"`) {
		t.Errorf("given password: %d %s", got.Status, got.Body)
	}
	expect(t, "too short", admin.Do("POST", "/api/users/"+id+"/temporary-password", `{"password":"short"}`), 400, "")
	expect(t, "own account", admin.Do("POST", "/api/users/1/temporary-password", `{}`), 400,
		`{"success":false,"error":"Change your own sign-in on Your account."}`)
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/"+id+"/temporary-password", `{}`), 403, "")
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET auth_provider = 'ldap' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	expect(t, "directory account", admin.Do("POST", "/api/users/"+id+"/temporary-password", `{}`), 400,
		`{"success":false,"error":"This account signs in with LDAP/AD. Reset its password there."}`)
	expect(t, "directory account, set now", admin.Do("POST", "/api/users/"+id+"/reset-password", `{"newPassword":"Some-Pass-123!"}`), 400,
		`{"success":false,"error":"This account signs in with LDAP/AD. Reset its password there."}`)
	expect(t, "directory account, link", admin.Do("POST", "/api/users/"+id+"/password-reset-link", `{}`), 400,
		`{"success":false,"error":"This account signs in with LDAP/AD. Reset its password there."}`)
}

// Several accounts at once: each id gets its own outcome, in the order
// given; the list shows the state; false takes it back.
func TestRequirePasswordChangeForSeveral(t *testing.T) {
	w, _ := mailWorld(t, true)
	admin := w.As("admin")
	carol := makeAccount(t, w, admin, "carol", "carol@example.test")
	dirk := makeAccount(t, w, admin, "dirk", "")
	if _, err := w.DB.Exec(context.Background(), `UPDATE users SET auth_provider = 'ldap' WHERE id = ?`, dirk); err != nil {
		t.Fatal(err)
	}
	body := `{"ids":[` + carol + `,"` + dirk + `",1,99999,"x",2]}`
	got := admin.Do("POST", "/api/users/require-password-change", body)
	want := `{"success":true,"data":{"results":[` +
		`{"id":` + carol + `,"username":"carol","outcome":"changed"},` +
		`{"id":` + dirk + `,"username":"dirk","outcome":"refused","error":"This account signs in with LDAP/AD. Reset its password there."},` +
		`{"id":1,"username":"e2e_admin","outcome":"refused","error":"Change your own sign-in on Your account."},` +
		`{"id":99999,"outcome":"refused","error":"User not found"},` +
		`{"id":"x","outcome":"refused","error":"Invalid user ID"},` +
		`{"id":2,"username":"e2e_super_admin","outcome":"changed"}]}}`
	expect(t, "require", got, 200, want)
	if !listedAs(t, admin, "carol").MustChangePassword || listedAs(t, admin, "dirk").MustChangePassword {
		t.Error("the list shows the wrong accounts")
	}
	if got := signIn(w, "carol", "Old-Pass-123!"); got.Body != `{"success":true,"passwordChangeRequired":true}` {
		t.Errorf("carol's sign-in: %s", got.Body)
	}
	expect(t, "again", admin.Do("POST", "/api/users/require-password-change", `{"ids":[`+carol+`]}`), 200,
		`{"success":true,"data":{"results":[{"id":`+carol+`,"username":"carol","outcome":"unchanged"}]}}`)
	expect(t, "take back", admin.Do("POST", "/api/users/require-password-change", `{"ids":[`+carol+`],"required":false}`), 200,
		`{"success":true,"data":{"results":[{"id":`+carol+`,"username":"carol","outcome":"changed"}]}}`)
	if listedAs(t, admin, "carol").MustChangePassword {
		t.Error("taken back, but listed")
	}
	rows := strings.Join(auditRowsAbout(t, w, carol), "\n")
	if !strings.Contains(rows, "password_change_required|Required a new password at the next sign-in of user: carol|1") ||
		!strings.Contains(rows, "password_change_requirement_removed|No longer requires a new password of user: carol|1") {
		t.Errorf("audit:\n%s", rows)
	}
	expect(t, "no list", admin.Do("POST", "/api/users/require-password-change", `{"ids":[]}`), 400, "")
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/require-password-change", `{"ids":[`+carol+`]}`), 403, "")
}

// lockEnv lets a test make enough failed sign-ins for a lockout.
var lockEnv = map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}

// Failed sign-ins lock an account at the address: the list shows it with
// until when, Unlock ends it and the password works again at once.
func TestUnlock(t *testing.T) {
	w, _ := mailWorldEnv(t, true, lockEnv)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "erin", "")
	for i := 0; i < 10; i++ {
		signIn(w, "erin", "wrong-password")
	}
	if got := signIn(w, "erin", "Old-Pass-123!"); got.Status != http.StatusTooManyRequests {
		t.Fatalf("not locked: %d %s", got.Status, got.Body)
	}
	state := listedAs(t, admin, "erin")
	if !state.Locked || state.LockedUntil == nil || len(state.Locks) != 1 || state.Locks[0].IP != "127.0.0.1" || state.Locks[0].Failures != 10 {
		t.Fatalf("list: %+v", state)
	}
	until, err := time.Parse(time.RFC3339, *state.LockedUntil)
	if err != nil || time.Until(until) < 10*time.Minute || time.Until(until) > 16*time.Minute {
		t.Errorf("locked until %v %v", until, err)
	}
	_ = id
}

// Unlock ends the lock; the next sign-in works.
func TestUnlockEndsTheLock(t *testing.T) {
	w, _ := mailWorldEnv(t, true, lockEnv)
	admin := w.As("admin")
	id := makeAccount(t, w, admin, "erin", "")
	for i := 0; i < 10; i++ {
		signIn(w, "erin", "wrong-password")
	}
	expect(t, "unlock", admin.Do("POST", "/api/users/"+id+"/unlock", `{}`), 200, `{"success":true,"data":{"was_locked":true}}`)
	if got := signIn(w, "erin", "Old-Pass-123!"); got.Status != http.StatusOK {
		t.Errorf("still locked: %d %s", got.Status, got.Body)
	}
	if listedAs(t, admin, "erin").Locked {
		t.Error("the list still says locked")
	}
	expect(t, "not locked", admin.Do("POST", "/api/users/"+id+"/unlock", `{}`), 200, `{"success":true,"data":{"was_locked":false}}`)
	if rows := strings.Join(auditRowsAbout(t, w, id), "\n"); !strings.Contains(rows, "user_unlocked|Unlocked user: erin; cleared 10 failed sign-in(s)|1") {
		t.Errorf("audit:\n%s", rows)
	}
	expect(t, "viewer", w.As("viewer").Do("POST", "/api/users/"+id+"/unlock", `{}`), 403, "")
}
