package commercialization_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/commercialization"
)

// clockWorld is the group with a .env file, a restart coordinator and a
// mail service of its own; docker sets the layout with two containers.
func clockWorld(t *testing.T, docker bool) (*proxytest.World, string, *restart.Service) {
	t.Helper()
	envPath := filepath.Join(t.TempDir(), ".env")
	var coordinator *restart.Service
	w := proxytest.NewWorld(t, func(d *kit.Deps) {
		d.Config = &config.Config{EnvPath: envPath, Docker: docker, Getenv: func(name string) string {
			if name == "BETTERDESK_DOCKER_LAYOUT" {
				return "split"
			}
			return ""
		}}
		coordinator = restart.New(restart.Options{DataDir: t.TempDir(), GoAPI: d.GoAPI})
		d.Restart = coordinator
		d.Mail = mail.New(mail.Options{DB: d.Store})
		commercialization.Register(d)
	})
	return w, envPath, coordinator
}

func account(id int64, role string) *proxytest.Account {
	a := &proxytest.Account{UserID: id}
	a.User.Username = "e2e_" + role
	a.User.Role = role
	return a
}

func put(path, body string, who *proxytest.Account) proxytest.Request {
	return proxytest.Request{Method: "PUT", Path: path, Session: who, Type: "application/json", Body: &body}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return out
}

// TestSaveClockSettings saves outside the Docker layout: the .env file
// gets the five keys, the answer has the shape of the old route, the
// change is in the operator's restart transaction with the settings from
// before as its rollback, and the audit log has the save.
func TestSaveClockSettings(t *testing.T) {
	w, envPath, coordinator := clockWorld(t, false)
	if err := os.WriteFile(envPath, []byte("PORT=5000\nBILLING_TIMEZONE=Europe/Berlin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := account(1, "admin")
	rec, calls := w.Do(put("/api/panel/billing/clock/settings", `{"ntp_servers":" a.example , b.example ","max_skew_ms":"2500ms","trust_os_ntp":0}`, admin), nil)
	if rec.Code != 200 || len(calls) != 0 {
		t.Fatalf("save: %d %s, calls %v", rec.Code, rec.Body, calls)
	}
	body := rec.Body.String()
	for _, part := range []string{
		`{"success":true,"settings":{"ntp_servers":"a.example,b.example","max_skew_ms":2500,"require_synced_clock":true,"trust_os_ntp":false,"billing_timezone":"UTC"},"serviceConfig":`,
		`,"status":null,"restart":null,"restartRequired":{"id":"`,
		`"changes":[{"key":"billing-clock","label":"commercialization.clock.title"}]`,
		`,"dockerMode":false}`,
	} {
		if !strings.Contains(body, part) {
			t.Errorf("the answer lacks %s:\n%s", part, body)
		}
	}
	content, _ := os.ReadFile(envPath)
	want := "PORT=5000\nBILLING_TIMEZONE=UTC\nNTP_SERVERS=a.example,b.example\nBILLING_MAX_CLOCK_SKEW_MS=2500\nBILLING_REQUIRE_SYNCED_CLOCK=1\nBILLING_TRUST_OS_NTP=0\n"
	if string(content) != want {
		t.Errorf("file\n%q\nwant\n%q", content, want)
	}
	state, _ := json.Marshal(coordinator.ReadState())
	if !strings.Contains(string(state), `"rollback":{"type":"billing-clock","settings":{"ntp_servers":"pool.ntp.org,time.google.com,time.cloudflare.com","max_skew_ms":2000,"require_synced_clock":true,"trust_os_ntp":`) ||
		!strings.Contains(string(state), `"billing_timezone":"Europe/Berlin"}}`) {
		t.Errorf("the transaction does not hold the settings from before: %s", state)
	}
	var n int
	if err := w.Deps.Store.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_log WHERE action = 'billing_clock_settings_updated'").Scan(&n); err != nil || n != 1 {
		t.Errorf("audit entries %d (%v)", n, err)
	}

	// Another operator's save is refused by the transaction before the
	// file is written, so the settings stay those the transaction can
	// undo (BUGS CM5); a refused value still answers 400 first
	other := account(2, "super_admin")
	rec, _ = w.Do(put("/api/panel/billing/clock/settings", `{"ntp_servers":"c.example"}`, other), nil)
	if rec.Code != 409 || rec.Body.String() != `{"success":false,"error":"restart_transaction_owned"}` {
		t.Errorf("a second operator: %d %s", rec.Code, rec.Body)
	}
	rec, _ = w.Do(put("/api/panel/billing/clock/settings", `{"ntp_servers":"bad host"}`, other), nil)
	if rec.Code != 400 || rec.Body.String() != `{"success":false,"error":"invalid_ntp_servers"}` {
		t.Errorf("a second operator's refused value: %d %s", rec.Code, rec.Body)
	}
	if content, _ := os.ReadFile(envPath); string(content) != want {
		t.Errorf("a refused save wrote the file:\n%s", content)
	}
	if after, _ := json.Marshal(coordinator.ReadState()); string(after) != string(state) {
		t.Errorf("a refused save changed the transaction:\n%s\nwas\n%s", after, state)
	}
}

// TestSaveClockSettingsRefusals: a refused value answers 400 and writes
// nothing; a role without server.config gets the guard's answer.
func TestSaveClockSettingsRefusals(t *testing.T) {
	w, envPath, coordinator := clockWorld(t, false)
	for body, want := range map[string]string{
		`{"ntp_servers":"bad host"}`:          `{"success":false,"error":"invalid_ntp_servers"}`,
		`{"max_skew_ms":600001}`:              `{"success":false,"error":"invalid_max_skew"}`,
		`{"billing_timezone":"Mars/Base"}`:    `{"success":false,"error":"invalid_billing_timezone"}`,
		`{"max_skew_ms":{"toString":1}}`:      `{"success":false,"error":"Cannot convert object to primitive value"}`,
		`{"billing_timezone":{"toString":1}}`: `{"success":false,"error":"Cannot convert object to primitive value"}`,
		// A body with none of the settings would save the defaults (BUGS C6)
		`{}`:          `{"success":false,"error":"No settings to save"}`,
		`{"other":1}`: `{"success":false,"error":"No settings to save"}`,
		`["UTC"]`:     `{"success":false,"error":"No settings to save"}`,
	} {
		rec, _ := w.Do(put("/api/panel/billing/clock/settings", body, account(1, "admin")), nil)
		status := 400
		if strings.Contains(body, "toString") {
			status = 500
		}
		if rec.Code != status || rec.Body.String() != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec, _ := w.Do(put("/api/panel/billing/clock/settings", `{}`, account(5, "operator")), nil)
	if rec.Code != 403 {
		t.Errorf("operator: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(envPath); err == nil {
		t.Error("a refused save wrote the file")
	}
	if coordinator.ReadState() != nil {
		t.Error("a refused save started a transaction")
	}
}

// TestSaveClockSettingsInDocker saves in the layout with two containers:
// the settings go to the API server, the answer has its status and no
// restart, and a failed restart of an earlier clock change is dismissed.
func TestSaveClockSettingsInDocker(t *testing.T) {
	w, envPath, coordinator := clockWorld(t, true)
	admin := account(1, "admin")
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ntp_servers":"pool.ntp.org","max_skew_ms":2000,"require_synced_clock":true,"trust_os_ntp":true,"config":{"ntp_servers":"x.example","max_skew_ms":3500},"status":{"synced":true}}`}
	rec, calls := w.Do(put("/api/panel/billing/clock/settings", `{"ntp_servers":"x.example","max_skew_ms":3500}`, admin), ok)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	want := `{"success":true,"settings":{"ntp_servers":"x.example","max_skew_ms":3500,"require_synced_clock":true,"trust_os_ntp":true,"billing_timezone":"UTC"},"serviceConfig":null,"status":{"synced":true},"restart":null,"restartRequired":null,"dockerMode":true}`
	if rec.Body.String() != want {
		t.Errorf("answer\n%s\nwant\n%s", rec.Body, want)
	}
	if len(calls) != 2 || calls[0].Method != "GET" || calls[1].Method != "PUT" || calls[1].URL != "/api/timesync/config" ||
		calls[1].Body != `{"ntp_servers":"x.example","max_skew_ms":3500,"require_synced_clock":true,"trust_os_ntp":true,"billing_timezone":"UTC"}` {
		t.Errorf("calls %+v", calls)
	}
	if content, _ := os.ReadFile(envPath); !strings.Contains(string(content), "NTP_SERVERS=x.example") {
		t.Errorf("the save did not also write the file:\n%s", content)
	}

	// A failed transaction with a clock change is removed by the next save
	if _, err := coordinator.RegisterChange(1, restart.Change{Key: "billing-clock", Rollback: nil}); err == nil {
		t.Fatal("a change without rollback was taken")
	}
	refused := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"error":"max_skew_ms must be between 1 and 600000"}`}
	rec, _ = w.Do(put("/api/panel/billing/clock/settings", `{"trust_os_ntp":true}`, admin), refused)
	if rec.Code != 500 || rec.Body.String() != `{"success":false,"error":"max_skew_ms must be between 1 and 600000"}` {
		t.Errorf("an answer with an error: %d %s", rec.Code, rec.Body)
	}
	rec, _ = w.Do(proxytest.Request{Path: "/api/panel/billing/clock/settings", Session: admin}, ok)
	if !strings.Contains(rec.Body.String(), `"dockerMode":true`) {
		t.Errorf("read: %s", rec.Body)
	}
}

// TestEmailConfig reads, saves and reads back the help request mail
// settings: only a false switches off, the row holds the JSON of the old
// console, and the save is audited.
func TestEmailConfig(t *testing.T) {
	w, _, _ := clockWorld(t, false)
	admin := account(1, "admin")
	rec, _ := w.Do(proxytest.Request{Path: "/api/panel/commercialization/email-config", Session: admin}, nil)
	if rec.Body.String() != `{"success":true,"config":{"help_requests_enabled":true,"notify_assigned_operators":true,"fallback_alert_email":true,"include_folder_in_subject":true}}` {
		t.Errorf("first read: %s", rec.Body)
	}
	rec, _ = w.Do(put("/api/panel/commercialization/email-config", `{"help_requests_enabled":false,"notify_assigned_operators":0,"include_folder_in_subject":false}`, admin), nil)
	want := `{"success":true,"config":{"help_requests_enabled":false,"notify_assigned_operators":true,"fallback_alert_email":true,"include_folder_in_subject":false}}`
	if rec.Body.String() != want {
		t.Errorf("save: %s", rec.Body)
	}
	var stored string
	if err := w.Deps.Store.QueryRow(context.Background(), "SELECT value FROM settings WHERE key = 'commercialization_email_config'").Scan(&stored); err != nil ||
		stored != `{"help_requests_enabled":false,"notify_assigned_operators":true,"fallback_alert_email":true,"include_folder_in_subject":false}` {
		t.Errorf("stored %q (%v)", stored, err)
	}
	rec, _ = w.Do(proxytest.Request{Path: "/api/panel/commercialization/email-config", Session: admin}, nil)
	if rec.Body.String() != want {
		t.Errorf("read back: %s", rec.Body)
	}
	var details string
	if err := w.Deps.Store.QueryRow(context.Background(), "SELECT details FROM audit_log WHERE action = 'commercialization_email_config_updated'").Scan(&details); err != nil ||
		details != "Updated commercialization email notifications" {
		t.Errorf("audit entry %q (%v)", details, err)
	}
	rec, _ = w.Do(put("/api/panel/commercialization/email-config", `{}`, account(5, "operator")), nil)
	if rec.Code != 403 {
		t.Errorf("operator saves: %d", rec.Code)
	}

	// A body with none of the switches would switch every one on (BUGS C6)
	for _, body := range []string{`{}`, `{"other":false}`, `[false,false]`} {
		rec, _ = w.Do(put("/api/panel/commercialization/email-config", body, admin), nil)
		if rec.Code != 400 || rec.Body.String() != `{"success":false,"error":"No settings to save"}` {
			t.Errorf("save %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec, _ = w.Do(proxytest.Request{Path: "/api/panel/commercialization/email-config", Session: admin}, nil)
	if rec.Body.String() != want {
		t.Errorf("read after the refused saves: %s", rec.Body)
	}
}

// TestSMTPStatus reads the SMTP settings for the billing page, with the
// password masked.
func TestSMTPStatus(t *testing.T) {
	w, _, _ := clockWorld(t, false)
	operator := account(5, "operator")
	rec, _ := w.Do(proxytest.Request{Path: "/api/panel/commercialization/smtp-status", Session: operator}, nil)
	if rec.Body.String() != `{"configured":false}` {
		t.Errorf("without settings: %s", rec.Body)
	}
	if _, err := w.Deps.Store.Exec(context.Background(), "INSERT INTO settings (key, value) VALUES ('smtp_config', ?)",
		`{"host":"mail.example","port":25,"user":"u","pass":"secret-pass","alert_email":"ops@example.com"}`); err != nil {
		t.Fatal(err)
	}
	rec, _ = w.Do(proxytest.Request{Path: "/api/panel/commercialization/smtp-status", Session: operator}, nil)
	if body := rec.Body.String(); strings.Contains(body, "secret-pass") || !strings.Contains(body, `"pass":"********"`) {
		t.Errorf("with settings: %s", body)
	}
	rec, _ = w.Do(proxytest.Request{Path: "/api/panel/commercialization/smtp-status", Session: account(6, "viewer")}, nil)
	if rec.Code != 403 {
		t.Errorf("viewer: %d", rec.Code)
	}
}

// TestClockReadsWithEitherPermission pins the guard of the three clock
// reads: server.config or billing.view.
func TestClockReadsWithEitherPermission(t *testing.T) {
	w, _, _ := clockWorld(t, false)
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"synced":true}`}
	for _, path := range []string{"/api/panel/billing/clock/settings", "/api/panel/billing/clock/detect-timezone", "/api/panel/billing/timesync/status"} {
		ids := map[string]int64{"admin": 1, "server_admin": 3, "global_admin": 4, "operator": 5, "viewer": 6}
		for role, status := range map[string]int{"server_admin": 200, "operator": 200, "global_admin": 200, "admin": 200, "viewer": 403} {
			rec, _ := w.Do(proxytest.Request{Path: path, Session: account(ids[role], role)}, ok)
			if rec.Code != status {
				t.Errorf("%s as %s: %d %s", path, role, rec.Code, rec.Body)
			}
		}
		rec, _ := w.Do(proxytest.Request{Path: path, Session: account(6, "viewer")}, ok)
		if !strings.Contains(rec.Body.String(), "billing.view") {
			t.Errorf("%s: the refusal names %s", path, rec.Body)
		}
	}
	d := decode(t, func() string {
		rec, _ := w.Do(proxytest.Request{Path: "/api/panel/billing/clock/detect-timezone", Session: account(1, "admin")}, nil)
		return rec.Body.String()
	}())
	if d["common"].([]any)[14] != "Europe/Kyiv" || d["timezone"] == "" {
		t.Errorf("detect: %v", d)
	}
}
