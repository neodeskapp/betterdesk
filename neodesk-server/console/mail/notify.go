package mail

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// englishMailWords are the texts of the mails this package writes as
// en.json has them, for a service without language files.
var englishMailWords = map[string]string{
	"meta.lang":                        "en",
	"automation.severity":              "Severity",
	"automation.severity_info":         "Info",
	"automation.severity_warning":      "Warning",
	"automation.severity_critical":     "Critical",
	"automation.device":                "Device",
	"automation.device_id":             "Device ID",
	"automation.triggered_at":          "Triggered",
	"automation.condition_type":        "Condition",
	"automation.description":           "Description",
	"automation.op_gt":                 "Greater than",
	"automation.op_gte":                "Greater or equal",
	"automation.op_lt":                 "Less than",
	"automation.op_lte":                "Less or equal",
	"automation.op_eq":                 "Equal to",
	"automation.op_neq":                "Not equal to",
	"automation.type_cpu_usage":        "CPU use (%)",
	"automation.type_memory_usage":     "Memory use (%)",
	"automation.type_disk_usage":       "Disk use (%)",
	"automation.type_offline_duration": "Time offline (seconds)",
	"automation.type_idle_duration":    "Time idle (seconds)",
	"automation.type_custom":           "Custom",
	"help_request.message":             "Message",
	"device_detail.folder":             "Folder",
	"mail.link_hint":                   "If the button doesn't work, copy this link into your browser:",
	"mail.unsubscribe":                 "Turn these mails off",
	"mail.footer_sent_named":           "Sent by {server} at {host}.",
	"mail.footer_sent_named_nohost":    "Sent by {server}.",
	"mail.footer_sent_default":         "Sent by the server at {host}, which runs NeoDesk.",
	"mail.footer_sent_default_nohost":  "Sent by a server that runs NeoDesk.",
	"mail.footer_run_by":               "{organisation} runs this server.",
	"mail.footer_contact_email":        "Questions? Write to {email}.",
	"mail.footer_contact_url":          "Questions? Go to {url}.",
	"mail.footer_contact_both":         "Questions? Write to {email} or go to {url}.",
	"mail.alert.subject":               "Alert from {server}: {rule}",
	"mail.alert.preheader":             "{severity} on {device}: {message}",
	"mail.alert.heading":               "{rule} on {device}",
	"mail.alert.lead":                  "The alert rule {rule} went off for {device}. Check the device in the console.",
	"mail.alert.button":                "Open the device",
	"mail.alert.button_alerts":         "Open the alerts",
	"mail.alert.why":                   "You get this mail because your address is on the alert rule {rule}.",
	"mail.alert.turn_off":              "The link below turns off every alert mail to this address. An admin can also take your address off the rule.",
	"mail.help.subject":                "{server}: {device} asks for help",
	"mail.help.preheader":              "{message}",
	"mail.help.preheader_empty":        "No message. Open the request to take it on.",
	"mail.help.heading":                "{device} asks for help",
	"mail.help.lead":                   "Someone at {device} sent a help request. Open it to take it on.",
	"mail.help.button":                 "Open the help requests",
	"mail.help.why_operator":           "You get this mail because you look after this device's folder or group.",
	"mail.help.why_alert":              "You get this mail because this is the alert address in the mail settings.",
	"mail.help.turn_off":               "The link below turns off help request mails to this address.",
	"mail.help.turn_off_nolink":        "An admin can change who gets these mails under Billing, Email notifications.",
	"mail.alert.turn_off_nolink":       "An admin can take your address off the alert rule.",
	"mail.test.subject":                "Test mail from {server}",
	"mail.test.preheader":              "The SMTP settings in the console work.",
	"mail.test.heading":                "Your mail settings work",
	"mail.test.lead":                   "This server sent this mail with the SMTP settings saved in the console. If you can read it, its mails reach your inbox.",
	"mail.test.lead_named":             "{server} sent this mail with the SMTP settings saved in the console. If you can read it, its mails reach your inbox.",
	"mail.test.button":                 "Open the mail settings",
	"mail.test.details":                "Check that the sender shows as {from} and that the logo and colours look right in light and dark mode.",
	"mail.test.why":                    "You get this mail because you sent it to yourself from the mail settings.",
}

// opNames are the alert rules' comparisons, by the names the rules store
// and by the signs older rules have.
var opNames = map[string]string{"gt": "gt", "gte": "gte", "lt": "lt", "lte": "lte", "eq": "eq", "neq": "neq",
	">": "gt", ">=": "gte", "<": "lt", "<=": "lte", "==": "eq", "=": "eq", "!=": "neq"}

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

// AlertFacts is where an alert mail goes and how it names things.
type AlertFacts struct {
	// Server is the server's name, Base the console's address ("" for no
	// link).
	Server, Base string
	// Zone is where the reader's times are said (brandmail.Zone).
	Zone *time.Location
}

// alertMail writes an alert mail in the words' language: who sends it
// (the server's name in the subject), the rule and the device as the
// heading, one button to the device, then severity, device, message, the
// time in the reader's zone and the condition in words as a table, and
// the rule's description. The error is the TypeError the Node console
// threw: for a severity that is no text, and for a value it could not
// make text of.
func alertMail(alert Alert, rule AlertRule, w mailWords, f AlertFacts) (brandmail.Mail, error) {
	var t texts
	name := t.of(rule.Name)
	if t.err != nil {
		return brandmail.Mail{}, t.err
	}
	switch {
	case alert.Severity.IsUndefined():
		return brandmail.Mail{}, errors.New("Cannot read properties of undefined (reading 'toUpperCase')")
	case alert.Severity.IsNull():
		return brandmail.Mail{}, errors.New("Cannot read properties of null (reading 'toUpperCase')")
	case !alert.Severity.IsString():
		return brandmail.Mail{}, errors.New("alert.severity.toUpperCase is not a function")
	}
	severity := alert.Severity.Str()
	deviceID := t.or(alert.DeviceID, "")
	device := deviceID
	if device == "" {
		device = "N/A"
	}
	message := t.of(alert.Message)
	triggered := t.of(alert.TriggeredAt)
	// Without a description the rule's measure has to be text, as the
	// Node console printed it; with one, a measure that is no text is
	// left out
	measure := ""
	if !rule.Description.Truthy() {
		measure = t.of(rule.ConditionType)
	}
	op := t.of(rule.ConditionOp)
	threshold := t.of(rule.ConditionValue)
	description := t.or(rule.Description, "")
	if t.err != nil {
		return brandmail.Mail{}, t.err
	}

	if rule.Description.Truthy() {
		measure = (&texts{}).of(rule.ConditionType)
	}
	if word := w.say("automation.severity_" + strings.ToLower(severity)); word != "" {
		severity = word
	}
	if at, err := time.Parse(time.RFC3339Nano, triggered); err == nil {
		triggered = brandmail.DateTime(at, w.lang, f.Zone)
	}
	if word := w.say("automation.type_" + measure); word != "" {
		measure = word
	}
	if word := w.say("automation.op_" + opNames[op]); opNames[op] != "" && word != "" {
		op = word
	}
	vars := []string{"rule", name, "device", device, "severity", severity, "message", message, "server", f.Server}
	m := brandmail.Mail{
		Kind: "alert", T: w.translate(), Lang: w.lang, Automatic: true, Unsubscribe: NotifyAlert,
		Subject:   w.say("mail.alert.subject", vars...),
		Preheader: strings.TrimSuffix(strings.TrimSpace(w.say("mail.alert.preheader", vars...)), ":"),
		Heading:   w.say("mail.alert.heading", vars...),
		Lead:      w.say("mail.alert.lead", vars...),
		Facts: []brandmail.Fact{
			{Label: w.say("automation.severity"), Value: severity},
			{Label: w.say("automation.device"), Value: device},
			{Label: w.say("help_request.message"), Value: message},
			{Label: w.say("automation.triggered_at"), Value: triggered},
			{Label: w.say("automation.condition_type"), Value: strings.TrimSpace(measure + " " + op + " " + threshold)},
		},
		Why:     w.say("mail.alert.why", vars...),
		TurnOff: w.say("mail.alert.turn_off", vars...), TurnOffNoLink: w.say("mail.alert.turn_off_nolink", vars...),
	}
	if description != "" {
		m.Details = []string{description}
	}
	if f.Base != "" {
		if deviceID != "" {
			m.Button, m.Link = w.say("mail.alert.button", vars...), f.Base+"/devices?open="+url.QueryEscape(deviceID)
		} else {
			m.Button, m.Link = w.say("mail.alert.button_alerts", vars...), f.Base+"/automation"
		}
	}
	return m, nil
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

// SendAlertEmail sends the mail for an alert to each address of the
// rule's notify_emails, each in the language and time zone of the
// account with that address, and reports whether any went out. A rule
// without recipients sends nothing and answers false.
//
// The mail is built before the recipients are looked at, so the errors
// come first: the TypeError the Node console threw for an alert whose
// severity is no text, or for a value it could not make text of. Rows of
// the console's own tables never cause one.
func (s *Service) SendAlertEmail(ctx context.Context, alert Alert, rule AlertRule) (bool, error) {
	brand := s.Branding(ctx)
	facts := AlertFacts{Server: brand.Name(), Base: s.GetPanelBaseURL(), Zone: time.Local}
	first, err := alertMail(alert, rule, s.words(), facts)
	if err != nil {
		return false, err
	}
	if !rule.NotifyEmails.Truthy() {
		return false, nil
	}
	to, list, unreadable := recipients(rule.NotifyEmails)
	if unreadable != nil {
		return s.sendBranded(ctx, Email{To: to, ToList: list, Mail: first}, unreadable)
	}
	sent := false
	for _, addr := range splitRecipients(to, list) {
		reader := s.ReaderOf(ctx, addr)
		f := facts
		f.Zone = brandmail.Zone(reader.Zone)
		m, err := alertMail(alert, rule, s.wordsIn(reader.Lang), f)
		if err != nil {
			return sent, err
		}
		if ok, _ := s.sendBranded(ctx, Email{To: []string{addr}, Mail: m}, nil); ok {
			sent = true
		}
	}
	return sent, nil
}

// AlertMail is an alert mail in a language, for the previews.
func (s *Service) AlertMail(lang string, alert Alert, rule AlertRule) (brandmail.Mail, error) {
	brand := s.brand()
	return alertMail(alert, rule, s.wordsIn(lang), AlertFacts{Server: brand.Name(), Base: s.GetPanelBaseURL(), Zone: time.Local})
}

// HelpRequest is the help request a notification mail is about.
type HelpRequest struct {
	Hostname jsbind.Value
	DeviceID jsbind.Value
	Message  jsbind.Value
}

// Why a help request mail goes to someone (HelpRequestEmail.Reason).
const (
	HelpForOperator = "operator" // they look after the device's folder or group
	HelpForAlert    = "alert"    // the alert address of the SMTP settings
)

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
	// Reason is why the recipients get it: HelpForOperator (the default)
	// or HelpForAlert.
	Reason string
}

// helpRequestMail writes a help request mail in the words' language: the
// server and the device in the subject, the message as the preheader, a
// button to the console's help requests, and the device, its ID, its
// folder and the message as a table. Without a baseURL the mail has no
// button.
func helpRequestMail(help HelpRequest, folderName jsbind.Value, reason, baseURL, server string, w mailWords) (brandmail.Mail, error) {
	var t texts
	hostname := "Unknown device"
	switch {
	case help.Hostname.Truthy():
		hostname = t.of(help.Hostname)
	case help.DeviceID.Truthy():
		hostname = t.of(help.DeviceID)
	}
	message := t.or(help.Message, "")
	hasDevice, hasFolder := help.DeviceID.Truthy(), folderName.Truthy()
	deviceID := t.or(help.DeviceID, "")
	folder := t.or(folderName, "")
	if t.err != nil {
		return brandmail.Mail{}, t.err
	}
	vars := []string{"device", hostname, "server", server, "message", strings.Join(strings.Fields(message), " ")}
	subject := w.say("mail.help.subject", vars...)
	if hasFolder {
		subject = "[" + folder + "] " + subject
	}
	preheader := w.say("mail.help.preheader", vars...)
	if strings.TrimSpace(message) == "" {
		preheader = w.say("mail.help.preheader_empty", vars...)
	}
	facts := []brandmail.Fact{{Label: w.say("automation.device"), Value: hostname}}
	if hasDevice {
		facts = append(facts, brandmail.Fact{Label: w.say("automation.device_id"), Value: deviceID})
	}
	if hasFolder {
		facts = append(facts, brandmail.Fact{Label: w.say("device_detail.folder"), Value: folder})
	}
	facts = append(facts, brandmail.Fact{Label: w.say("help_request.message"), Value: message})
	why := w.say("mail.help.why_operator", vars...)
	if reason == HelpForAlert {
		why = w.say("mail.help.why_alert", vars...)
	}
	m := brandmail.Mail{
		Kind: "help-request", T: w.translate(), Lang: w.lang, Automatic: true, Unsubscribe: NotifyHelp,
		Subject: subject, Preheader: preheader,
		Heading: w.say("mail.help.heading", vars...), Lead: w.say("mail.help.lead", vars...),
		Facts: facts, Why: why, TurnOff: w.say("mail.help.turn_off", vars...), TurnOffNoLink: w.say("mail.help.turn_off_nolink", vars...),
	}
	if baseURL != "" {
		m.Button, m.Link = w.say("mail.help.button", vars...), baseURL+"/help-requests"
	}
	return m, nil
}

// SendHelpRequestEmail sends the notification for a help request to each
// recipient, in the language of the account with that address, and
// reports whether any went out. The link in it is GetPanelBaseURL with
// /help-requests; without that address the mail has no button. The error
// is the TypeError of a value that is no text.
func (s *Service) SendHelpRequestEmail(ctx context.Context, m HelpRequestEmail) (bool, error) {
	base, server := s.GetPanelBaseURL(), s.Branding(ctx).Name()
	first, err := helpRequestMail(m.HelpRequest, m.FolderName, m.Reason, base, server, s.words())
	if err != nil {
		return false, err
	}
	sent := false
	addrs := splitRecipients(m.To, m.ToList)
	if len(addrs) == 0 {
		return s.sendBranded(ctx, Email{To: m.To, ToList: m.ToList, Mail: first}, nil)
	}
	for _, addr := range addrs {
		mail, err := helpRequestMail(m.HelpRequest, m.FolderName, m.Reason, base, server, s.wordsIn(s.ReaderOf(ctx, addr).Lang))
		if err != nil {
			return sent, err
		}
		if ok, _ := s.sendBranded(ctx, Email{To: []string{addr}, Mail: mail}, nil); ok {
			sent = true
		}
	}
	return sent, nil
}

// HelpRequestMail is a help request mail in a language, for the previews.
func (s *Service) HelpRequestMail(lang string, help HelpRequest, folder jsbind.Value) (brandmail.Mail, error) {
	return helpRequestMail(help, folder, HelpForOperator, s.GetPanelBaseURL(), s.brand().Name(), s.wordsIn(lang))
}

// TestMail is the mail of the mail settings' test: it says the settings
// work and asks the admin to check the sender and the look.
func (s *Service) TestMail(ctx context.Context, lang string) brandmail.Mail {
	w := s.wordsIn(lang)
	brand := s.Branding(ctx)
	from := FromName(s.LoadIdentity(ctx), brand)
	vars := []string{"server", brand.Name(), "from", from}
	m := brandmail.Mail{
		Kind: "test", T: w.translate(), Lang: w.lang,
		Subject: w.say("mail.test.subject", vars...), Preheader: w.say("mail.test.preheader", vars...),
		Heading: w.say("mail.test.heading", vars...), Lead: w.say("mail.test.lead", vars...),
		Details: []string{w.say("mail.test.details", vars...)},
		Why:     w.say("mail.test.why", vars...),
	}
	if brand.OwnName() {
		m.Lead = w.say("mail.test.lead_named", vars...)
	}
	if base := s.GetPanelBaseURL(); base != "" {
		m.Button, m.Link = w.say("mail.test.button", vars...), base+"/settings?tab=email"
	}
	return m
}
