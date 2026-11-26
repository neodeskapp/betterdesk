package impersonate_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The person viewed gets a mail in their own language: who, when, why.
// The setting turns it off.
func TestTheNoticeMail(t *testing.T) {
	w, box := newWorld(t, options{})
	exec(t, w, `UPDATE users SET email = 'olga@example.test', preferred_language = 'de' WHERE id = ?`, idOperator)
	exec(t, w, `UPDATE users SET first_name = 'Sam', last_name = 'Super' WHERE id = ?`, idSuper)
	// The reader's time zone gives the time, as in the other security mails
	if res := w.As("operator").Do("PUT", "/api/users/me/profile", `{"time_zone":"Europe/Berlin"}`); res.Status != 200 {
		t.Fatalf("time zone: %d %s", res.Status, res.Body)
	}
	a := w.As("super_admin")
	// The admin's request is in English; the mail follows the reader
	res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", idOperator), startBody("Ticket 4711: the device list is empty"), "Accept-Language", "en")
	if res.Status != 200 {
		t.Fatalf("start: %d %s", res.Status, res.Body)
	}
	m := waitForMails(t, box, 1)[0]
	if len(m.To) != 1 || m.To[0] != "olga@example.test" {
		t.Fatalf("to: %v", m.To)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	day := time.Now().In(berlin).Format("2006-01-02")
	if m.Subject != "Sam Super hat NeoDesk mit deinem Konto angesehen" {
		t.Errorf("subject: %q", m.Subject)
	}
	if m.Text == nil || !strings.Contains(*m.Text, "Sam Super hat NeoDesk am "+day) || !strings.Contains(*m.Text, " Europe/Berlin") || !strings.Contains(*m.Text, "Ticket 4711: the device list is empty") ||
		!strings.Contains(*m.Text, "https://desk.example/account") {
		t.Errorf("text: %v", textOf(m.Text))
	}
	if m.HTML == nil || !strings.Contains(*m.HTML, "Ticket 4711") {
		t.Error("no HTML part with the reason")
	}

	// Off: no mail
	a.Do("POST", "/api/impersonation/stop", "{}")
	admin := w.As("admin")
	if res := admin.Do("PUT", "/api/settings/impersonation", `{"notice_mail":false}`); res.Status != 200 || !strings.Contains(res.Body, `"notice_mail":false`) {
		t.Fatalf("setting: %d %s", res.Status, res.Body)
	}
	if res := admin.Do("PUT", "/api/settings/impersonation", `{"notice_mail":"no"}`); res.Status != 400 {
		t.Errorf("a setting that is no boolean: %d %s", res.Status, res.Body)
	}
	start(t, w, "super_admin", idOperator)
	time.Sleep(200 * time.Millisecond)
	if n := len(box.all()); n != 1 {
		t.Fatalf("%d mails with the notice off", n)
	}
	if res := w.As("operator").Do("GET", "/api/settings/impersonation", ""); res.Status != 403 {
		t.Errorf("the setting for an operator: %d", res.Status)
	}
}

func textOf(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// The person's own sign-in history shows the impersonation with who (by
// first and last name, as the activity list names people) and why; the
// admin's own history doesn't. The user page reads anyone's with user.view.
func TestSecurityEvents(t *testing.T) {
	w, _ := newWorld(t, options{})
	exec(t, w, `UPDATE users SET first_name = 'Sam', last_name = 'Super' WHERE id = ?`, idSuper)
	operator := w.As("operator")
	exec(t, w, `INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (?, 'login', 'User logged in', '203.0.113.7')`, idOperator)
	a := start(t, w, "super_admin", idOperator)
	// The operator's own history is not open to the impersonation
	if res := a.Do("GET", "/api/users/me/security-events", ""); res.Status != 403 {
		t.Errorf("own history from the impersonation: %d %s", res.Status, res.Body)
	}
	a.Do("POST", "/api/impersonation/stop", "{}")

	type event struct {
		Action string `json:"action"`
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
		IP     string `json:"ip"`
	}
	read := func(path string, as string) []event {
		t.Helper()
		agent := operator
		if as != "" {
			agent = w.As(as)
		}
		res := agent.Do("GET", path, "")
		if res.Status != 200 {
			t.Fatalf("%s: %d %s", path, res.Status, res.Body)
		}
		var out struct {
			Data struct {
				Events []event `json:"events"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data.Events
	}
	mine := read("/api/users/me/security-events", "")
	if len(mine) != 2 || mine[0] != (event{"impersonation_started", "Sam Super", "Reproduce the ticket 4711", ""}) ||
		mine[1] != (event{"login", "", "", "203.0.113.7"}) {
		t.Errorf("the operator's history: %+v", mine)
	}
	if got := read(fmt.Sprintf("/api/users/%d/security-events", idOperator), "admin"); len(got) != 2 {
		t.Errorf("on the user page: %+v", got)
	}
	for _, e := range read("/api/users/me/security-events", "super_admin") {
		if e.Action == "impersonation_started" {
			t.Errorf("the admin's own history shows the impersonation: %+v", e)
		}
	}
	if res := w.As("viewer").Do("GET", fmt.Sprintf("/api/users/%d/security-events", idOperator), ""); res.Status != 403 {
		t.Errorf("a viewer reads another's history: %d", res.Status)
	}
}
