package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/smtpwire"
)

// Email is a mail for SendEmail: who gets it and what it says. The
// service lays it out (brandmail.Render) in the server's branding; there
// is no other way to send a mail.
type Email struct {
	// To is the recipients: one text (which may name several, separated
	// by commas), or a list with ToList set.
	To     []string
	ToList bool
	// Mail is what the mail says, in the reader's language.
	Mail brandmail.Mail
}

// RawEmail is a message with its parts as they are, for the tests that
// check the transport against nodemailer (SendUnbranded).
type RawEmail struct {
	To      []string
	ToList  bool
	Subject string
	// Text and HTML are the two bodies; nil leaves one out.
	Text *string
	HTML *string
	// Inline are the images the HTML shows by Content-ID.
	Inline []Inline
	// Lang goes into the Content-Language header; "" leaves it out.
	Lang string
}

// Inline is an image a mail's HTML shows by its Content-ID.
type Inline = message.Inline

// mailWords are the texts of the mails the service writes itself in one
// language: say gives the text of a key with its {name} placeholders
// filled from name, value pairs, or "" for a key the language files lack.
type mailWords struct {
	lang string
	say  func(key string, pairs ...string) string
}

// translate is say as a brandmail.Translate, the key itself where the
// language files lack it.
func (w mailWords) translate() brandmail.Translate {
	return func(key string, vars map[string]any) string {
		pairs := make([]string, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, fmt.Sprint(v))
		}
		if text := w.say(key, pairs...); text != "" {
			return text
		}
		return key
	}
}

// words are the console's default language, or English without language
// files.
func (s *Service) words() mailWords { return s.wordsIn("") }

// wordsIn are the texts of a language the console has, else of its
// default language, else English (englishMailWords).
func (s *Service) wordsIn(lang string) mailWords {
	var b *i18n.Bundle
	if s.opts.Texts != nil {
		b = s.opts.Texts()
	}
	if b == nil {
		return mailWords{lang: "en", say: func(key string, pairs ...string) string {
			text := englishMailWords[key]
			for i := 0; i+1 < len(pairs); i += 2 {
				text = strings.ReplaceAll(text, "{"+pairs[i]+"}", pairs[i+1])
			}
			return text
		}}
	}
	if lang == "" || !b.Has(lang) {
		lang = b.DefaultLang
	}
	say := func(key string, pairs ...string) string {
		vars := make([]any, len(pairs))
		for i, p := range pairs {
			vars[i] = p
		}
		text := b.Translate(lang, key, i18n.V(vars...))
		if text == key {
			return ""
		}
		return text
	}
	if l := say("meta.lang"); l != "" {
		lang = l
	}
	return mailWords{lang: lang, say: say}
}

// Translator gives the texts of a language for a mail, the console's
// default language for one it doesn't have, and its code.
func (s *Service) Translator(lang string) (brandmail.Translate, string) {
	w := s.wordsIn(lang)
	return w.translate(), w.lang
}

// brand is the console's branding for the mails the service writes.
func (s *Service) brand() brandmail.Branding { return s.Branding(context.Background()) }

// Branding is what every mail shows of the server: its name, accent and
// logo (Options.Branding), its host and who runs it (Contact).
func (s *Service) Branding(ctx context.Context) brandmail.Branding {
	b := brandmail.Branding{Server: brandmail.DefaultServer}
	if s.opts.Branding != nil {
		b = s.opts.Branding()
	}
	if b.Host == "" {
		b.Host = brandmail.HostOf(s.GetPanelBaseURL())
	}
	b.Contact = s.Contact(ctx)
	return b
}

// Reader is what the console knows of a mail's recipient by address: the
// language and time zone of the account with that address.
type Reader struct {
	Lang string
	Zone string
}

// ReaderOf is the reader at an address: the account's preferred language
// and time zone, empty for an address no account has.
func (s *Service) ReaderOf(ctx context.Context, addr string) Reader {
	var r Reader
	if s.opts.DB == nil || strings.TrimSpace(addr) == "" {
		return r
	}
	var id int64
	var lang sql.NullString
	if err := s.opts.DB.QueryRow(ctx, `SELECT id, preferred_language FROM users WHERE LOWER(email) = LOWER(?) ORDER BY id LIMIT 1`, strings.TrimSpace(addr)).Scan(&id, &lang); err != nil {
		return r
	}
	r.Lang = strings.TrimSpace(lang.String)
	var zone sql.NullString
	if err := s.opts.DB.QueryRow(ctx, `SELECT time_zone FROM account_profile WHERE user_id = ?`, id).Scan(&zone); err == nil {
		r.Zone = strings.TrimSpace(zone.String)
	}
	return r
}

// countRecipients counts the recipients for the log: the entries of a
// list that are not empty, or the parts of a text between commas that are
// more than white space.
func countRecipients(to []string, list bool) int {
	n := 0
	if list {
		for _, entry := range to {
			if entry != "" {
				n++
			}
		}
		return n
	}
	if len(to) == 0 {
		return 0
	}
	for _, part := range strings.Split(to[0], ",") {
		if jsbind.Trim(part) != "" {
			n++
		}
	}
	return n
}

// fromText is the sender nodemailer read from the configuration's from:
// the text as it is, a number or a flag as its text, a list as its
// entries with commas between them. The error is the one nodemailer
// failed with for a value it could not make text of.
func fromText(from value) (string, error) {
	notAFunction := errors.New(`(str || "").toString is not a function`)
	switch from.kind {
	case kindArray:
		parts := make([]string, 0, len(from.items))
		for _, item := range from.items {
			if !item.truthy() {
				continue
			}
			if item.kind == kindObject && item.ownToString {
				return "", notAFunction
			}
			text, err := item.toString()
			if err != nil {
				return "", err
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, ","), nil
	case kindObject:
		if from.ownToString {
			return "", notAFunction
		}
	}
	return from.toString()
}

// SendEmail lays out a mail in the server's branding and sends it with
// the configuration in use. It reports whether the mail server took it,
// and logs the outcome: how many recipients a sent message had, or why it
// failed, never an address.
//
// The sender is the configuration's from, else "neodesk@localhost", with
// the identity's display name (FromName) when it has none. Replies go to
// the mail's ReplyTo, else to the support address of the identity. A mail
// the server sends on its own says so (Auto-Submitted), and a
// notification goes to each recipient on its own with a link that turns
// it off (List-Unsubscribe); an address that turned it off gets none.
//
// A mail without a subject or a heading is refused. Without a
// configuration nothing is sent and the answer is false. A failed
// delivery is false with a log line; the error is always nil.
func (s *Service) SendEmail(ctx context.Context, m Email) (bool, error) {
	return s.sendBranded(ctx, m, nil)
}

// sendBranded is SendEmail. unreadable is the error nodemailer would fail
// with for recipients it could not make text of.
func (s *Service) sendBranded(ctx context.Context, m Email, unreadable error) (bool, error) {
	if strings.TrimSpace(m.Mail.Subject) == "" || strings.TrimSpace(m.Mail.Heading) == "" {
		s.log.Error("Send refused: a mail without subject or heading")
		return false, nil
	}
	t := s.transporterFor(ctx)
	if t == nil {
		s.log.Warn("Cannot send — no SMTP config")
		return false, nil
	}
	if m.Mail.T == nil {
		// The layout's own texts in the mail's language, else the default
		m.Mail.T, m.Mail.Lang = s.Translator(m.Mail.Lang)
	}
	b := s.Branding(ctx)
	if m.Mail.Unsubscribe == "" || unreadable != nil {
		return s.finish(ctx, t, m.To, m.ToList, brandmail.Render(m.Mail, b, ""), unreadable)
	}
	sent := false
	for _, addr := range splitRecipients(m.To, m.ToList) {
		if s.Unsubscribed(ctx, m.Mail.Unsubscribe, addr) {
			s.log.Info("Not sent: the recipient turned these mails off")
			continue
		}
		built := brandmail.Render(m.Mail, b, s.UnsubscribeURL(ctx, m.Mail.Unsubscribe, addr))
		if ok, _ := s.finish(ctx, t, []string{addr}, false, built, nil); ok {
			sent = true
		}
	}
	return sent, nil
}

// splitRecipients is every address of a mail's recipients, in order.
func splitRecipients(to []string, list bool) []string {
	var out []string
	for i, entry := range to {
		if i > 0 && !list {
			break
		}
		for _, part := range strings.Split(entry, ",") {
			if p := jsbind.Trim(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// finish sends a rendered mail and logs how it went.
func (s *Service) finish(ctx context.Context, t *transporter, to []string, list bool, built brandmail.Built, unreadable error) (bool, error) {
	err := unreadable
	if err == nil {
		err = s.sendBuilt(ctx, t, to, list, built)
	}
	if err != nil {
		s.log.Error("Send failed: " + err.Error())
		return false, nil
	}
	s.log.Info("Sent message to " + strconv.Itoa(countRecipients(to, list)) + " recipient(s)")
	return true, nil
}

// errNotRendered refuses a mail that brandmail did not lay out.
var errNotRendered = errors.New("the mail was not laid out by brandmail")

// sendBuilt hands a rendered mail to the transport.
func (s *Service) sendBuilt(ctx context.Context, t *transporter, to []string, list bool, built brandmail.Built) error {
	if !built.Rendered() {
		return errNotRendered
	}
	if t.refusal != nil {
		return t.refusal
	}
	sender, err := s.sender(t)
	if err != nil {
		return err
	}
	b := s.Branding(ctx)
	id := s.LoadIdentity(ctx)
	replyTo := strings.TrimSpace(built.ReplyTo)
	if replyTo == "" || !plainAddress(replyTo) {
		replyTo = b.Contact.Email
	}
	var headers []message.Header
	if built.Automatic {
		headers = append(headers, message.Header{Name: "Auto-Submitted", Value: "auto-generated"})
	}
	if built.UnsubscribeURL != "" {
		headers = append(headers,
			message.Header{Name: "List-Unsubscribe", Value: "<" + built.UnsubscribeURL + ">"},
			message.Header{Name: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"})
	}
	text, html := built.Text, built.HTML
	return s.opts.Transport.Send(ctx, t.settings, Outgoing{
		From:            withName(wellFormed(sender), FromName(id, b)),
		To:              to,
		ToList:          list,
		Subject:         built.Subject,
		Text:            &text,
		HTML:            &html,
		Inline:          built.Inline,
		Lang:            built.Lang,
		ReplyTo:         replyTo,
		Headers:         headers,
		MessageIDDomain: messageIDDomain(sender, s.GetPanelBaseURL()),
	})
}

// messageIDDomain is the Message-ID's domain: "" (the sender's) unless
// the sender's is no domain anyone owns (localhost, a bare name), then the
// console's public host.
func messageIDDomain(sender, base string) string {
	addr := sender
	if i := strings.LastIndexByte(addr, '<'); i >= 0 {
		addr = strings.TrimSuffix(addr[i+1:], ">")
	}
	domain := strings.ToLower(strings.TrimSpace(addr[strings.LastIndexByte(addr, '@')+1:]))
	if strings.Contains(domain, ".") && domain != "localhost" && !strings.HasSuffix(domain, ".localhost") {
		return ""
	}
	host := brandmail.HostOf(base)
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	return host
}

// sender is the configuration's from, else the default.
func (s *Service) sender(t *transporter) (string, error) {
	from := t.config.get("from")
	if !from.truthy() {
		from = stringValue(defaultFrom)
	}
	return fromText(from)
}

// SendUnbranded sends a message with its parts as they are, as the
// console handed them to nodemailer. It exists for the tests that compare
// the transport with nodemailer's; the console's code sends with
// SendEmail, and TestNothingSendsUnbranded keeps it that way.
func (s *Service) SendUnbranded(ctx context.Context, m RawEmail) (bool, error) {
	return s.deliverRaw(ctx, m, nil)
}

// deliverRaw sends a message as it is. unreadable is as for sendBranded.
func (s *Service) deliverRaw(ctx context.Context, m RawEmail, unreadable error) (bool, error) {
	t := s.transporterFor(ctx)
	if t == nil {
		s.log.Warn("Cannot send — no SMTP config")
		return false, nil
	}
	err := unreadable
	if err == nil {
		err = s.sendRaw(ctx, t, m)
	}
	if err != nil {
		s.log.Error("Send failed: " + err.Error())
		return false, nil
	}
	s.log.Info("Sent message to " + strconv.Itoa(countRecipients(m.To, m.ToList)) + " recipient(s)")
	return true, nil
}

func (s *Service) sendRaw(ctx context.Context, t *transporter, m RawEmail) error {
	if t.refusal != nil {
		return t.refusal
	}
	sender, err := s.sender(t)
	if err != nil {
		return err
	}
	return s.opts.Transport.Send(ctx, t.settings, Outgoing{
		From:    wellFormed(sender),
		To:      m.To,
		ToList:  m.ToList,
		Subject: m.Subject,
		Text:    m.Text,
		HTML:    m.HTML,
		Inline:  m.Inline,
		Lang:    m.Lang,
	})
}

// TestResult is the answer of a connection test. As JSON it is
// {"success":true}, or {"success":false,"error":"..."} with the mail
// server's or nodemailer's message.
type TestResult struct {
	Success bool
	// Error is the reason of a failure, nil on success.
	Error *string
}

// MarshalJSON implements json.Marshaler.
func (r TestResult) MarshalJSON() ([]byte, error) {
	buf := strconv.AppendBool([]byte(`{"success":`), r.Success)
	if r.Error != nil {
		buf = appendQuoted(append(buf, `,"error":`...), *r.Error)
	}
	return append(buf, '}'), nil
}

func failedTest(message string) TestResult { return TestResult{Error: &message} }

// noSMTPGreeting is the answer of a connection test whose server did not
// greet as a mail server does: the line it sent goes to the log only, so
// the test reads no other service's banner.
const noSMTPGreeting = "Invalid greeting: the server did not answer as an SMTP server"

// smtpReply is the start of an SMTP reply line: a code from 200 to 599,
// then the end of the line, a space or a hyphen. A server that greets
// with one (421 busy, 554 no service) greeted as a mail server, and the
// test shows its reply.
var smtpReply = regexp.MustCompile(`^[2-5][0-9]{2}([ -]|$)`)

// TestConnection connects to the mail server of the configuration in use
// and signs in, without sending anything. Without a configuration the
// answer is the error "No SMTP configuration", and for a stored
// configuration that is invalid "Invalid SMTP configuration". A server
// whose first lines are no SMTP replies is answered with noSMTPGreeting.
//
// A failed test is a TestResult; the error is always nil.
func (s *Service) TestConnection(ctx context.Context) (TestResult, error) {
	t := s.transporterFor(ctx)
	if t == nil {
		return failedTest("No SMTP configuration"), nil
	}
	if t.refusal != nil {
		return failedTest(t.refusal.Error()), nil
	}
	if err := s.opts.Transport.Verify(ctx, t.settings); err != nil {
		var wire *smtpwire.Error
		if errors.As(err, &wire) && wire.Code == "EPROTOCOL" && wire.Command == "CONN" && wire.Response != "" && !smtpReply.MatchString(wire.Response) {
			s.log.Warn("SMTP test: the server did not greet as an SMTP server:", wire.Message)
			return failedTest(noSMTPGreeting), nil
		}
		return failedTest(err.Error()), nil
	}
	return TestResult{Success: true}, nil
}

// GetPanelBaseURL returns the address people reach the console at, for
// links in mails: PANEL_PUBLIC_URL, else PUBLIC_URL, without one trailing
// slash; else the panel's public host (PanelHost), with https and
// HTTPSPort when the console serves HTTPS and http and Port when it does
// not, where 443 and 80 are left out; else "", for no link.
func (s *Service) GetPanelBaseURL() string {
	explicit := s.getenv("PANEL_PUBLIC_URL")
	if explicit == "" {
		explicit = s.getenv("PUBLIC_URL")
	}
	if explicit != "" {
		return strings.TrimSuffix(explicit, "/")
	}
	host := ""
	if s.opts.PanelHost != nil {
		host = publicHost(s.opts.PanelHost())
	}
	return s.consoleAt(host)
}

// PanelBaseURLFor is GetPanelBaseURL, but where that is "" the console at
// host (an IP address or a host name; anything else gives "") with the
// console's scheme and port. The desktop sign-in through the console
// (DECISIONS A276) gives the host name the desktop app reached the server
// at.
func (s *Service) PanelBaseURLFor(host string) string {
	if base := s.GetPanelBaseURL(); base != "" {
		return base
	}
	return s.consoleAt(publicHost(host))
}

// consoleAt is the console's address at host, which publicHost checked,
// with the scheme and port it serves; "" for no host.
func (s *Service) consoleAt(host string) string {
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	scheme, port, standard := "http", s.opts.Port, 80
	if s.opts.ServesHTTPS != nil && s.opts.ServesHTTPS() {
		scheme, port, standard = "https", s.opts.HTTPSPort, 443
	}
	if port > 0 && port != standard {
		host += ":" + strconv.Itoa(port)
	}
	return scheme + "://" + host
}

// hostName is a host name of ASCII letters, digits and inner hyphens in
// labels joined by dots, in lower case.
var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// publicHost is the host a PANEL_PUBLIC_HOST value names, in lower case and
// without brackets, or "" when it names none: a value written as a URL
// gives the URL's host; then it has to be an IP address or a host name.
func publicHost(value string) string {
	value = jsbind.Trim(value)
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil {
			return ""
		}
		value = u.Hostname()
	}
	value = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if addr, err := netip.ParseAddr(value); err == nil && addr.Zone() == "" {
		return addr.String()
	}
	if len(value) > 253 || !hostName.MatchString(value) {
		return ""
	}
	return value
}

// escapeHTML is the console's escapeHtml: &, <, > and the double quote.
// The single quote stays.
var escapeHTML = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace
