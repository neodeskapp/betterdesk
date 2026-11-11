package mail

import (
	"context"
	"encoding/json"
	"math"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
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
// body is req.body. Its host is required: without one the answer is 400
// {"error":"SMTP host is required"}. What is stored:
//
//	host         String(host) without the white space around it
//	port         parseInt(port, 10), 587 when that is no number or 0
//	secure       true for any value that counts as set
//	user         the value as sent, "" when not set
//	pass         the value as sent; when it is not set or is "********",
//	             the password stored so far, else ""
//	from         the value as sent, "betterdesk@localhost" when not set
//	alert_email  String(alert_email) trimmed, "" when not set
//	tlsVerify    true or false when the request has the member, left out
//	             when it has not
//
// user, pass and from keep whatever JSON type the request used. A value
// JavaScript cannot make text of where it needs text (host, port,
// alert_email) and a failing database answer 500
// {"error":"Internal server error"}.
func (s *Service) PutSMTPSettings(ctx context.Context, body middleware.JSONObject) Answer {
	const failed = "SMTP update error:"
	if !bodyValue(body, "host").truthy() {
		return Answer{Status: http.StatusBadRequest, Body: errorBody{Error: "SMTP host is required"}}
	}
	existing, err := s.storedConfig(ctx)
	if err != nil {
		return serverError(failed, err)
	}
	config, err := nextConfig(body, existing)
	if err != nil {
		return serverError(failed, err)
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

// nextConfig is the configuration to store for a request whose host is
// set, given what is stored so far (any JSON value, or undefined). The
// error is the TypeError for a host, port or alert_email JavaScript
// cannot make text of.
func nextConfig(body middleware.JSONObject, existing value) (value, error) {
	// The password stays what it was unless the request brings a new one:
	// the settings page sends the mask back, or nothing.
	pass := bodyValue(body, "pass")
	if !pass.truthy() || (pass.kind == kindString && pass.str == MaskedPass) {
		pass = stringValue("")
		if kept := existing.member("pass"); kept.truthy() {
			pass = kept
		}
	}

	hostText, err := bodyValue(body, "host").toString()
	if err != nil {
		return undefined, err
	}
	portText, err := bodyValue(body, "port").toString()
	if err != nil {
		return undefined, err
	}
	port := parseInt10(portText)
	if port == 0 || math.IsNaN(port) {
		port = 587
	}
	orText := func(name, fallback string) value {
		if v := bodyValue(body, name); v.truthy() {
			return v
		}
		return stringValue(fallback)
	}
	alertEmail := stringValue("")
	if v := bodyValue(body, "alert_email"); v.truthy() {
		text, err := v.toString()
		if err != nil {
			return undefined, err
		}
		alertEmail = stringValue(jsbind.Trim(text))
	}
	config := objectValue(
		"host", stringValue(jsbind.Trim(hostText)),
		"port", numberValue(port),
		"secure", boolValue(bodyValue(body, "secure").truthy()),
		"user", orText("user", ""),
		"pass", pass,
		"from", orText("from", defaultFrom),
		"alert_email", alertEmail,
	)
	if v := bodyValue(body, "tlsVerify"); !v.isUndefined() {
		config.set("tlsVerify", boolValue(v.truthy()))
	}
	return config, nil
}

// TestSMTPSettings tests the connection to the mail server of the
// configuration in use and answers the TestResult. The TypeError of
// TestConnection is a 500 {"error":"Internal server error"}.
func (s *Service) TestSMTPSettings(ctx context.Context) Answer {
	result, err := s.TestConnection(ctx)
	if err != nil {
		return serverError("SMTP test error:", err)
	}
	return Answer{Status: http.StatusOK, Body: result}
}

var _ json.Marshaler = rawJSON("")
