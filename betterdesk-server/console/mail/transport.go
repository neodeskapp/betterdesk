package mail

import (
	"context"
	"io"
	"time"

	"github.com/unitronix/betterdesk-server/console/mail/message"
	"github.com/unitronix/betterdesk-server/console/mail/smtpwire"
)

// SMTP is the Transport of a running console: it builds the message
// (package message) and delivers it over a connection of its own (package
// smtpwire), which together is what nodemailer did for the console. The
// zero value works.
type SMTP struct {
	// Now is the clock of the Date header. Nil means time.Now.
	Now func() time.Time
	// Rand gives the random bytes of the MIME boundary and the
	// Message-ID. Nil means crypto/rand.
	Rand io.Reader
	// Wire holds what a connection needs besides the saved settings: the
	// dialer, the resolver, the machine's host name, the root
	// certificates and the timeouts. Its zero fields are nodemailer's
	// defaults. The six settings fields in it are not read.
	Wire smtpwire.Options
}

// options joins the injected parts of Wire with the saved settings.
func (t *SMTP) options(settings Settings) smtpwire.Options {
	opts := t.Wire
	opts.Host = settings.Host
	opts.Port = settings.Port
	opts.Secure = settings.Secure
	opts.User = settings.User
	opts.Pass = settings.Pass
	opts.TLSVerify = settings.TLSVerify
	return opts
}

// Send builds the message and delivers it. The message is built before
// anything is connected, so a mail that cannot be built opens no
// connection.
func (t *SMTP) Send(ctx context.Context, settings Settings, mail Outgoing) error {
	built, err := message.Build(message.Mail{
		From:    mail.From,
		To:      mail.To,
		Subject: mail.Subject,
		Text:    mail.Text,
		HTML:    mail.HTML,
	}, message.Options{Now: t.Now, Rand: t.Rand})
	if err != nil {
		return err
	}
	envelope := smtpwire.Envelope{From: built.EnvelopeFrom, To: built.EnvelopeTo}
	_, err = smtpwire.Send(ctx, t.options(settings), envelope, built.Raw)
	return err
}

// Verify connects, signs in when the settings have credentials, and
// leaves again.
func (t *SMTP) Verify(ctx context.Context, settings Settings) error {
	return smtpwire.Verify(ctx, t.options(settings))
}
