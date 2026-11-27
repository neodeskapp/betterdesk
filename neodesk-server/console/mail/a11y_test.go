package mail

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// inCET runs the test with the server in Central European time.
func inCET(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.FixedZone("CET", 3600)
	t.Cleanup(func() { time.Local = old })
}

var testAlert = Alert{Severity: jsbind.String("critical"), DeviceID: jsbind.String("123456789"), Message: jsbind.String("CPU usage 97% for 10 minutes"), TriggeredAt: jsbind.String("2026-11-26T10:15:00.000Z")}

func testRule(to string) AlertRule {
	return AlertRule{Name: jsbind.String("High CPU"), Description: jsbind.String("CPU above 90 percent"), ConditionType: jsbind.String("cpu_usage"), ConditionOp: jsbind.String("gt"), ConditionValue: jsbind.Number(90), NotifyEmails: jsbind.String(to)}
}

// sentAlert sends an alert mail through the service and returns what went
// to the transport.
func sentAlert(t *testing.T, opts Options, env map[string]string) []Outgoing {
	t.Helper()
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, env, opts)
	ok, err := s.SendAlertEmail(context.Background(), testAlert, testRule("ops@example.org, lead@example.org"))
	if !ok || err != nil || len(transport.sends) != 2 {
		t.Fatalf("sent %v, %v, %d sends", ok, err, len(transport.sends))
	}
	var out []Outgoing
	for _, s := range transport.sends {
		out = append(out, s.mail)
	}
	return out
}

func sentHelp(t *testing.T, opts Options, env map[string]string) Outgoing {
	t.Helper()
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, env, opts)
	ok, err := s.SendHelpRequestEmail(context.Background(), HelpRequestEmail{To: []string{"ops@example.org"},
		HelpRequest: HelpRequest{Hostname: jsbind.String("PC-RECEPTION"), DeviceID: jsbind.String("123456789"), Message: jsbind.String("The printer doesn't work")},
		FolderName:  jsbind.String("Front office")})
	if !ok || err != nil || len(transport.sends) != 1 {
		t.Fatalf("sent %v, %v, %d sends", ok, err, len(transport.sends))
	}
	return transport.sends[0].mail
}

func header(m Outgoing, name string) string {
	for _, h := range m.Headers {
		if h.Name == name {
			return h.Value
		}
	}
	return ""
}

// The alert mail says who sends it and what happened in its subject, has
// one button to the device, its facts in a table with row headers, the
// time in the server's zone in words, and goes to each address on its own
// with a link that turns alert mails off for it.
func TestAlertMail(t *testing.T) {
	inCET(t)
	sends := sentAlert(t, Options{}, map[string]string{"PANEL_PUBLIC_URL": "https://console.example.org"})
	m := sends[0]
	html, text := *m.HTML, *m.Text
	if m.Subject != "Alert from NeoDesk: High CPU" || len(m.To) != 1 || m.To[0] != "ops@example.org" || sends[1].To[0] != "lead@example.org" {
		t.Errorf("subject %q to %v, %v", m.Subject, m.To, sends[1].To)
	}
	for _, re := range []string{
		`^<!DOCTYPE html><html lang="en" dir="ltr"`, `<h1 [^>]*>High CPU on 123456789</h1>`,
		`<th scope="row"[^>]*>Severity</th><td [^>]*>Critical</td>`,
		`<th scope="row"[^>]*>Triggered</th><td [^>]*>26 November 2026, 11:15 CET</td>`,
		`<th scope="row"[^>]*>Condition</th><td [^>]*>CPU use \(%\) Greater than 90</td>`,
		`<a class="nd-on nd-btn-a" href="https://console.example.org/devices\?open=123456789"[^>]*>Open the device</a>`,
		`nd-preheader[^>]*>Critical on 123456789: CPU usage 97% for 10 minutes`,
	} {
		if !regexp.MustCompile(re).MatchString(html) {
			t.Errorf("the alert mail has no %s:\n%s", re, html)
		}
	}
	for _, want := range []string{"Severity: Critical", "Triggered: 26 November 2026, 11:15 CET", "CPU above 90 percent", "Open the device:\nhttps://console.example.org/devices?open=123456789"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text part has no %q:\n%s", want, text)
		}
	}
	if header(m, "Auto-Submitted") != "auto-generated" || !strings.HasPrefix(header(m, "List-Unsubscribe"), "<https://console.example.org/mail/unsubscribe?t=") || header(m, "List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Errorf("headers %v", m.Headers)
	}
	if header(m, "List-Unsubscribe") == header(sends[1], "List-Unsubscribe") {
		t.Error("two recipients share one unsubscribe link")
	}
}

// The help request mail names the server and the device in its subject,
// the message as its preheader, and says why the reader gets it.
func TestHelpMail(t *testing.T) {
	m := sentHelp(t, Options{}, map[string]string{"PANEL_PUBLIC_URL": "https://console.example.org"})
	html := *m.HTML
	if m.Subject != "[Front office] NeoDesk: PC-RECEPTION asks for help" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, re := range []string{
		`<h1 [^>]*>PC-RECEPTION asks for help</h1>`, `nd-preheader[^>]*>The printer doesn&#39;t work`,
		`<th scope="row"[^>]*>Folder</th><td [^>]*>Front office</td>`,
		`<a class="nd-on nd-btn-a" href="https://console.example.org/help-requests"[^>]*>Open the help requests</a>`,
		`>You get this mail because you look after this device&#39;s folder or group.</p>`,
	} {
		if !regexp.MustCompile(re).MatchString(html) {
			t.Errorf("the help request mail has no %s:\n%s", re, html)
		}
	}
	if !strings.Contains(*m.Text, "https://console.example.org/help-requests") {
		t.Errorf("the text part has no link:\n%s", *m.Text)
	}
}

// Without language files the mails are English; with them, the console's
// default language and its direction, or the reader's account language.
func TestMailsInTheReadersLanguage(t *testing.T) {
	for _, c := range []struct{ lang, dir, severity string }{{"de", "ltr", "Kritisch"}, {"ar", "rtl", ""}} {
		b := i18n.New(filepath.Join("..", "static", "files", "lang"), c.lang)
		b.Load()
		opts := Options{Texts: func() *i18n.Bundle { return b }}
		m := sentAlert(t, opts, nil)[0]
		if m.Lang != c.lang || !strings.HasPrefix(*m.HTML, `<!DOCTYPE html><html lang="`+c.lang+`" dir="`+c.dir+`"`) {
			t.Errorf("%s alert: Lang %q, page starts %.80q", c.lang, m.Lang, *m.HTML)
		}
		if c.severity != "" && !strings.Contains(*m.HTML, ">"+c.severity+"</td>") {
			t.Errorf("%s alert: no severity %q", c.lang, c.severity)
		}
		h := sentHelp(t, opts, nil)
		if h.Lang != c.lang {
			t.Errorf("%s help request: Lang %q", c.lang, h.Lang)
		}
	}

	// An address of an account with a language gets the mail in it
	b := i18n.New(filepath.Join("..", "static", "files", "lang"), "en")
	b.Load()
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	if _, err := db.Exec(context.Background(), `INSERT INTO users (username, password_hash, role, email, preferred_language) VALUES ('ops', 'x', 'admin', 'OPS@example.org', 'de')`); err != nil {
		t.Fatal(err)
	}
	s, transport, _ := newTestService(db, nil, Options{Texts: func() *i18n.Bundle { return b }})
	if ok, err := s.SendAlertEmail(context.Background(), testAlert, testRule("ops@example.org, other@example.org")); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if len(transport.sends) != 2 || transport.sends[0].mail.Lang != "de" || transport.sends[1].mail.Lang != "en" {
		t.Errorf("languages %q %q", transport.sends[0].mail.Lang, transport.sends[1].mail.Lang)
	}
	if !strings.HasPrefix(transport.sends[0].mail.Subject, "Alarm von NeoDesk:") {
		t.Errorf("German subject %q", transport.sends[0].mail.Subject)
	}
}

// The English words the mails use without language files are the English
// language file's.
func TestEnglishMailWordsAreTheLanguageFiles(t *testing.T) {
	b := i18n.New(filepath.Join("..", "static", "files", "lang"), "en")
	b.Load()
	for key, text := range englishMailWords {
		if got := b.Translate("en", key, nil); got != text {
			t.Errorf("%s is %q in en.json, %q in the mail", key, got, text)
		}
	}
}

// Every mail goes out with a display name in From (the server's name, or
// the one set in the mail settings), replies to the mail's person or the
// support address, and a Message-ID domain someone owns.
func TestSenderAndReplies(t *testing.T) {
	ctx := context.Background()
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, map[string]string{"PANEL_PUBLIC_URL": "https://desk.acme.example"}, Options{
		Branding: func() brandmail.Branding { return brandmail.Branding{Server: "Acme IT"} },
	})
	m := brandmail.Mail{Subject: "s", Heading: "h", ReplyTo: "jana@acme.example"}
	if ok, _ := s.SendEmail(ctx, Email{To: []string{"a@example.org"}, Mail: m}); !ok {
		t.Fatal("not sent")
	}
	got := transport.sends[0].mail
	if !strings.HasPrefix(got.From, `"Acme IT via NeoDesk" <`) || got.ReplyTo != "jana@acme.example" || header(got, "Auto-Submitted") != "" {
		t.Errorf("from %q, reply to %q, headers %v", got.From, got.ReplyTo, got.Headers)
	}
	if _, err := s.SaveIdentity(ctx, Identity{FromName: "Acme Helpdesk", Organisation: "Acme GmbH", SupportEmail: "it@acme.example"}); err != nil {
		t.Fatal(err)
	}
	m.ReplyTo, m.Automatic = "", true
	if ok, _ := s.SendEmail(ctx, Email{To: []string{"a@example.org"}, Mail: m}); !ok {
		t.Fatal("not sent")
	}
	got = transport.sends[1].mail
	if !strings.HasPrefix(got.From, `"Acme Helpdesk" <`) || got.ReplyTo != "it@acme.example" || header(got, "Auto-Submitted") != "auto-generated" {
		t.Errorf("from %q, reply to %q, headers %v", got.From, got.ReplyTo, got.Headers)
	}
	if !strings.Contains(*got.HTML, "Acme GmbH runs this server.") || !strings.Contains(*got.HTML, `href="mailto:it@acme.example"`) {
		t.Errorf("the footer doesn't say who runs the server:\n%s", *got.HTML)
	}
	if got.MessageIDDomain != "" {
		t.Errorf("a sender at example.com gets Message-ID domain %q", got.MessageIDDomain)
	}
	if d := messageIDDomain("neodesk@localhost", "https://desk.acme.example:8443"); d != "desk.acme.example" {
		t.Errorf("a sender at localhost gets Message-ID domain %q", d)
	}
	if _, err := s.SaveIdentity(ctx, Identity{SupportURL: "javascript:alert(1)"}); err == nil {
		t.Error("a support page that is no web address was saved")
	}
}

// A mail that brandmail did not lay out never reaches the transport, and
// one without a subject or a heading is refused.
func TestOnlyRenderedMailsGoOut(t *testing.T) {
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, nil, Options{})
	for _, m := range []brandmail.Mail{{Subject: "s"}, {Heading: "h"}, {}} {
		if ok, _ := s.SendEmail(context.Background(), Email{To: []string{"a@example.org"}, Mail: m}); ok {
			t.Errorf("%+v went out", m)
		}
	}
	tr := s.transporterFor(context.Background())
	if err := s.sendBuilt(context.Background(), tr, []string{"a@example.org"}, false, brandmail.Built{Subject: "s", HTML: "<p>raw</p>"}); err != errNotRendered {
		t.Errorf("a hand-made mail: %v", err)
	}
	if len(transport.sends) != 0 {
		t.Errorf("%d mails reached the transport", len(transport.sends))
	}
}

// An address that turned a notification off with its link gets no more of
// that kind, and gets it again once an admin turns it on.
func TestUnsubscribe(t *testing.T) {
	ctx := context.Background()
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, map[string]string{"PANEL_PUBLIC_URL": "https://console.example.org"}, Options{})
	if ok, _ := s.SendAlertEmail(ctx, testAlert, testRule("ops@example.org")); !ok {
		t.Fatal("not sent")
	}
	link := strings.Trim(header(transport.sends[0].mail, "List-Unsubscribe"), "<>")
	token := link[strings.Index(link, "?t=")+3:]
	kind, addr, ok := s.ReadUnsubscribeToken(ctx, token)
	if !ok || kind != NotifyAlert || addr != "ops@example.org" {
		t.Fatalf("token reads %q %q %v", kind, addr, ok)
	}
	if _, _, ok := s.ReadUnsubscribeToken(ctx, token[:len(token)-2]+"xx"); ok {
		t.Error("a changed token is read")
	}
	if err := s.SetUnsubscribed(ctx, kind, addr, true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.SendAlertEmail(ctx, testAlert, testRule("OPS@example.org")); ok || len(transport.sends) != 1 {
		t.Errorf("an address that turned alerts off got one (%d sends)", len(transport.sends))
	}
	if ok, _ := s.SendHelpRequestEmail(ctx, HelpRequestEmail{To: []string{"ops@example.org"}, HelpRequest: HelpRequest{Hostname: jsbind.String("PC")}}); !ok {
		t.Error("turning alerts off stopped help request mails")
	}
	if err := s.SetUnsubscribed(ctx, kind, addr, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.SendAlertEmail(ctx, testAlert, testRule("ops@example.org")); !ok {
		t.Error("turned on again, still no mail")
	}
}
