// Package mailcatalog is every mail the console sends, with made-up
// facts: the previews of the mail settings and their "Send me this mail"
// build them here, and the tests check each kind once.
package mailcatalog

import (
	"context"
	"fmt"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/chat"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/impersonate"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/emailchange"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/users"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
)

// Kinds are the mails, in the order the settings list them. Each has the
// title settings.email.preview_kind_<kind with _ for ->.
var Kinds = []string{
	"invite", "support", "account-invite", "reset", "reset-self", "email-confirm", "email-notice",
	"expiry", "inactive", "security-signin", "security-password", "security-two-factor", "security-email",
	"impersonation", "chat", "alert", "help", "test",
}

// Notifications are the kinds a reader can turn off from the mail.
var Notifications = map[string]string{"chat": mail.NotifyChat, "alert": mail.NotifyAlert, "help": mail.NotifyHelp}

// Source is what the samples are made with.
type Source struct {
	Bundle *i18n.Bundle
	Mail   *mail.Service
	// Base is the console's address, Server its name.
	Base   string
	Server string
	// Reader names the person a sample is about ("mmueller").
	Reader string
	// Now is the time the samples start from.
	Now time.Time
}

// ErrUnknown is the answer for a kind that doesn't exist.
var ErrUnknown = fmt.Errorf("no such mail")

// Build is the sample of a mail kind in a language.
func (s Source) Build(ctx context.Context, kind, lang string) (brandmail.Mail, error) {
	t, code := s.Mail.Translator(lang)
	zone := time.Local
	branding := brandmail.Branding{Server: s.Server, Host: brandmail.HostOf(s.Base)}
	user := s.Reader
	if user == "" {
		user = "mmueller"
	}
	week := s.Now.Add(7 * 24 * time.Hour)
	c, ok := onboard.ParseConsole(s.Base)
	if !ok {
		c = onboard.Console{Host: branding.Host, HTTPS: true}
	}
	switch kind {
	case "invite", "support":
		k := onboard.KindJoin
		if kind == "support" {
			k = onboard.KindSupport
			week = s.Now.Add(time.Hour)
		}
		return onboard.BuildMail(t, onboard.MailFacts{
			Kind: k, Lang: code, Link: c.MailLink(k, "9H83A6RJ"), Code: "9H83A6RJ", Branding: branding,
			Sender: "Jana Weber", SenderEmail: "jana.weber@example.com", Expires: week, Zone: zone,
		}), nil
	case "account-invite", "reset", "reset-self":
		purpose := passwordlinks.PurposeInvite
		if kind != "account-invite" {
			purpose = passwordlinks.PurposeReset
			week = s.Now.Add(time.Hour)
		}
		return passwordlinks.BuildMail(t, passwordlinks.MailFacts{
			Purpose: purpose, Lang: code, Link: passwordlinks.SetPasswordLink(s.Base, "sample-token"), Username: user,
			Sender: "Jana Weber", SenderEmail: "jana.weber@example.com", Self: kind == "reset-self",
			Expires: week, Zone: zone, Branding: branding,
		}), nil
	case "email-confirm":
		return emailchange.ConfirmMail(t, emailchange.Facts{Base: s.Base, Username: user, Email: "m.mueller@example.com", Self: true, Expires: s.Now.Add(24 * time.Hour), Lang: code, Zone: zone}, "sample-token"), nil
	case "email-notice":
		return emailchange.NoticeMail(t, emailchange.Facts{Base: s.Base, Username: user, Email: "m.mueller@example.com", Lang: code, Zone: zone}, ""), nil
	case "expiry":
		return users.ExpiryMail(t, code, zone, s.Base, user, week), nil
	case "inactive":
		return users.InactiveMail(t, code, zone, s.Base, user, 90, week), nil
	case "security-signin", "security-password", "security-two-factor", "security-email":
		all := securitymail.PreviewMails(t, code, user, s.Base, s.Now)
		return all[map[string]int{"security-signin": 0, "security-password": 1, "security-two-factor": 2, "security-email": 3}[kind]], nil
	case "impersonation":
		return impersonate.PreviewNotice(s.Bundle, code, s.Base, s.Now), nil
	case "chat":
		return chat.PreviewWaitMail(t, code, s.Base, s.Server, s.Now.Add(-15*time.Minute)), nil
	case "alert":
		return s.Mail.AlertMail(code,
			mail.Alert{Severity: jsbind.String("warning"), DeviceID: jsbind.String("123456789"), Message: jsbind.String("Disk C: is 93% full"), TriggeredAt: jsbind.String(s.Now.UTC().Format(time.RFC3339))},
			mail.AlertRule{Name: jsbind.String("Disk almost full"), ConditionType: jsbind.String("disk_usage"), ConditionOp: jsbind.String("gt"), ConditionValue: jsbind.Number(90)})
	case "help":
		return s.Mail.HelpRequestMail(code, mail.HelpRequest{Hostname: jsbind.String("Reception PC"), DeviceID: jsbind.String("123456789"), Message: jsbind.String("My printer stopped working after the update")}, jsbind.String("Front office"))
	case "test":
		return s.Mail.TestMail(ctx, code), nil
	}
	return brandmail.Mail{}, ErrUnknown
}
