package onboard

import (
	"html"
	"strings"
)

// Translate gives the text of a language key with its placeholders
// filled, in the language the mail is written in.
type Translate func(key string, vars map[string]any) string

// Mail is an invite or quick support mail: a subject, a short text part
// and the same as a short HTML part.
type Mail struct {
	Subject string
	Text    string
	HTML    string
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
}

// BuildMail writes the mail of a code: a heading, who sends it, what to
// do, the link (a button in HTML), until when it works, for an invite the
// code for a BetterDesk that is already installed, and who sent the mail.
// The keys are below onboard.mail in the language files.
func BuildMail(t Translate, f MailFacts) Mail {
	prefix := "onboard.mail.invite_"
	if f.Kind == KindSupport {
		prefix = "onboard.mail.support_"
	}
	vars := map[string]any{"server": f.Server, "sender": f.Sender, "code": f.Code, "expires": f.Expires, "host": f.Host}
	subject := t(prefix+"subject", vars)
	heading := t(prefix+"heading", vars)
	intro := t(prefix+"intro", vars)
	steps := t(prefix+"steps", vars)
	button := t(prefix+"button", vars)
	expires := t(prefix+"expires", vars)
	code := ""
	if f.Kind != KindSupport {
		code = t("onboard.mail.invite_code", vars)
	}
	footer := t("onboard.mail.footer", vars)

	lines := []string{heading, "", intro, "", steps, f.Link, "", expires}
	if code != "" {
		lines = append(lines, "", code)
	}
	lines = append(lines, "", "--", footer)
	text := strings.Join(lines, "\n") + "\n"

	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<div style="font-family: -apple-system, 'Segoe UI', Roboto, sans-serif; max-width: 560px; line-height: 1.5; color: #1f2933;">`)
	b.WriteString(`<h1 style="font-size: 20px; margin: 0 0 16px;">` + e(heading) + `</h1>`)
	b.WriteString(`<p>` + e(intro) + `</p>`)
	b.WriteString(`<p>` + e(steps) + `</p>`)
	b.WriteString(`<table role="presentation" cellspacing="0" cellpadding="0"><tr><td style="border-radius: 6px; background: #1d4ed8;">`)
	b.WriteString(`<a href="` + e(f.Link) + `" style="display: inline-block; padding: 10px 18px; color: #ffffff; text-decoration: none; font-weight: 600;">` + e(button) + `</a>`)
	b.WriteString(`</td></tr></table>`)
	b.WriteString(`<p style="word-break: break-all;"><a href="` + e(f.Link) + `">` + e(f.Link) + `</a></p>`)
	b.WriteString(`<p>` + e(expires) + `</p>`)
	if code != "" {
		b.WriteString(`<p>` + e(code) + `</p>`)
	}
	b.WriteString(`<p style="color: #6b7280; font-size: 13px; border-top: 1px solid #e5e7eb; padding-top: 12px;">` + e(footer) + `</p>`)
	b.WriteString(`</div>`)
	return Mail{Subject: subject, Text: text, HTML: b.String()}
}
