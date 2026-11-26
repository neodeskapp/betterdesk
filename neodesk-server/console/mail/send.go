package mail

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/smtpwire"
)

// Email is a message for SendEmail.
type Email struct {
	// To is the recipients: one text (which may name several, separated
	// by commas), or a list with ToList set.
	To     []string
	ToList bool
	// Subject is the subject line.
	Subject string
	// Text and HTML are the two bodies; nil leaves one out.
	Text *string
	HTML *string
	// Inline are the images the HTML shows by Content-ID (the logo of
	// brandmail).
	Inline []Inline
	// Lang is the language the mail is written in ("de"), which goes into
	// its Content-Language header; "" leaves the header out.
	Lang string
}

// Inline is an image a mail's HTML shows by its Content-ID.
type Inline = message.Inline

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

// SendEmail sends one message with the configuration in use and reports
// whether the mail server took it. It logs the outcome: how many
// recipients a sent message had, or why it failed, never an address.
//
// The sender is the configuration's from, else "neodesk@localhost".
// Without a configuration nothing is sent and the answer is false.
//
// A failed delivery is false with a log line, also for a stored
// configuration that is invalid; the error is always nil.
func (s *Service) SendEmail(ctx context.Context, m Email) (bool, error) {
	return s.deliver(ctx, m, nil)
}

// deliver is SendEmail. unreadable is the error nodemailer would fail with
// while building the message, for recipients it could not make text of.
func (s *Service) deliver(ctx context.Context, m Email, unreadable error) (bool, error) {
	t := s.transporterFor(ctx)
	if t == nil {
		s.log.Warn("Cannot send — no SMTP config")
		return false, nil
	}
	err := unreadable
	if err == nil {
		err = s.send(ctx, t, m)
	}
	if err != nil {
		s.log.Error("Send failed: " + err.Error())
		return false, nil
	}
	s.log.Info("Sent message to " + strconv.Itoa(countRecipients(m.To, m.ToList)) + " recipient(s)")
	return true, nil
}

func (s *Service) send(ctx context.Context, t *transporter, m Email) error {
	if t.refusal != nil {
		return t.refusal
	}
	from := t.config.get("from")
	if !from.truthy() {
		from = stringValue(defaultFrom)
	}
	sender, err := fromText(from)
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

// Alert is the alert an alert mail is about. The members are JavaScript
// values because the mail prints whatever they hold: a rule's row can
// carry any type on SQLite.
type Alert struct {
	Severity    jsbind.Value
	DeviceID    jsbind.Value
	Message     jsbind.Value
	TriggeredAt jsbind.Value
}

// AlertRule is the alert rule an alert mail is about: the columns of its
// row the mail reads.
type AlertRule struct {
	Name           jsbind.Value
	Description    jsbind.Value
	ConditionType  jsbind.Value
	ConditionOp    jsbind.Value
	ConditionValue jsbind.Value
	// NotifyEmails is who gets the mail: text with one or more addresses.
	NotifyEmails jsbind.Value
}

// upper is JavaScript's toUpperCase: every character's upper case, also
// where that is more than one character (the sharp s becomes SS).
//
// Node's tables are newer than the ones of golang.org/x/text: the letters
// of upperNewer got their capitals later.
func upper(s string) string {
	s = cases.Upper(language.Und).String(s)
	if strings.IndexFunc(s, func(r rune) bool { return upperNewer(r) != r }) < 0 {
		return s
	}
	return strings.Map(upperNewer, s)
}

// upperNewer is the capital of a small letter that Unicode 16 and 17 gave
// one: a few Latin and Georgian letters, and the Garay and Beria Erfe
// alphabets.
func upperNewer(r rune) rune {
	switch {
	case r >= 0x10D70 && r <= 0x10D85:
		return r - 0x20
	case r >= 0x16EBB && r <= 0x16ED3:
		return r - 0x1B
	}
	switch r {
	case 0x019B:
		return 0xA7DC
	case 0x0264:
		return 0xA7CB
	case 0x1C8A, 0xA7CD, 0xA7CF, 0xA7D3, 0xA7D5, 0xA7DB:
		return r - 1
	}
	return r
}

// texts turns values into text one after the other and remembers the
// first that fails.
type texts struct{ err error }

func (t *texts) of(v jsbind.Value) string {
	if t.err != nil {
		return ""
	}
	text, err := fromBind(v).toString()
	if err != nil {
		t.err = err
		return ""
	}
	return text
}

// or is `v || fallback` as text.
func (t *texts) or(v jsbind.Value, fallback string) string {
	if !v.Truthy() {
		return fallback
	}
	return t.of(v)
}

// alertMail builds subject, text and HTML of an alert mail. The error is
// the TypeError the console threw: for a severity that is no text, and
// for a value it could not make text of.
func alertMail(alert Alert, rule AlertRule) (subject, text, html string, err error) {
	var t texts
	name := t.of(rule.Name)
	if t.err != nil {
		return "", "", "", t.err
	}
	switch {
	case alert.Severity.IsUndefined():
		return "", "", "", errors.New("Cannot read properties of undefined (reading 'toUpperCase')")
	case alert.Severity.IsNull():
		return "", "", "", errors.New("Cannot read properties of null (reading 'toUpperCase')")
	case !alert.Severity.IsString():
		return "", "", "", errors.New("alert.severity.toUpperCase is not a function")
	}
	severity := alert.Severity.Str()
	device := t.or(alert.DeviceID, "N/A")
	message := t.of(alert.Message)
	triggered := t.of(alert.TriggeredAt)
	about := t.or(rule.Description, "")
	if !rule.Description.Truthy() {
		about = t.of(rule.ConditionType)
	}
	op := t.of(rule.ConditionOp)
	threshold := t.of(rule.ConditionValue)
	description := t.or(rule.Description, "")
	if t.err != nil {
		return "", "", "", t.err
	}

	subject = "[NeoDesk Alert] " + name + " — " + upper(severity)
	text = strings.Join([]string{
		"Alert: " + name,
		"Severity: " + severity,
		"Device: " + device,
		"Message: " + message,
		"Triggered at: " + triggered,
		"",
		"Rule: " + about + " " + op + " " + threshold,
	}, "\n")
	html = `
        <div style="font-family: sans-serif; max-width: 600px;">
            <h2 style="color: #e74c3c;">NeoDesk Alert: ` + escapeHTML(name) + `</h2>
            <table style="border-collapse: collapse; width: 100%;">
                <tr><td style="padding: 8px; border-bottom: 1px solid #eee; font-weight: bold;">Severity</td>
                    <td style="padding: 8px; border-bottom: 1px solid #eee;">` + escapeHTML(severity) + `</td></tr>
                <tr><td style="padding: 8px; border-bottom: 1px solid #eee; font-weight: bold;">Device</td>
                    <td style="padding: 8px; border-bottom: 1px solid #eee;">` + escapeHTML(device) + `</td></tr>
                <tr><td style="padding: 8px; border-bottom: 1px solid #eee; font-weight: bold;">Message</td>
                    <td style="padding: 8px; border-bottom: 1px solid #eee;">` + escapeHTML(message) + `</td></tr>
                <tr><td style="padding: 8px; font-weight: bold;">Time</td>
                    <td style="padding: 8px;">` + escapeHTML(triggered) + `</td></tr>
            </table>
            <p style="color: #888; font-size: 12px; margin-top: 16px;">
                Rule: ` + escapeHTML(description) + `
            </p>
        </div>`
	return subject, text, html, nil
}

// recipients reads who a mail goes to from a value: text as it is, a list
// as its entries, anything else as its text.
func recipients(v jsbind.Value) (to []string, list bool, err error) {
	if v.IsArray() {
		to = make([]string, 0, len(v.Items()))
		for _, item := range v.Items() {
			text, err := fromBind(item).toString()
			if err != nil {
				return nil, false, err
			}
			if item.IsUndefined() || item.IsNull() {
				text = ""
			}
			to = append(to, text)
		}
		return to, true, nil
	}
	text, err := fromBind(v).toString()
	if err != nil {
		return nil, false, err
	}
	return []string{text}, false, nil
}

// SendAlertEmail sends the mail for an alert to the rule's notify_emails
// and reports whether it was sent. A rule without recipients sends
// nothing and answers false.
//
// The mail is built before the recipients are looked at, so the errors
// come first: the TypeError the console threw for an alert whose severity
// is no text, or for a value it could not make text of. Rows of the
// console's own tables never cause one.
func (s *Service) SendAlertEmail(ctx context.Context, alert Alert, rule AlertRule) (bool, error) {
	subject, text, html, err := alertMail(alert, rule)
	if err != nil {
		return false, err
	}
	if !rule.NotifyEmails.Truthy() {
		return false, nil
	}
	to, list, unreadable := recipients(rule.NotifyEmails)
	return s.deliver(ctx, Email{To: to, ToList: list, Subject: subject, Text: &text, HTML: &html}, unreadable)
}

// HelpRequest is the help request a notification mail is about.
type HelpRequest struct {
	Hostname jsbind.Value
	DeviceID jsbind.Value
	Message  jsbind.Value
}

// HelpRequestEmail is a help request notification for SendHelpRequestEmail.
type HelpRequestEmail struct {
	// To and ToList are the recipients, as in Email.
	To     []string
	ToList bool
	// HelpRequest is the request.
	HelpRequest HelpRequest
	// FolderName is the device's folder. When set it goes into the
	// subject and the body.
	FolderName jsbind.Value
}

// helpRequestMail builds subject, text and HTML of a help request mail.
// Without a baseURL the mail has no link to the console.
func helpRequestMail(help HelpRequest, folderName jsbind.Value, baseURL string) (subject, text, html string, err error) {
	var t texts
	hostname := "Unknown device"
	switch {
	case help.Hostname.Truthy():
		hostname = t.of(help.Hostname)
	case help.DeviceID.Truthy():
		hostname = t.of(help.DeviceID)
	}
	message := t.or(help.Message, "")
	link := baseURL + "/help-requests"
	hasDevice, hasFolder := help.DeviceID.Truthy(), folderName.Truthy()
	deviceID := t.or(help.DeviceID, "")
	folder := t.or(folderName, "")
	if t.err != nil {
		return "", "", "", t.err
	}

	if hasFolder {
		subject = "[" + folder + "] "
	}
	subject += "Help request — " + hostname

	// Blank lines set the parts apart; the device id and the folder have a
	// line only when they are set
	lines := []string{"A new help request was submitted.", "", "Device: " + hostname}
	if hasDevice {
		lines = append(lines, "Device ID: "+deviceID)
	}
	if hasFolder {
		lines = append(lines, "Folder: "+folder)
	}
	lines = append(lines, "", "Message: "+message)
	linkRow := ""
	if baseURL != "" {
		lines = append(lines, "", "Open in panel: "+link)
		linkRow = `
            <p style="margin-top: 16px;"><a href="` + escapeHTML(link) + `">Open help requests in NeoDesk</a></p>`
	}
	text = strings.Join(lines, "\n")

	deviceRow, folderRow := "", ""
	if hasDevice {
		deviceRow = `<tr><td style="padding: 8px; border-bottom: 1px solid #eee; font-weight: bold;">Device ID</td>
                    <td style="padding: 8px; border-bottom: 1px solid #eee;">` + escapeHTML(deviceID) + `</td></tr>`
	}
	if hasFolder {
		folderRow = `<tr><td style="padding: 8px; border-bottom: 1px solid #eee; font-weight: bold;">Folder</td>
                    <td style="padding: 8px; border-bottom: 1px solid #eee;">` + escapeHTML(folder) + `</td></tr>`
	}
	html = `
        <div style="font-family: sans-serif; max-width: 600px;">
            <h2 style="color: #2563eb;">Help request — ` + escapeHTML(hostname) + `</h2>
            <table style="border-collapse: collapse; width: 100%;">
                ` + deviceRow + `
                ` + folderRow + `
                <tr><td style="padding: 8px; font-weight: bold;">Message</td>
                    <td style="padding: 8px;">` + escapeHTML(message) + `</td></tr>
            </table>` + linkRow + `
        </div>`
	return subject, text, html, nil
}

// SendHelpRequestEmail sends the notification for a help request and
// reports whether it was sent. The link in it is GetPanelBaseURL with
// /help-requests; without that address the mail has no link. The error is
// as for SendAlertEmail.
func (s *Service) SendHelpRequestEmail(ctx context.Context, m HelpRequestEmail) (bool, error) {
	subject, text, html, err := helpRequestMail(m.HelpRequest, m.FolderName, s.GetPanelBaseURL())
	if err != nil {
		return false, err
	}
	return s.SendEmail(ctx, Email{To: m.To, ToList: m.ToList, Subject: subject, Text: &text, HTML: &html})
}
