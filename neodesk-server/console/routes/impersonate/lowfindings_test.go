package impersonate_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// An account whose end date passed can't sign in (authn.ended),
// and API tokens treat it as disabled, but until the sweep job (every five
// minutes) sets disabled, an admin could view the console as it (review
// of 26 Nov 2026). Now the start is refused.
func TestAnAccountPastItsEndDateCannotBeViewed(t *testing.T) {
	w, _ := newWorld(t, options{})
	exec(t, w, `CREATE TABLE IF NOT EXISTS account_lifecycle (user_id INTEGER PRIMARY KEY, expires_ms INTEGER, expiry_notice_ms INTEGER,
		expired_ms INTEGER, inactive_exempt INTEGER NOT NULL DEFAULT 0, active_from_ms INTEGER, inactive_notice_ms INTEGER)`)
	exec(t, w, `INSERT INTO account_lifecycle (user_id, expires_ms) VALUES (?, ?)`, idOperator, time.Now().Add(-time.Hour).UnixMilli())
	a := w.As("super_admin")
	res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("Reproduce the ticket 4711"))
	if res.Status == http.StatusOK {
		t.Errorf("start for an account past its end date: %d %s", res.Status, cut(res.Body))
	}
}

// The reason drops control characters (Cc), white space and format
// characters (Cf); before (review of 26 Nov 2026) it kept the Cf ones: a right-to-left override or zero-width
// characters go into the audit log, the person's sign-in history and the
// notice mail as they are, and can make the reason read differently from
// what it says.
func TestTheReasonLosesFormatCharacters(t *testing.T) {
	w, _ := newWorld(t, options{})
	a := w.As("super_admin")
	body, _ := json.Marshal(map[string]string{"password": adminPassword, "reason": "Ticket 4711 ‮txt.exe‬​"})
	if res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), string(body)); res.Status != http.StatusOK {
		t.Fatalf("start: %d %s", res.Status, res.Body)
	}
	got := strings.Join(auditAbout(t, w, idOperator), "\n")
	if strings.ContainsAny(got, "‮‬​") {
		t.Errorf("the audit entry keeps bidi and zero-width characters: %q", got)
	}
}

// Without a configured public address the notice mail's link
// (and the branding's pictures) are built from the Host header of the
// admin's start request, so the admin decided where the button in a mail
// from the server led the person (review of 26 Nov 2026). Now the mail has
// no link then.
func TestTheNoticeLinkNeverFollowsTheHostHeader(t *testing.T) {
	w, box := newWorld(t, options{env: map[string]string{"PANEL_PUBLIC_URL": ""}})
	exec(t, w, `UPDATE users SET email = 'olga@example.test' WHERE id = ?`, idOperator)
	a := w.As("super_admin")
	var tok map[string]string
	_ = json.Unmarshal([]byte(a.Do("GET", "/__test/token", "").Body), &tok)

	body, _ := json.Marshal(map[string]string{"password": adminPassword, "reason": "Reproduce the ticket 4711"})
	req, err := http.NewRequest("POST", "http://"+w.Addr()+fmt.Sprintf("/api/users/%d/impersonate", idOperator), strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "desk.evil.test"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", tok["token"])
	req.Header.Set("Cookie", sidCookie+"="+a.Cookie(sidCookie)+"; "+csrfCookies(a))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %s", res.StatusCode, raw)
	}
	m := waitForMails(t, box, 1)[0]
	if strings.Contains(textOf(m.Text), "desk.evil.test") || strings.Contains(textOf(m.HTML), "desk.evil.test") {
		t.Errorf("the notice links to the Host header: %q", textOf(m.Text))
	}
}

// csrfCookies are the browser's cookies but the session's, for a request
// the test sends itself.
func csrfCookies(a interface{ Cookie(string) string }) string {
	var out []string
	for _, name := range []string{"__csrf", "__csrf_h"} {
		if v := a.Cookie(name); v != "" {
			out = append(out, name+"="+v)
		}
	}
	return strings.Join(out, "; ")
}
