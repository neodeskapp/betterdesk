package onboard

import (
	"regexp"
	"strings"
	"testing"
)

// englishMail is the welcome mail's English text, as the language file
// has it, so the test reads the mail as a person gets it.
func englishMail(key string, vars map[string]any) string {
	texts := map[string]string{
		"onboard.mail.welcome_subject": "{sender} invited you to {server}",
		"onboard.mail.welcome_lead":    "Set up NeoDesk on your computer. It takes a few minutes.",
		"onboard.mail.welcome_button":  "Set up NeoDesk",
		"onboard.mail.welcome_step_1":  "Download and open NeoDesk.",
		"onboard.mail.welcome_step_2":  "Click Install.",
		"onboard.mail.welcome_step_3":  "Done: {company}'s support can help you from now on, and you see when someone connects.",
		"onboard.mail.welcome_link":    "If the button doesn't work, open this link on the computer:",
		"onboard.mail.invite_expires":  "It works until {expires}.",
		"onboard.mail.invite_code":     "Already have NeoDesk? Open Settings, Server, Join a server, and enter the code {code}.",
		"onboard.mail.footer":          "Sent by {host} for {server}. If you didn't expect this email, you can ignore it.",
	}
	out, ok := texts[key]
	if !ok {
		return key
	}
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v.(string))
	}
	return out
}

func welcomeFacts() MailFacts {
	return MailFacts{
		Kind: KindJoin, Link: "https://desk.example.com/join/ABCD2345", Code: "ABCD-2345",
		Server: "Acme Desk", Sender: "Linus Rath", Company: "Acme", Host: "desk.example.com",
		Expires: "2026-12-01 15:00 UTC", Accent: "#1d4ed8", Logo: "https://desk.example.com/uploads/branding/logo-0123456789abcdef.png",
	}
}

var (
	hrefs = regexp.MustCompile(`href="([^"]*)"`)
	tags  = regexp.MustCompile(`<[a-zA-Z/!][^>]*>`)
)

// TestTheWelcomeMailHasOneButtonAndThreeSteps: the invite's mail says who
// invited the person to which server, has one button, Set up NeoDesk, and
// the three steps, in both parts.
func TestTheWelcomeMailHasOneButtonAndThreeSteps(t *testing.T) {
	m := BuildMail(englishMail, welcomeFacts())
	if m.Subject != "Linus Rath invited you to Acme Desk" {
		t.Errorf("subject %q", m.Subject)
	}
	steps := []string{"Download and open NeoDesk.", "Click Install.", "Done: Acme's support can help you from now on, and you see when someone connects."}
	for _, part := range []struct{ name, body string }{{"text", m.Text}, {"html", strings.ReplaceAll(m.HTML, "&#39;", "'")}} {
		at := -1
		for i, step := range steps {
			next := strings.Index(part.body, step)
			if next < 0 || next < at {
				t.Errorf("%s part: step %d missing or out of order:\n%s", part.name, i+1, part.body)
			}
			at = next
		}
		if !strings.Contains(part.body, "Linus Rath invited you to Acme Desk") {
			t.Errorf("%s part has no heading", part.name)
		}
	}
	if got := strings.Count(m.HTML, ">Set up NeoDesk</a>"); got != 1 {
		t.Errorf("%d buttons Set up NeoDesk, want one:\n%s", got, m.HTML)
	}
	if !strings.Contains(m.Text, "Set up NeoDesk: https://desk.example.com/join/ABCD2345") {
		t.Errorf("text part without the link:\n%s", m.Text)
	}
}

// TestTheWelcomeMailRendersInMailClients: table layout with inline styles,
// no scripts, SVG or forms, which mail clients drop; the one style sheet,
// in the head, only adds dark mode, so the mail reads without it; a plain
// text part without markup.
func TestTheWelcomeMailRendersInMailClients(t *testing.T) {
	m := BuildMail(englishMail, welcomeFacts())
	head, body, ok := strings.Cut(m.HTML, "</head>")
	if !ok || strings.Count(m.HTML, "<style") != 1 || !strings.Contains(head, "<style>") || !strings.Contains(head, "prefers-color-scheme: dark") {
		t.Errorf("want one style sheet in the head, for dark mode:\n%s", head)
	}
	for _, banned := range []string{"<style", "<script", "<svg", "<form", "<div", "<link", "display: flex", "display:flex", "position:"} {
		if strings.Contains(body, banned) {
			t.Errorf("the HTML body has %q", banned)
		}
	}
	if !strings.Contains(m.HTML, `role="presentation"`) || !strings.HasPrefix(m.HTML, "<!DOCTYPE html>") {
		t.Errorf("the HTML part is not a table layout document:\n%s", m.HTML)
	}
	if tags.MatchString(m.Text) {
		t.Errorf("the text part has markup:\n%s", m.Text)
	}
	// The button is a table cell with the accent colour, so Outlook shows it
	if !strings.Contains(m.HTML, `bgcolor="#1b62cf"`) {
		t.Errorf("the button is not in the accent colour:\n%s", m.HTML)
	}
}

// TestTheWelcomeMailCarriesNoSecretButTheLink: every link in the mail is
// the invite link, the only image the logo.
func TestTheWelcomeMailCarriesNoSecretButTheLink(t *testing.T) {
	f := welcomeFacts()
	m := BuildMail(englishMail, f)
	for _, href := range hrefs.FindAllStringSubmatch(m.HTML, -1) {
		if href[1] != f.Link {
			t.Errorf("a link that is not the invite: %q", href[1])
		}
	}
	if got := strings.Count(m.HTML, "<img"); got != 1 || !strings.Contains(m.HTML, `src="`+f.Logo+`"`) || !strings.Contains(m.HTML, `alt="Acme Desk"`) {
		t.Errorf("%d images, want the logo once:\n%s", got, m.HTML)
	}
}

// TestTheWelcomeMailTakesOnlyAUsableLogoAndColour: an SVG, plain-HTTP or
// script logo is left out for NeoDesk's mark, carried in the mail, and the
// name; a colour that is not one is the console's blue; a light accent
// gets dark text on the button.
func TestTheWelcomeMailTakesOnlyAUsableLogoAndColour(t *testing.T) {
	srcs := regexp.MustCompile(`src="([^"]*)"`)
	for _, logo := range []string{"", "https://desk.example.com/logo.svg", "http://desk.example.com/logo.png", "javascript:alert(1)//x.png", "data:image/png;base64,AAAA", `https://x.example/a.png" onerror="x`} {
		f := welcomeFacts()
		f.Logo = logo
		m := BuildMail(englishMail, f)
		for _, src := range srcs.FindAllStringSubmatch(m.HTML, -1) {
			if !strings.HasPrefix(src[1], "cid:") {
				t.Errorf("logo %q went in as %q", logo, src[1])
			}
		}
		if !strings.Contains(m.HTML, ">Acme Desk</td>") || len(m.Inline) != 2 {
			t.Errorf("logo %q: not the mark and the name instead (%d images)", logo, len(m.Inline))
		}
	}
	f := welcomeFacts()
	f.Accent = "red; background:url(x)"
	if m := BuildMail(englishMail, f); !strings.Contains(m.HTML, `bgcolor="#1b62cf"`) || strings.Contains(m.HTML, "url(x)") {
		t.Errorf("a bad colour went in:\n%s", m.HTML)
	}
	f.Accent = "#A3E635"
	m := BuildMail(englishMail, f)
	if !strings.Contains(m.HTML, `bgcolor="#a3e635"`) || !strings.Contains(m.HTML, "color: #111827") {
		t.Errorf("a light accent keeps white text:\n%s", m.HTML)
	}
}
