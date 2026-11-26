package mail

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// newTestService returns a service on db with a recording transport and
// log, and the environment env.
func newTestService(db *store.DB, env map[string]string, opts Options) (*Service, *fakeTransport, *logLines) {
	transport, log := &fakeTransport{}, &logLines{}
	opts.DB, opts.Transport, opts.Logger = db, transport, log
	opts.Getenv = func(name string) string { return env[name] }
	return New(opts), transport, log
}

func storeConfig(t *testing.T, db *store.DB, text string) {
	t.Helper()
	if err := setSetting(context.Background(), db, settingKey, text); err != nil {
		t.Fatal(err)
	}
}

func stored(t *testing.T, db *store.DB) string {
	t.Helper()
	text, _, err := getSetting(context.Background(), db, settingKey)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

const fullConfig = `{"host":"smtp.example.com","port":587,"secure":false,"user":"user","pass":"s3cret-Pa55","from":"from@example.com","alert_email":"ops@example.com"}`

// ---- the Jest tests of tests/smtpSettings.test.js ----

func TestGetAnswersNotConfiguredWithoutSMTP(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s, _, _ := newTestService(db, nil, Options{})
		answer := s.GetSMTPSettings(context.Background())
		if answer.Status != 200 || marshal(t, answer.Body) != `{"configured":false}` {
			t.Fatalf("%d %s", answer.Status, marshal(t, answer.Body))
		}
	})
}

func TestGetMasksThePassword(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		storeConfig(t, db, fullConfig)
		s, _, _ := newTestService(db, nil, Options{})
		answer := s.GetSMTPSettings(context.Background())
		want := `{"configured":true,"host":"smtp.example.com","port":587,"secure":false,"user":"user","pass":"********","from":"from@example.com","alert_email":"ops@example.com"}`
		if got := marshal(t, answer.Body); answer.Status != 200 || got != want {
			t.Fatalf("%d %s", answer.Status, got)
		}
	})
}

// The password is in no answer, whatever its type and wherever it comes
// from: every stored form of it reads as "********" or "".
func TestThePasswordIsNeverInAnAnswer(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, pass := range []string{`"s3cret-Pa55"`, `5551234`, `["s3cret-Pa55"]`, `{"k":"s3cret-Pa55"}`, `true`, `"********"`, `""`, `0`, `null`, `false`} {
		storeConfig(t, db, `{"host":"smtp.example.com","pass":`+pass+`}`)
		s, transport, log := newTestService(db, nil, Options{})
		wantPass := `"********"`
		if pass == `""` || pass == `0` || pass == `null` || pass == `false` {
			wantPass = `""`
		}
		got := marshal(t, s.GetSMTPSettings(ctx).Body)
		if !strings.Contains(got, `"pass":`+wantPass+`,`) || strings.Contains(got, "s3cret") || strings.Contains(got, "5551234") {
			t.Fatalf("pass %s: answer %s", pass, got)
		}
		// Neither the connection test nor a mail puts it in an answer or the log
		transport.verifyErr = errors.New("Invalid login")
		test := marshal(t, s.TestSMTPSettings(ctx).Body)
		text := "body"
		_, _ = s.SendEmail(ctx, Email{To: []string{"someone@example.org"}, Subject: "subject", Text: &text})
		for _, line := range log.lines {
			if strings.Contains(line[1], "s3cret") || strings.Contains(line[1], "5551234") {
				t.Fatalf("pass %s: log line %q", pass, line)
			}
		}
		if strings.Contains(test, "s3cret") || strings.Contains(test, "5551234") {
			t.Fatalf("pass %s: test answer %s", pass, test)
		}
	}
	// The same from the environment
	s, _, _ := newTestService(db, map[string]string{"SMTP_HOST": "env.example.org", "SMTP_USER": "u", "SMTP_PASS": "s3cret-Pa55"}, Options{})
	storeConfig(t, db, "")
	if got := marshal(t, s.GetSMTPSettings(ctx).Body); !strings.Contains(got, `"pass":"********"`) || strings.Contains(got, "s3cret") {
		t.Fatalf("environment: %s", got)
	}
}

func TestPutKeepsTheStoredPasswordForTheMask(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		storeConfig(t, db, `{"host":"smtp.example.com","port":587,"secure":false,"user":"user","pass":"existing-secret","from":"from@example.com"}`)
		s, _, _ := newTestService(db, nil, Options{})
		answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"smtp.example.com","port":587,"secure":false,"user":"user","pass":"********","from":"from@example.com","alert_email":"ops@example.com"}`))
		if answer.Status != 200 || marshal(t, answer.Body) != `{"success":true}` {
			t.Fatalf("%d %s", answer.Status, marshal(t, answer.Body))
		}
		want := `{"host":"smtp.example.com","port":587,"secure":false,"user":"user","pass":"existing-secret","from":"from@example.com","alert_email":"ops@example.com"}`
		if got := stored(t, db); got != want {
			t.Fatalf("stored %s", got)
		}
		if answer.Audit == nil || *answer.Audit != (AuditEntry{Action: "smtp_config_updated", Details: "SMTP: smtp.example.com:587"}) {
			t.Fatalf("audit entry %+v", answer.Audit)
		}
		// No password at all keeps it too; an empty one removes it (BUGS
		// RV40); a new one replaces it; the mask with nothing stored is no
		// password.
		for _, step := range []struct{ body, want string }{
			{`{"host":"smtp.example.com","user":"user","from":"f@example.com"}`, `"pass":"existing-secret"`},
			{`{"host":"smtp.example.com","user":"user","from":"f@example.com","pass":null}`, `"pass":"existing-secret"`},
			{`{"host":"smtp.example.com","user":"user","from":"f@example.com","pass":""}`, `"pass":""`},
			{`{"host":"smtp.example.com","user":"user","from":"f@example.com","pass":"********"}`, `"pass":""`},
			{`{"host":"h","from":"f@example.com","pass":"new-secret"}`, `"pass":"new-secret"`},
			{`{"host":"h","from":"f@example.com","pass":"********"}`, `"pass":"new-secret"`},
			{`{"host":"h","from":"f@example.com","pass":" ********"}`, `"pass":" ********"`},
		} {
			if answer := s.PutSMTPSettings(ctx, bodyOf(step.body)); answer.Status != 200 {
				t.Fatalf("%s: %d", step.body, answer.Status)
			}
			if got := stored(t, db); !strings.Contains(got, step.want) {
				t.Fatalf("%s: stored %s, want %s", step.body, got, step.want)
			}
		}
		if _, err := db.Exec(ctx, "DELETE FROM settings WHERE key = ?", settingKey); err != nil {
			t.Fatal(err)
		}
		s.PutSMTPSettings(ctx, bodyOf(`{"host":"h","from":"f@example.com","pass":"********"}`))
		if got := stored(t, db); !strings.Contains(got, `"pass":""`) {
			t.Fatalf("mask without a stored password: %s", got)
		}
	})
}

func TestPutNeedsAHost(t *testing.T) {
	db := storetest.Migrated(t)
	s, _, _ := newTestService(db, nil, Options{})
	for _, body := range []string{`{}`, `{"host":""}`, `{"host":null}`, `{"host":0}`, `{"host":false}`, `[]`, `{"port":25,"pass":"x"}`} {
		answer := s.PutSMTPSettings(context.Background(), bodyOf(body))
		if answer.Status != 400 || marshal(t, answer.Body) != `{"error":"SMTP host is required"}` || answer.Audit != nil {
			t.Fatalf("%s: %d %s %+v", body, answer.Status, marshal(t, answer.Body), answer.Audit)
		}
	}
	if stored(t, db) != "" {
		t.Fatalf("a refused request stored %q", stored(t, db))
	}
	// No body at all is the empty object
	if answer := s.PutSMTPSettings(context.Background(), middleware.JSONObject{}); answer.Status != 400 {
		t.Fatalf("no body: %d", answer.Status)
	}
}

// A sender that is no address was saved, and the next mail went out
// without a From line (BUGS L406)
func TestPutRefusesASenderThatIsNoAddress(t *testing.T) {
	db := storetest.Migrated(t)
	s, _, _ := newTestService(db, nil, Options{})
	for _, from := range []string{"not an address", "neodesk@", "@example.org", "NeoDesk <neodesk>", "a@b, c@d"} {
		body := `{"host":"smtp.example.org","from":` + marshal(t, from) + `}`
		answer := s.PutSMTPSettings(context.Background(), bodyOf(body))
		if answer.Status != 400 || marshal(t, answer.Body) != `{"error":"Invalid from"}` {
			t.Errorf("%q: %d %s", from, answer.Status, marshal(t, answer.Body))
		}
	}
	if stored(t, db) != "" {
		t.Fatalf("a refused request stored %q", stored(t, db))
	}
	for _, from := range []string{"neodesk@example.org", "NeoDesk <neodesk@example.org>", "neodesk@localhost"} {
		body := `{"host":"smtp.example.org","from":` + marshal(t, from) + `}`
		if answer := s.PutSMTPSettings(context.Background(), bodyOf(body)); answer.Status != 200 {
			t.Errorf("%q: %d %s", from, answer.Status, marshal(t, answer.Body))
		}
	}
}

func TestConnectionTestAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, transport, _ := newTestService(db, nil, Options{})
		if got := marshal(t, s.TestSMTPSettings(ctx).Body); got != `{"success":false,"error":"No SMTP configuration"}` {
			t.Fatalf("without a configuration: %s", got)
		}
		storeConfig(t, db, fullConfig)
		if answer := s.TestSMTPSettings(ctx); answer.Status != 200 || marshal(t, answer.Body) != `{"success":true}` {
			t.Fatalf("%d %s", answer.Status, marshal(t, answer.Body))
		}
		if len(transport.verifies) != 1 || transport.verifies[0] != (Settings{Host: "smtp.example.com", Port: 587, User: "user", Pass: "s3cret-Pa55"}) {
			t.Fatalf("verified with %+v", transport.verifies)
		}
		transport.verifyErr = errors.New(`Invalid login: 535 "bad" <credentials>`)
		if got := marshal(t, s.TestSMTPSettings(ctx).Body); got != `{"success":false,"error":"Invalid login: 535 \"bad\" <credentials>"}` {
			t.Fatalf("failure: %s", got)
		}
	})
}

// ---- security decisions ----

// The certificate of the mail server is checked unless the configuration
// says false or the console's default (SMTP_TLS_VERIFY) is off. A stored
// tlsVerify that is no flag counts as not set (BUGS EM1).
func TestTLSVerification(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, c := range []struct {
		stored     string
		byDefault  bool
		wantVerify bool
	}{
		{`{"host":"h"}`, false, false},
		{`{"host":"h"}`, true, true},
		{`{"host":"h","tlsVerify":true}`, false, true},
		{`{"host":"h","tlsVerify":false}`, true, false},
		{`{"host":"h","tlsVerify":null}`, true, true},
		{`{"host":"h","tlsVerify":null}`, false, false},
		{`{"host":"h","tlsVerify":"false"}`, true, true},
		{`{"host":"h","tlsVerify":"true"}`, false, false},
		{`{"host":"h","tlsVerify":0}`, true, true},
		{`{"host":"h","tlsVerify":1}`, false, false},
	} {
		storeConfig(t, db, c.stored)
		s, transport, _ := newTestService(db, nil, Options{TLSVerify: c.byDefault})
		if _, err := s.TestConnection(ctx); err != nil {
			t.Fatal(err)
		}
		if got := transport.verifies[0].TLSVerify; got != c.wantVerify {
			t.Errorf("%s with the default %v: verify %v", c.stored, c.byDefault, got)
		}
		// The settings page sees a stored flag, and nothing for the default
		answer := marshal(t, s.GetSMTPSettings(ctx).Body)
		want := map[string]string{"true": `"tlsVerify":true`, "false": `"tlsVerify":false`}[strings.TrimSuffix(strings.TrimPrefix(c.stored, `{"host":"h","tlsVerify":`), "}")]
		if want == "" && strings.Contains(answer, "tlsVerify") || want != "" && !strings.HasSuffix(answer, ","+want+"}") {
			t.Errorf("%s: the settings answer %s", c.stored, answer)
		}
	}
	// The environment's configuration takes the default
	storeConfig(t, db, "")
	for _, byDefault := range []bool{false, true} {
		s, transport, _ := newTestService(db, map[string]string{"SMTP_HOST": "env.example.org"}, Options{TLSVerify: byDefault})
		_, _ = s.TestConnection(ctx)
		if transport.verifies[0].TLSVerify != byDefault {
			t.Errorf("environment with the default %v: verify %v", byDefault, transport.verifies[0].TLSVerify)
		}
	}
}

// Every value an alert mail or a help request mail prints into its HTML is
// escaped: also the severity, the device and the time of an alert, which
// a rule's update and a device's token can set (BUGS EM3).
func TestMailsEscapeWhatTheyPrint(t *testing.T) {
	hostile := `<script>alert("x")</script>&`
	escaped := `&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;&amp;`
	// The branded layout escapes as html.EscapeString does: quotes as &#34;
	escaped = `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;&amp;`
	words, brand := New(Options{}).words(), brandmail.Branding{Server: hostile}
	_, _, html, err := alertMail(
		Alert{Severity: jsbind.String(hostile), DeviceID: jsbind.String(hostile), Message: jsbind.String(hostile), TriggeredAt: jsbind.String(hostile)},
		AlertRule{Name: jsbind.String(hostile), Description: jsbind.String(hostile), ConditionValue: jsbind.Number(1)}, words, brand)
	if err != nil {
		t.Fatal(err)
	}
	// The title and the heading name the rule; severity, device, message and
	// time are facts, the description a line below them
	if strings.Count(html, escaped) < 7 || strings.Contains(html, "<script>") {
		t.Fatalf("alert mail: %s", html)
	}
	_, _, html, err = helpRequestMail(HelpRequest{Hostname: jsbind.String(hostile), DeviceID: jsbind.String(hostile), Message: jsbind.String(hostile)},
		jsbind.String(hostile), `https://panel.example.org/?a="b"&c=<d>`, words, brand)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(html, escaped) < 6 || strings.Contains(html, "<script>") ||
		!strings.Contains(html, `href="https://panel.example.org/?a=&#34;b&#34;&amp;c=&lt;d&gt;/help-requests"`) {
		t.Fatalf("help request mail: %s", html)
	}
}

// The log names how many recipients a mail had, never who they were, and
// nothing of the message.
func TestTheLogNamesNoAddressAndNoBody(t *testing.T) {
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, log := newTestService(db, nil, Options{})
	body := "the body of the message"
	ok, err := s.SendEmail(context.Background(), Email{To: []string{"alice@example.org, bob@example.org"}, Subject: "the subject", Text: &body, HTML: &body})
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	transport.sendErr = errors.New("connect ECONNREFUSED 10.0.0.1:587")
	if ok, _ := s.SendEmail(context.Background(), Email{To: []string{"carol@example.org"}, ToList: true, Subject: "the subject", Text: &body}); ok {
		t.Fatal("a failed mail counts as sent")
	}
	want := [][2]string{
		{"info", "SMTP transporter configured: smtp.example.com:587"},
		{"info", "Sent message to 2 recipient(s)"},
		{"error", "Send failed: connect ECONNREFUSED 10.0.0.1:587"},
	}
	if !sameLogs(log.lines, want) {
		t.Fatalf("log %q", log.lines)
	}
	for _, line := range log.lines {
		for _, secret := range []string{"alice", "bob", "carol", "the body", "the subject", "s3cret", "from@example.com"} {
			if strings.Contains(line[1], secret) {
				t.Fatalf("log line %q has %q", line[1], secret)
			}
		}
	}
}

func TestWithoutATransportNothingIsSent(t *testing.T) {
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	log := &logLines{}
	s := New(Options{DB: db, Logger: log})
	text := "x"
	if ok, err := s.SendEmail(context.Background(), Email{To: []string{"a@example.org"}, Text: &text}); ok || err != nil {
		t.Fatal(ok, err)
	}
	if got := marshal(t, s.TestSMTPSettings(context.Background()).Body); got != `{"success":false,"error":"No SMTP configuration"}` {
		t.Fatal(got)
	}
	// The settings page still shows the configuration
	if got := marshal(t, s.GetSMTPSettings(context.Background()).Body); !strings.Contains(got, `"configured":true`) {
		t.Fatal(got)
	}
}

// A database that fails: reading falls back on the environment, saving
// answers 500 and stores nothing.
func TestAFailingDatabase(t *testing.T) {
	db := storetest.Migrated(t)
	s, _, _ := newTestService(db, map[string]string{"SMTP_HOST": "env.example.org"}, Options{})
	if _, err := db.Exec(context.Background(), "DROP TABLE settings"); err != nil {
		t.Fatal(err)
	}
	cfg := s.LoadSMTPConfig(context.Background())
	if cfg == nil || cfg.get("host").str != "env.example.org" {
		t.Fatalf("configuration %+v", cfg)
	}
	answer := s.PutSMTPSettings(context.Background(), bodyOf(`{"host":"h"}`))
	if answer.Status != 500 || marshal(t, answer.Body) != `{"error":"Internal server error"}` || answer.Audit != nil ||
		!strings.HasPrefix(answer.LogError, "SMTP update error: ") {
		t.Fatalf("%d %s %+v %q", answer.Status, marshal(t, answer.Body), answer.Audit, answer.LogError)
	}
	if _, err := HasEmailNotificationSent(context.Background(), db, "a", "b", "c"); err != nil {
		t.Fatalf("the notification log is another table: %v", err)
	}
}

// ---- how the help request service uses the mail service ----
// (tests/helpRequestEmailService.test.js)

func TestHelpRequestNotification(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		storeConfig(t, db, strings.Replace(fullConfig, `"ops@example.com"`, `"  ops@example.com "`, 1))
		s, transport, _ := newTestService(db, map[string]string{"PANEL_PUBLIC_URL": "https://panel.example.org/"}, Options{})

		// The address of last resort
		if got, err := s.GetAlertEmail(ctx); got != "ops@example.com" || err != nil {
			t.Fatalf("alert address %q %v", got, err)
		}
		// One mail per recipient, noted in the log of notifications
		sent, err := s.HasEmailNotificationSent(ctx, "help_request", "42", "alice@example.com")
		if sent || err != nil {
			t.Fatal(sent, err)
		}
		ok, err := s.SendHelpRequestEmail(ctx, HelpRequestEmail{
			To:          []string{"alice@example.com"},
			HelpRequest: HelpRequest{Hostname: jsbind.String("PC-01"), DeviceID: jsbind.String("dev-1"), Message: jsbind.String("Need help")},
			FolderName:  jsbind.String("Support"),
		})
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
		m := transport.sends[0].mail
		// The heading, the lead, the facts and the link, as the branded page has them (EM10)
		wantText := "Help request from PC-01\n\nA new help request was submitted.\n\nDevice: PC-01\nDevice ID: dev-1\nFolder: Support\nMessage: Need help\n\nOpen help requests in NeoDesk: https://panel.example.org/help-requests\n"
		if m.Subject != "[Support] Help request — PC-01" || !sameStrings(m.To, []string{"alice@example.com"}) || m.ToList || *m.Text != wantText ||
			m.From != "from@example.com" || !strings.Contains(*m.HTML, `href="https://panel.example.org/help-requests"`) {
			t.Fatalf("mail %s", describe(m))
		}
		// Without a device id and a folder only their lines go
		if ok, err := s.SendHelpRequestEmail(ctx, HelpRequestEmail{To: []string{"alice@example.com"}, HelpRequest: HelpRequest{Hostname: jsbind.String("PC-02")}}); !ok || err != nil {
			t.Fatal(ok, err)
		}
		if got, want := *transport.sends[1].mail.Text, "Help request from PC-02\n\nA new help request was submitted.\n\nDevice: PC-02\nMessage: \n\nOpen help requests in NeoDesk: https://panel.example.org/help-requests\n"; got != want {
			t.Fatalf("text %q, want %q", got, want)
		}
		for range 2 {
			if err := s.LogEmailNotificationSent(ctx, "help_request", "42", "alice@example.com"); err != nil {
				t.Fatal(err)
			}
		}
		if sent, _ := s.HasEmailNotificationSent(ctx, "help_request", "42", "alice@example.com"); !sent {
			t.Fatal("the notification is not in the log")
		}
		for _, other := range [][3]string{{"help_request", "43", "alice@example.com"}, {"help_request", "42", "bob@example.com"}, {"other", "42", "alice@example.com"},
			{"help_request", "42", "Alice@example.com"}} {
			if sent, _ := s.HasEmailNotificationSent(ctx, other[0], other[1], other[2]); sent {
				t.Fatalf("%q counts as sent", other)
			}
		}
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM email_notification_log").Scan(&n); err != nil || n != 1 {
			t.Fatalf("%d rows %v", n, err)
		}
		// A mail that fails is not sent
		transport.sendErr = errors.New("boom")
		if ok, _ := s.SendHelpRequestEmail(ctx, HelpRequestEmail{To: []string{"bob@example.com"}}); ok {
			t.Fatal("a failed mail counts as sent")
		}
	})
}

// ---- recordings beside the main one ----

func TestUpperCaseAsNode(t *testing.T) {
	var misc struct {
		Upper      [][2]json.RawMessage `json:"upper"`
		UpperWords [][2]string          `json:"upperWords"`
	}
	readGzip(t, "misc.json.gz", &misc)
	changed := map[rune]string{}
	for _, pair := range misc.Upper {
		var cp int
		var to string
		if json.Unmarshal(pair[0], &cp) != nil || json.Unmarshal(pair[1], &to) != nil {
			t.Fatal("bad pair")
		}
		changed[rune(cp)] = to
	}
	if len(changed) < 1400 {
		t.Fatalf("%d code points", len(changed))
	}
	wrong := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		want, ok := changed[cp]
		if !ok {
			want = string(cp)
		}
		if got := upper(string(cp)); got != want {
			wrong++
			if wrong < 20 {
				t.Errorf("upper(U+%04X) = %q, Node: %q", cp, got, want)
			}
		}
	}
	for _, word := range misc.UpperWords {
		if got := upper(word[0]); got != word[1] {
			t.Errorf("upper(%q) = %q, Node: %q", word[0], got, word[1])
		}
	}
}

func TestFlagsAsNodeConfig(t *testing.T) {
	var misc struct {
		Flags []struct {
			Env                  map[string]string `json:"env"`
			SMTPTLSVerify        bool              `json:"smtpTlsVerify"`
			HTTPRedirect         bool              `json:"httpRedirect"`
			AllowSelfSignedCerts bool              `json:"allowSelfSignedCerts"`
			Host                 string            `json:"host"`
			Port                 string            `json:"port"`
		} `json:"flags"`
	}
	readGzip(t, "misc.json.gz", &misc)
	if len(misc.Flags) < 190 {
		t.Fatalf("%d sets", len(misc.Flags))
	}
	verifyOn, selfSignedApart := 0, 0
	for _, f := range misc.Flags {
		if got := ParseFlag(f.Env["SMTP_TLS_VERIFY"], "false"); got != f.SMTPTLSVerify {
			t.Errorf("SMTP_TLS_VERIFY=%q: %v, Node: %v", f.Env["SMTP_TLS_VERIFY"], got, f.SMTPTLSVerify)
		}
		if got := ParseFlag(f.Env["HTTP_REDIRECT_HTTPS"], "true"); got != f.HTTPRedirect {
			t.Errorf("HTTP_REDIRECT_HTTPS=%q: %v, Node: %v", f.Env["HTTP_REDIRECT_HTTPS"], got, f.HTTPRedirect)
		}
		if f.SMTPTLSVerify {
			verifyOn++
		}
		// ALLOW_SELF_SIGNED_CERTS is a setting of its own: mail does not
		// follow it.
		if f.AllowSelfSignedCerts == f.SMTPTLSVerify {
			selfSignedApart++
		}
		// The host and port the panel's address is made of
		host := f.Env["HOST"]
		if host == "" {
			host = "127.0.0.1"
		}
		port := parseInt10(f.Env["PORT"])
		if port == 0 || math.IsNaN(port) {
			port = 5000
		}
		if host != f.Host || jsbind.NumberString(port) != f.Port {
			t.Errorf("HOST=%q PORT=%q: %q %v, Node: %q %s", f.Env["HOST"], f.Env["PORT"], host, port, f.Host, f.Port)
		}
	}
	if verifyOn < 5 || selfSignedApart < 5 {
		t.Fatalf("verify on in %d sets, both settings set alike in %d", verifyOn, selfSignedApart)
	}
}

// ---- the transporter the service remembers ----

func TestResetTransporter(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	storeConfig(t, db, fullConfig)
	s, _, log := newTestService(db, nil, Options{})
	configured := func() int {
		n := 0
		for _, line := range log.lines {
			if strings.HasPrefix(line[1], "SMTP transporter configured: ") {
				n++
			}
		}
		return n
	}
	for range 3 {
		_, _ = s.TestConnection(ctx)
	}
	if configured() != 1 {
		t.Fatalf("announced %d times for one configuration", configured())
	}
	s.ResetTransporter()
	_, _ = s.TestConnection(ctx)
	if configured() != 2 {
		t.Fatalf("announced %d times after a reset", configured())
	}
	// Another configuration is announced without a reset, and a save
	// resets
	storeConfig(t, db, strings.Replace(fullConfig, "587", "2525", 1))
	_, _ = s.TestConnection(ctx)
	s.PutSMTPSettings(ctx, bodyOf(`{"host":"smtp.example.com","port":2525,"user":"user","pass":"********","from":"from@example.com","alert_email":"ops@example.com"}`))
	_, _ = s.TestConnection(ctx)
	if configured() != 4 || log.lines[len(log.lines)-1][1] != "SMTP transporter configured: smtp.example.com:2525" {
		t.Fatalf("log %q", log.lines)
	}
}

func TestConcurrentUse(t *testing.T) {
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	var mu sync.Mutex
	sends := 0
	s := New(Options{DB: db, Logger: &quietLog{}, Transport: transportFunc(func() {
		mu.Lock()
		sends++
		mu.Unlock()
	})})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text := "x"
			for k := range 25 {
				switch (i + k) % 4 {
				case 0:
					s.ResetTransporter()
				case 1:
					_, _ = s.TestConnection(context.Background())
				case 2:
					_ = s.GetSMTPSettings(context.Background())
				}
				if ok, err := s.SendEmail(context.Background(), Email{To: []string{strconv.Itoa(k) + "@example.org"}, Text: &text}); !ok || err != nil {
					t.Error(ok, err)
				}
			}
		}()
	}
	wg.Wait()
	if sends != 200 {
		t.Fatalf("%d mails", sends)
	}
}

type quietLog struct{}

func (*quietLog) Info(...any)  {}
func (*quietLog) Warn(...any)  {}
func (*quietLog) Error(...any) {}

type transportFunc func()

func (f transportFunc) Send(context.Context, Settings, Outgoing) error { f(); return nil }
func (f transportFunc) Verify(context.Context, Settings) error         { return nil }

// Bytes that are no UTF-8 become the text Node made of them, before any
// JSON is read.
func TestDecodeLossyAsNode(t *testing.T) {
	var misc struct {
		Lossy [][2]string `json:"lossy"`
	}
	readGzip(t, "misc.json.gz", &misc)
	if len(misc.Lossy) < 3000 {
		t.Fatalf("%d byte strings", len(misc.Lossy))
	}
	changed := 0
	for _, pair := range misc.Lossy {
		raw, err := hex.DecodeString(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		got := decodeLossy(string(raw))
		if got != pair[1] {
			t.Errorf("% x: %q, Node: %q", raw, got, pair[1])
		}
		if got != string(raw) {
			changed++
		}
	}
	if changed < 1000 {
		t.Fatalf("only %d of the byte strings are no UTF-8", changed)
	}
}

// A settings row that is no text (a blob in a SQLite file) is no
// configuration, whatever its bytes say.
func TestASettingThatIsNoTextIsNoConfiguration(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO settings (key, value) VALUES (?, CAST(? AS BLOB))", settingKey, fullConfig); err != nil {
		t.Fatal(err)
	}
	s, _, _ := newTestService(db, nil, Options{})
	if cfg := s.LoadSMTPConfig(ctx); cfg != nil {
		t.Fatalf("a configuration from a blob: %s", cfg.signature())
	}
	// A save over it does not take a password from it
	if answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"h","from":"f@example.com","pass":"********"}`)); answer.Status != 200 {
		t.Fatalf("%d", answer.Status)
	}
	if got := stored(t, db); !strings.Contains(got, `"pass":""`) {
		t.Fatalf("stored %s", got)
	}
}

func TestParseFlag(t *testing.T) {
	for _, c := range []struct {
		value, fallback string
		want            bool
	}{
		{"true", "false", true}, {"TRUE", "false", true}, {"tRuE", "false", true}, {"", "true", true}, {"", "TRUE", true},
		{"", "false", false}, {"", "", false}, {"false", "true", false}, {"1", "true", false}, {"yes", "true", false}, {"trux", "true", false},
		{"fals", "true", false}, {"0000", "true", false}, {" true", "true", false}, {"true ", "true", false}, {"truee", "true", false}, {"tru", "true", false},
	} {
		if got := ParseFlag(c.value, c.fallback); got != c.want {
			t.Errorf("ParseFlag(%q, %q) = %v", c.value, c.fallback, got)
		}
	}
}
