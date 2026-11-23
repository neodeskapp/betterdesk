package mail

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// The three functions in this file are the handlers of the SMTP settings
// routes (GET, PUT and POST .../smtp/test, mounted by the automation and
// the settings group; the GET also answers the commercialization group's
// smtp-status). A route calls one with what it has of the request and
// sends the Answer: kit.JSON(w, r, a.Status, a.Body). The guards (sign-in,
// the permission server.config) stay with the route.

// Answer is what a settings handler tells its route to do.
type Answer struct {
	// Status is the HTTP status.
	Status int
	// Body marshals to the JSON the Node console sent, member for member.
	Body any
	// Audit is the audit log entry to write, nil for none. The route
	// writes it for the signed-in account and the client's address and
	// ignores a failure.
	Audit *AuditEntry
	// LogError is the line the Node console printed with console.error
	// under "[Settings]" for a 500, without that prefix; "" when there is
	// nothing to log.
	LogError string
}

// AuditEntry is an entry for the audit log.
type AuditEntry struct {
	Action  string
	Details string
}

// rawJSON is JSON that is already written.
type rawJSON string

// MarshalJSON implements json.Marshaler.
func (r rawJSON) MarshalJSON() ([]byte, error) { return []byte(r), nil }

type errorBody struct {
	Error string `json:"error"`
}

func serverError(what string, err error) Answer {
	return Answer{
		Status:   http.StatusInternalServerError,
		Body:     errorBody{Error: "Internal server error"},
		LogError: what + " " + jsbind.Message(err),
	}
}

// GetSMTPSettings answers the configuration in use for the settings page:
// {"configured":false}, or the configuration with "********" in place of a
// password (see Config.MarshalJSON). The password itself is never in the
// answer.
func (s *Service) GetSMTPSettings(ctx context.Context) Answer {
	cfg := s.LoadSMTPConfig(ctx)
	if cfg == nil {
		return Answer{Status: http.StatusOK, Body: rawJSON(`{"configured":false}`)}
	}
	return Answer{Status: http.StatusOK, Body: cfg}
}

// storedConfig is the stored row as the save handler reads it: whatever
// JSON value it holds, undefined when the row is missing, empty or no
// JSON.
func (s *Service) storedConfig(ctx context.Context) (value, error) {
	text, ok, err := getSetting(ctx, s.opts.DB, settingKey)
	if err != nil {
		return undefined, err
	}
	if !ok || text == "" {
		return undefined, nil
	}
	doc, err := parseJSON(text)
	if err != nil {
		return undefined, nil
	}
	return doc, nil
}

func bodyValue(body middleware.JSONObject, name string) value {
	raw := body.Raw(name)
	if raw == nil {
		return undefined
	}
	v, err := parseJSON(string(raw))
	if err != nil {
		return undefined
	}
	return v
}

// PutSMTPSettings saves the configuration a request sends and answers
// {"success":true} with the audit entry "smtp_config_updated".
//
// body is req.body. Each member has to have its type, and the first one
// that has not is answered with 400 {"error":"..."}: "SMTP host is
// required" for a host that is missing, blank or another empty value,
// else "Invalid <member>"; then a save without a sender is answered "SMTP
// sender is required" and one of more than 998 characters "SMTP sender is
// too long", a save that would keep the stored password for another
// host, port or user is answered "SMTP password is required when the
// host, port or user changes", and one that would keep it while it turns
// the certificate check off "SMTP password is required when tlsVerify is
// turned off". What is stored:
//
//	host         text, without the white space around it
//	port         a whole number from 1 to 65535, or text of its digits,
//	             stored as the number; 587 when missing or null
//	secure       true or false; false when missing
//	user         text; "" when missing or null
//	pass         text, "" for none; when missing, null or "********",
//	             the password stored so far when host, port and user
//	             are the stored ones and the certificate check stays on
//	             or was off, else ""
//	from         text that is not blank, of at most 998 characters
//	alert_email  plain addresses separated by commas (PlainAddressList),
//	             without the white space around them; "" when missing or
//	             null
//	tlsVerify    true or false when the request has the member, left out
//	             when it has not
//
// A failing database answers 500 {"error":"Internal server error"}.
func (s *Service) PutSMTPSettings(ctx context.Context, body middleware.JSONObject) Answer {
	const failed = "SMTP update error:"
	existing, err := s.storedConfig(ctx)
	if err != nil {
		return serverError(failed, err)
	}
	config, refused := nextConfig(body, existing, s.opts.TLSVerify)
	if refused != "" {
		return Answer{Status: http.StatusBadRequest, Body: errorBody{Error: refused}}
	}
	if err := setSetting(ctx, s.opts.DB, settingKey, config.json()); err != nil {
		return serverError(failed, err)
	}
	s.ResetTransporter()
	return Answer{
		Status: http.StatusOK,
		Body:   rawJSON(`{"success":true}`),
		Audit: &AuditEntry{
			Action:  "smtp_config_updated",
			Details: "SMTP: " + wellFormed(config.member("host").str) + ":" + jsbind.NumberString(config.member("port").num),
		},
	}
}

// nextConfig is the configuration to store for a request, given what is
// stored so far (any JSON value, or undefined) and whether the console
// checks the certificate of settings that do not say (Options.TLSVerify),
// or the reason the request is refused.
func nextConfig(body middleware.JSONObject, existing value, defaultVerify bool) (config value, refused string) {
	host := bodyValue(body, "host")
	switch {
	case host.kind == kindString && jsbind.Trim(host.str) != "":
	case host.kind == kindString || !host.truthy():
		return undefined, "SMTP host is required"
	default:
		return undefined, "Invalid host"
	}
	port, ok := savedPort(bodyValue(body, "port"))
	if !ok {
		return undefined, "Invalid port"
	}
	secure := bodyValue(body, "secure")
	if secure.kind != kindUndefined && secure.kind != kindBool {
		return undefined, "Invalid secure"
	}
	// The members that are text, "" when missing or null
	texts := map[string]string{}
	for _, name := range []string{"user", "pass", "from", "alert_email"} {
		switch v := bodyValue(body, name); v.kind {
		case kindUndefined, kindNull:
		case kindString:
			texts[name] = v.str
		default:
			return undefined, "Invalid " + name
		}
	}
	alertEmail := jsbind.Trim(texts["alert_email"])
	if !PlainAddressList(alertEmail) {
		return undefined, "Invalid alert_email"
	}
	tlsVerify := bodyValue(body, "tlsVerify")
	if tlsVerify.kind != kindUndefined && tlsVerify.kind != kindBool {
		return undefined, "Invalid tlsVerify"
	}
	if jsbind.Trim(texts["from"]) == "" {
		return undefined, "SMTP sender is required"
	}
	if utf8.RuneCountInString(texts["from"]) > maxFromLength {
		return undefined, "SMTP sender is too long"
	}
	// The password stays what it was for the mask or no password member at
	// all, but only for the server and the account it was given for, with
	// the server's certificate checked as before: it is never sent to
	// another host, port or user, or to a server whose certificate is no
	// longer checked. An empty password removes it.
	pass := stringValue(texts["pass"])
	if sent := bodyValue(body, "pass"); sent.kind != kindString || sent.str == MaskedPass {
		pass = stringValue("")
		if kept := existing.member("pass"); kept.truthy() {
			if !sameAccount(existing, jsbind.Trim(host.str), port, texts["user"]) {
				return undefined, passwordRequired
			}
			if verifies(existing.member("tlsVerify"), defaultVerify) && !verifies(tlsVerify, defaultVerify) {
				return undefined, passwordRequiredUnverified
			}
			pass = kept
		}
	}
	config = objectValue(
		"host", stringValue(jsbind.Trim(host.str)),
		"port", numberValue(port),
		"secure", boolValue(secure.flag),
		"user", stringValue(texts["user"]),
		"pass", pass,
		"from", stringValue(texts["from"]),
		"alert_email", stringValue(alertEmail),
	)
	if tlsVerify.kind == kindBool {
		config.set("tlsVerify", tlsVerify)
	}
	return config, ""
}

// maxFromLength is the longest sender a save takes, in characters: the
// longest line a mail header may have.
const maxFromLength = 998

// passwordRequired refuses a save that keeps the stored password while it
// changes the host, the port or the user.
const passwordRequired = "SMTP password is required when the host, port or user changes"

// passwordRequiredUnverified refuses a save that keeps the stored password
// while it turns the certificate check off.
const passwordRequiredUnverified = "SMTP password is required when tlsVerify is turned off"

// verifies is whether settings with this tlsVerify member check the mail
// server's certificate: the flag when it is one, else the console's
// default (Service.tlsVerify).
func verifies(tlsVerify value, defaultVerify bool) bool {
	if tlsVerify.kind == kindBool {
		return tlsVerify.flag
	}
	return defaultVerify
}

// sameAccount reports whether the stored configuration has the host, the
// port and the user of a save. A port the stored row does not have is 587,
// a user it does not have is "".
func sameAccount(existing value, host string, port float64, user string) bool {
	if h := existing.member("host"); h.kind != kindString || h.str != host {
		return false
	}
	switch p := existing.member("port"); {
	case p.kind == kindNumber:
		if p.num != port {
			return false
		}
	case p.truthy() || port != 587:
		return false
	}
	switch u := existing.member("user"); {
	case u.kind == kindString:
		return u.str == user
	default:
		return !u.truthy() && user == ""
	}
}

// savedPort reads the port of a save: a whole number from 1 to 65535, or
// text of its digits; 587 when the request has none (missing or null).
func savedPort(v value) (float64, bool) {
	n := math.NaN()
	switch v.kind {
	case kindUndefined, kindNull:
		return 587, true
	case kindNumber:
		n = v.num
	case kindString:
		if v.str != "" && strings.Trim(v.str, "0123456789") == "" {
			n, _ = strconv.ParseFloat(v.str, 64)
		}
	}
	return n, n == math.Trunc(n) && n >= 1 && n <= 65535
}

// TestSMTPSettings tests the connection to the mail server of the
// configuration in use and answers the TestResult.
func (s *Service) TestSMTPSettings(ctx context.Context) Answer {
	result, _ := s.TestConnection(ctx)
	return Answer{Status: http.StatusOK, Body: result}
}

var _ json.Marshaler = rawJSON("")
