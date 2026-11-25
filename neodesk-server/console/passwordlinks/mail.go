package passwordlinks

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
)

// Translate gives the text of a language key with its placeholders
// filled, in the language the mail is written in.
type Translate func(key string, vars map[string]any) string

// Mail is a link's mail: a subject, a text part and the same as an HTML
// part.
type Mail struct {
	Subject string
	Text    string
	HTML    string
}

// MailFacts are what a link's mail says.
type MailFacts struct {
	// Purpose is PurposeInvite or PurposeReset.
	Purpose string
	// Link is the link, Username the account it is for.
	Link     string
	Username string
	// Sender is who sent it, Host the console's host name.
	Sender string
	Host   string
	// Expires is when the link stops working, as the reader sees it.
	Expires string
	// Branding is the console's name, accent colour and logo.
	Branding brandmail.Branding
}

// BuildMail writes the mail of a link, in the console's branded layout:
// who sent it, one button, the link written out for when the button does
// not work, the username and until when the link works. The keys are
// below password_link in the language files.
func BuildMail(t Translate, f MailFacts) Mail {
	vars := map[string]any{
		"server": f.Branding.Server, "sender": f.Sender, "username": f.Username,
		"expires": f.Expires, "host": f.Host,
	}
	prefix := "password_link.reset_"
	if f.Purpose == PurposeInvite {
		prefix = "password_link.invite_"
	}
	subject := t(prefix+"subject", vars)
	heading := t(prefix+"heading", vars)
	lead := t(prefix+"lead", vars)
	button := t(prefix+"button", vars)
	small := []string{t("password_link.mail_username", vars), t(prefix+"expires", vars)}
	if f.Purpose != PurposeInvite {
		small = append(small, t("password_link.reset_ignore", vars))
	}
	linkHint := t("password_link.mail_link_hint", vars)
	footer := t("password_link.mail_footer", vars)

	lines := []string{heading, "", lead, "", button + ": " + f.Link, ""}
	lines = append(lines, small...)
	lines = append(lines, "", "--", footer)
	return Mail{
		Subject: subject,
		Text:    strings.Join(lines, "\n") + "\n",
		HTML: brandmail.HTML(brandmail.Page{
			Branding: f.Branding, Heading: heading, Lead: lead, Button: button, Link: f.Link,
			LinkHint: linkHint, Small: small, Footer: footer,
		}),
	}
}
