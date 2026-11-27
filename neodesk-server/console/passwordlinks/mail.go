package passwordlinks

import (
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
)

// Translate gives the text of a language key with its placeholders
// filled, in the language the mail is written in.
type Translate = brandmail.Translate

// MailFacts are what a link's mail says.
type MailFacts struct {
	// Purpose is PurposeInvite or PurposeReset.
	Purpose string
	// Lang is the language of the texts.
	Lang string
	// Link is the link, Username the account it is for.
	Link     string
	Username string
	// Sender is who sent it by display name, SenderEmail where replies
	// go. A reset the person asked for themselves (Self) names no sender.
	Sender      string
	SenderEmail string
	Self        bool
	// Expires is when the link stops working, Zone where the reader's
	// times are said.
	Expires time.Time
	Zone    *time.Location
	// Branding is the console's name and host.
	Branding brandmail.Branding
}

// BuildMail writes the mail of a link: who sent it, one button, the link
// written out, the username and until when the link works. A reset the
// person asked for says only what the sign-in page already said: that an
// account with this address exists, to its own address. The keys are
// below mail.account_invite and mail.reset in the language files.
func BuildMail(t Translate, f MailFacts) brandmail.Mail {
	b := f.Branding
	vars := map[string]any{
		"server": b.Name(), "sender": f.Sender, "username": f.Username, "host": b.Host,
		"expires": brandmail.DateTime(f.Expires, f.Lang, f.Zone),
	}
	m := brandmail.Mail{T: t, Lang: f.Lang, Link: f.Link}
	if f.Purpose == PurposeInvite {
		m.Kind = "account-invite"
		if b.OwnName() {
			m.Subject, m.Lead = t("mail.account_invite.subject_named", vars), t("mail.account_invite.lead_named", vars)
		} else {
			m.Subject, m.Lead = t("mail.account_invite.subject", vars), t("mail.account_invite.lead", vars)
		}
		m.Preheader = t("mail.account_invite.preheader", vars)
		m.Heading = t("mail.account_invite.heading", vars)
		m.Button = t("mail.account_invite.button", vars)
		m.Details = []string{t("mail.account_invite.username", vars), t("mail.account_invite.expires", vars)}
		m.Why = t("mail.account_invite.why", vars)
		m.ReplyTo = f.SenderEmail
		return m
	}
	m.Kind = "reset"
	if b.OwnName() {
		m.Subject = t("mail.reset.subject_named", vars)
	} else {
		m.Subject = t("mail.reset.subject", vars)
	}
	m.Preheader = t("mail.reset.preheader", vars)
	m.Heading = t("mail.reset.heading", vars)
	m.Button = t("mail.reset.button", vars)
	m.Details = []string{t("mail.account_invite.username", vars), t("mail.account_invite.expires", vars), t("mail.reset.ignore", vars)}
	if f.Self {
		m.Kind = "reset-self"
		m.Lead, m.Why, m.Automatic = t("mail.reset.lead_self", vars), t("mail.reset.why_self", vars), true
	} else {
		m.Lead, m.Why, m.ReplyTo = t("mail.reset.lead", vars), t("mail.reset.why", vars), f.SenderEmail
	}
	return m
}
