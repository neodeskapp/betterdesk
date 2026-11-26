package onboard

import (
	"fmt"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
)

// Translate gives the text of a language key with its placeholders
// filled, in the language the mail is written in.
type Translate func(key string, vars map[string]any) string

// Mail is an invite or quick support mail: a subject, a text part and the
// same as an HTML part.
type Mail struct {
	Subject string
	Text    string
	HTML    string
	// Inline are the logo images the HTML shows (brandmail.Inline).
	Inline []message.Inline
	// Lang is the language the texts are in, for mail.Email.Lang.
	Lang string
}

// MailFacts are what a mail says.
type MailFacts struct {
	// Kind is KindJoin or KindSupport.
	Kind string
	// Link is the code's link; Code the code as people read it.
	Link string
	Code string
	// Server is the console's name (branding), Sender the account's name,
	// Host the console's host name.
	Server string
	Sender string
	Host   string
	// Expires is when the link stops working, as the reader sees it.
	Expires string
	// Company is whose support helps the person (the server's name).
	Company string
	// Accent is the branding's accent colour (#rrggbb); anything else is
	// the default blue.
	Accent string
	// Logo is the address of the branding logo as a PNG, JPEG or GIF over
	// HTTPS; anything else leaves the logo out for the server's name.
	Logo string
}

// BuildMail writes the mail of a code. An invite is the welcome mail:
// who invited the person to which server, one button (Set up NeoDesk),
// the three steps, the link for when the button does not work, until
// when it works and the code for a NeoDesk that is already installed. A
// quick support mail says who wants to help and what to do, with the same
// button. Both carry the server's logo or name and its accent colour, in
// a table layout with inline styles, as mail clients need it; nothing in
// them but the link opens anything. The keys are below onboard.mail in
// the language files; the mail's language is the one whose meta.lang t
// gives, the one its texts came from.
func BuildMail(t Translate, f MailFacts) Mail {
	lang := t("meta.lang", nil)
	if lang == "meta.lang" {
		lang = ""
	}
	company := f.Company
	if company == "" {
		company = f.Server
	}
	vars := map[string]any{"server": f.Server, "sender": f.Sender, "code": f.Code, "expires": f.Expires, "host": f.Host, "company": company}
	var subject, heading, lead, button, expires, code string
	var steps []string
	if f.Kind == KindSupport {
		subject = t("onboard.mail.support_subject", vars)
		heading = t("onboard.mail.support_heading", vars)
		lead = t("onboard.mail.support_intro", vars) + " " + t("onboard.mail.support_steps", vars)
		button = t("onboard.mail.support_button", vars)
		expires = t("onboard.mail.support_expires", vars)
	} else {
		subject = t("onboard.mail.welcome_subject", vars)
		heading = subject
		lead = t("onboard.mail.welcome_lead", vars)
		button = t("onboard.mail.welcome_button", vars)
		steps = []string{t("onboard.mail.welcome_step_1", vars), t("onboard.mail.welcome_step_2", vars), t("onboard.mail.welcome_step_3", vars)}
		expires = t("onboard.mail.invite_expires", vars)
		code = t("onboard.mail.invite_code", vars)
	}
	linkHint := t("onboard.mail.welcome_link", vars)
	footer := t("onboard.mail.footer", vars)

	return Mail{
		Subject: subject,
		Text:    mailText(heading, lead, steps, button, f.Link, expires, code, f.Code, footer),
		HTML:    mailHTML(f, lang, heading, lead, steps, button, linkHint, expires, code, footer),
		Inline:  brandmail.Inline(brandmail.Page{Branding: brandmail.Branding{Server: f.Server, Logo: f.Logo}}),
		Lang:    lang,
	}
}

// mailText is the plain text part. codeLead, when set, says where to
// enter the code, which follows on a line of its own.
func mailText(heading, lead string, steps []string, button, link, expires, codeLead, code, footer string) string {
	lines := []string{heading, "", lead, ""}
	for i, step := range steps {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, step))
	}
	if len(steps) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, button+": "+link, "", expires)
	if codeLead != "" {
		lines = append(lines, "", codeLead, code)
	}
	lines = append(lines, "", "--", footer)
	return strings.Join(lines, "\n") + "\n"
}

// mailHTML is the HTML part, in the console's branded layout. The code
// is a line of its own below codeLead.
func mailHTML(f MailFacts, lang, heading, lead string, steps []string, button, linkHint, expires, codeLead, footer string) string {
	p := brandmail.Page{
		Branding: brandmail.Branding{Server: f.Server, Accent: f.Accent, Logo: f.Logo}, Lang: lang,
		Heading: heading, Lead: lead, Button: button, Link: f.Link, Steps: steps,
		LinkHint: linkHint, Small: []string{expires}, Footer: footer,
	}
	if codeLead != "" {
		p.CodeLead, p.Code = codeLead, f.Code
	}
	return brandmail.HTML(p)
}
