package mail

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// sentAlert sends an alert mail through the service and returns what went
// to the transport.
func sentAlert(t *testing.T, opts Options) Outgoing {
	t.Helper()
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, nil, opts)
	ok, err := s.SendAlertEmail(context.Background(),
		Alert{Severity: jsbind.String("critical"), DeviceID: jsbind.String("123456789"), Message: jsbind.String("CPU usage 97% for 10 minutes"), TriggeredAt: jsbind.String("2026-11-26T10:15:00.000Z")},
		AlertRule{Name: jsbind.String("High CPU"), Description: jsbind.String("CPU above 90 percent"), ConditionType: jsbind.String("cpu_usage"), ConditionOp: jsbind.String("gt"), ConditionValue: jsbind.Number(90), NotifyEmails: jsbind.String("ops@example.org")})
	if !ok || err != nil || len(transport.sends) != 1 {
		t.Fatalf("sent %v, %v, %d sends", ok, err, len(transport.sends))
	}
	return transport.sends[0].mail
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

// The alert mail is a whole page: language, title, a heading, its facts in
// a table with row headers, a readable time and condition, and no grey
// under 4.5:1; its text part says the same.
func TestAlertMailIsAPage(t *testing.T) {
	m := sentAlert(t, Options{})
	html, text := *m.HTML, *m.Text
	for _, re := range []string{
		`^<!DOCTYPE html><html lang="en" dir="ltr">`, `<title>Alert: High CPU</title>`, `<h1 [^>]*>Alert: High CPU</h1>`,
		`<th scope="row"[^>]*>Severity</th><td [^>]*>Critical</td>`, `<th scope="row"[^>]*>Device</th><td [^>]*>123456789</td>`,
		`<th scope="row"[^>]*>Triggered</th><td [^>]*>2026-11-26 10:15 UTC</td>`,
		`<th scope="row"[^>]*>Condition</th><td [^>]*>CPU use \(%\) Greater than 90</td>`,
	} {
		if !regexp.MustCompile(re).MatchString(html) {
			t.Errorf("the alert mail has no %s:\n%s", re, html)
		}
	}
	if strings.Contains(html, "#888") || strings.Contains(html, "#e74c3c") {
		t.Error("the alert mail still has its old grey or red text")
	}
	for _, want := range []string{"Alert: High CPU", "Severity: Critical", "Triggered: 2026-11-26 10:15 UTC", "Condition: CPU use (%) Greater than 90", "CPU above 90 percent"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text part has no %q:\n%s", want, text)
		}
	}
	if m.Lang != "en" {
		t.Errorf("Lang %q", m.Lang)
	}
}

// The help request mail is a whole page too, with its link as the button.
func TestHelpMailIsAPage(t *testing.T) {
	m := sentHelp(t, Options{}, map[string]string{"PANEL_PUBLIC_URL": "https://console.example.org"})
	html := *m.HTML
	for _, re := range []string{
		`^<!DOCTYPE html><html lang="en" dir="ltr">`, `<title>Help request from PC-RECEPTION</title>`, `<h1 [^>]*>Help request from PC-RECEPTION</h1>`,
		`<th scope="row"[^>]*>Device ID</th><td [^>]*>123456789</td>`, `<th scope="row"[^>]*>Folder</th><td [^>]*>Front office</td>`,
		`<th scope="row"[^>]*>Message</th><td [^>]*>The printer doesn&#39;t work</td>`,
		`<a class="nd-on" href="https://console.example.org/help-requests"[^>]*>Open help requests in NeoDesk</a>`,
	} {
		if !regexp.MustCompile(re).MatchString(html) {
			t.Errorf("the help request mail has no %s:\n%s", re, html)
		}
	}
	if !strings.Contains(*m.Text, "https://console.example.org/help-requests") {
		t.Errorf("the text part has no link:\n%s", *m.Text)
	}
}

// Both mails are in the console's default language, with its direction.
func TestAlertAndHelpMailsInTheConsoleLanguage(t *testing.T) {
	for _, c := range []struct{ lang, dir, severity string }{{"de", "ltr", "Kritisch"}, {"ar", "rtl", ""}} {
		b := i18n.New(filepath.Join("..", "static", "files", "lang"), c.lang)
		b.Load()
		opts := Options{Texts: func() *i18n.Bundle { return b }}
		m := sentAlert(t, opts)
		if m.Lang != c.lang || !strings.HasPrefix(*m.HTML, `<!DOCTYPE html><html lang="`+c.lang+`" dir="`+c.dir+`">`) {
			t.Errorf("%s alert: Lang %q, page starts %.80q", c.lang, m.Lang, *m.HTML)
		}
		if c.severity != "" && !strings.Contains(*m.HTML, ">"+c.severity+"</td>") {
			t.Errorf("%s alert: no severity %q:\n%s", c.lang, c.severity, *m.HTML)
		}
		h := sentHelp(t, opts, nil)
		if h.Lang != c.lang || !strings.HasPrefix(*h.HTML, `<!DOCTYPE html><html lang="`+c.lang+`" dir="`+c.dir+`">`) {
			t.Errorf("%s help request: Lang %q, page starts %.80q", c.lang, h.Lang, *h.HTML)
		}
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
