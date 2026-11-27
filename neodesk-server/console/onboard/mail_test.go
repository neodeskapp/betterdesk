package onboard

import (
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
)

func welcomeFacts(lang string) MailFacts {
	c := Console{Host: "desk.example.com", HTTPS: true}
	return MailFacts{
		Kind: KindJoin, Lang: lang, Link: c.MailLink(KindJoin, "abcd2345"), Code: "abcd2345",
		Branding: brandmail.Branding{Server: "Acme Desk", Host: "desk.example.com"},
		Sender:   "Linus Rath", SenderEmail: "linus@acme.example",
		Expires: time.Date(2026, 12, 3, 0, 12, 0, 0, time.UTC), Zone: time.FixedZone("CET", 3600),
	}
}

// The invite says who invites the person (by name, not by the server's
// default name), what to do, and until when, in the reader's date format
// and zone; replies go to the person who sent it.
func TestTheInviteSaysWhoInvitesAndUntilWhen(t *testing.T) {
	m := BuildMail(inLanguage(t, "en"), welcomeFacts("en"))
	if m.Subject != "Linus Rath invites you to set up remote support" {
		t.Errorf("subject %q", m.Subject)
	}
	if m.Lead != "Linus Rath from Acme Desk invited you to install NeoDesk on your computer, so Acme Desk can help you on it when you ask." {
		t.Errorf("lead %q", m.Lead)
	}
	if m.Steps[2] != "Done. Acme Desk can help you when you ask, and you always see when someone connects." {
		t.Errorf("step 3 %q", m.Steps[2])
	}
	if got := strings.Join(m.Details, " "); got != "The link and the code work until 3 December 2026, 01:12 CET." {
		t.Errorf("details %q", got)
	}
	if m.ReplyTo != "linus@acme.example" || m.Automatic {
		t.Errorf("reply to %q, automatic %v", m.ReplyTo, m.Automatic)
	}
	de := BuildMail(inLanguage(t, "de"), welcomeFacts("de"))
	if got := strings.Join(de.Details, " "); got != "Link und Code gelten bis 3. Dezember 2026, 01:12 Uhr CET." {
		t.Errorf("German details %q", got)
	}
}

// A server that keeps the name NeoDesk isn't named as the reader's
// support: the sentences name the person who invited them.
func TestTheDefaultNameIsNotTheSupport(t *testing.T) {
	f := welcomeFacts("en")
	f.Branding.Server = "NeoDesk"
	m := BuildMail(inLanguage(t, "en"), f)
	all := m.Subject + " " + m.Preheader + " " + m.Lead + " " + strings.Join(m.Steps, " ")
	if strings.Contains(all, "NeoDesk's support") || strings.Contains(all, "NeoDesk can help") || strings.Contains(all, "from NeoDesk") {
		t.Errorf("the mail names NeoDesk as the support: %s", all)
	}
	if m.Steps[2] != "Done. Linus Rath can help you when you ask, and you always see when someone connects." {
		t.Errorf("step 3 %q", m.Steps[2])
	}
}

// The code is in one form everywhere: the link written out and the code
// line both have ABCD-2345, and the code is not in the subject.
func TestTheCodeHasOneForm(t *testing.T) {
	m := BuildMail(inLanguage(t, "en"), welcomeFacts("en"))
	if m.Link != "https://desk.example.com/join/ABCD-2345" || m.Code != "ABCD-2345" {
		t.Errorf("link %q, code %q", m.Link, m.Code)
	}
	built := brandmail.Render(m, brandmail.Branding{Server: "Acme Desk"}, "")
	if strings.Contains(built.HTML, "ABCD2345") || strings.Contains(built.Text, "ABCD2345") {
		t.Error("the code is also written without its hyphen")
	}
	if strings.Contains(strings.ToUpper(m.Subject+m.Preheader), "ABCD") {
		t.Errorf("the code is in the subject or preheader: %q %q", m.Subject, m.Preheader)
	}
}

// The quick support mail has one button and says nothing gets installed.
func TestTheSupportMail(t *testing.T) {
	f := welcomeFacts("en")
	f.Kind = KindSupport
	f.Link = Console{Host: "desk.example.com", HTTPS: true}.MailLink(KindSupport, "abcd2345")
	m := BuildMail(inLanguage(t, "en"), f)
	if m.Subject != "Linus Rath is ready to help you" || m.Button != "Start the support session" || m.Link != "https://desk.example.com/s/ABCD-2345" {
		t.Errorf("%q %q %q", m.Subject, m.Button, m.Link)
	}
	if m.Code != "" || len(m.Steps) != 0 {
		t.Error("the support mail has a code or steps")
	}
	if !strings.Contains(strings.Join(m.Details, " "), "Nothing gets installed.") {
		t.Errorf("details %q", m.Details)
	}
}
