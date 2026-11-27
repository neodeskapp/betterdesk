package onboard

import (
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
)

// Translate gives the text of a language key with its placeholders
// filled, in the language the mail is written in.
type Translate = brandmail.Translate

// MailFacts are what an invite or quick support mail says.
type MailFacts struct {
	// Kind is KindJoin or KindSupport.
	Kind string
	// Lang is the language of T's texts.
	Lang string
	// Link is the code's link and Code the code, both in the one form
	// the mail shows (Display): the link written out has the code as the
	// reader types it.
	Link string
	Code string
	// Branding is the server's name, for the wording: a server named
	// NeoDesk is not named as the reader's support.
	Branding brandmail.Branding
	// Sender is the person who sends it, by display name, and SenderEmail
	// where replies go.
	Sender      string
	SenderEmail string
	// Expires is when the link stops working, Zone where the reader's
	// times are said.
	Expires time.Time
	Zone    *time.Location
}

// MailLink is the link of a code for a mail: the console's page with the
// code as Display writes it, the form the mail shows the code in. The
// page reads it with or without the hyphen.
func (c Console) MailLink(kind, code string) string {
	if kind == KindSupport {
		return c.Base() + "/s/" + Display(code)
	}
	return c.Base() + "/join/" + Display(code)
}

// BuildMail writes the mail of a code. An invite is the welcome mail:
// who invited the person, one button (Set up NeoDesk), the three steps,
// until when it works and the code for a NeoDesk that is already
// installed. A quick support mail says who wants to help and what to do,
// with one button. Replies go to the person who sent it. The keys are
// below mail.invite and mail.support in the language files.
func BuildMail(t Translate, f MailFacts) brandmail.Mail {
	named := f.Branding.OwnName()
	vars := map[string]any{"server": f.Branding.Name(), "sender": f.Sender, "expires": brandmail.DateTime(f.Expires, f.Lang, f.Zone)}
	pick := func(key string) string {
		if named {
			return t(key+"_named", vars)
		}
		return t(key, vars)
	}
	m := brandmail.Mail{T: t, Lang: f.Lang, Link: f.Link, ReplyTo: f.SenderEmail}
	if f.Kind == KindSupport {
		m.Kind = "support"
		m.Subject = t("mail.support.subject", vars)
		m.Preheader = t("mail.support.preheader", vars)
		m.Heading = t("mail.support.heading", vars)
		m.Lead = pick("mail.support.lead")
		m.Button = t("mail.support.button", vars)
		m.Details = []string{t("mail.support.expires", vars)}
		m.Why = t("mail.support.why", vars)
		return m
	}
	m.Kind = "invite"
	m.Subject = t("mail.invite.subject", vars)
	m.Preheader = pick("mail.invite.preheader")
	m.Heading = t("mail.invite.heading", vars)
	m.Lead = pick("mail.invite.lead")
	m.Button = t("mail.invite.button", vars)
	m.Steps = []string{t("mail.invite.step_1", vars), t("mail.invite.step_2", vars), pick("mail.invite.step_3")}
	m.CodeLead, m.Code = t("mail.invite.code_lead", vars), Display(f.Code)
	m.Details = []string{t("mail.invite.expires", vars)}
	m.Why = t("mail.invite.why", vars)
	return m
}
