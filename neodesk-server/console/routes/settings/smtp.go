package settings

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
)

// The three SMTP routes under /api/settings/email hand the request to the
// mail service, which decides the answer, and write what it asks for:
// the answer, the audit log entry of a save, and the log line of a
// failure.

// smtpAnswer sends an answer of the mail service.
func (h *handlers) smtpAnswer(w http.ResponseWriter, r *http.Request, a mail.Answer) {
	if a.LogError != "" {
		h.d.Log.Child("Settings").Error(a.LogError)
	}
	if a.Audit != nil {
		// A failure to write the audit entry does not change the answer
		_ = h.d.LogAction(r, a.Audit.Action, a.Audit.Details)
	}
	kit.JSON(w, r, a.Status, a.Body)
}

// getSMTP answers GET /api/settings/email/smtp: the SMTP settings in use,
// the password masked, or {"configured":false}.
func (h *handlers) getSMTP(w http.ResponseWriter, r *http.Request) {
	h.smtpAnswer(w, r, h.d.Mail.GetSMTPSettings(r.Context()))
}

// saveSMTP answers PUT /api/settings/email/smtp with the body {host, port,
// secure, user, pass, from, alert_email, tlsVerify}. Only the host is
// required. An empty or masked password keeps the stored one. A refusal
// is answered in the console's language with the field it is about:
// {"error", "field"} (BUGS L419).
func (h *handlers) saveSMTP(w http.ResponseWriter, r *http.Request) {
	answer := h.d.Mail.PutSMTPSettings(r.Context(), kit.Body(r))
	if refusal, ok := smtpRefusals[answer.Refusal()]; ok {
		kit.JSON(w, r, answer.Status, smtpRefused{Error: kit.T(r, refusal.key, nil), Field: refusal.field})
		return
	}
	h.smtpAnswer(w, r, answer)
}

type smtpRefused struct {
	Error string `json:"error"`
	Field string `json:"field"`
}

// smtpRefusals are the mail service's refusals of a save: the text key and
// the form field of each.
var smtpRefusals = map[string]struct{ key, field string }{
	"SMTP host is required":                {"settings.email.err_host_required", "host"},
	"Invalid host":                         {"settings.email.err_host_required", "host"},
	"Invalid port":                         {"settings.email.port_error", "port"},
	"SMTP sender is required":              {"settings.email.err_from_required", "from"},
	"SMTP sender is too long":              {"settings.email.err_from_long", "from"},
	"Invalid from":                         {"settings.email.err_from_invalid", "from"},
	"Invalid alert_email":                  {"settings.email.err_alert_email", "alert_email"},
	mail.RefusalPasswordRequired:           {"settings.email.err_pass_required", "pass"},
	mail.RefusalPasswordRequiredUnverified: {"settings.email.err_pass_unverified", "pass"},
}

// testSMTP answers POST /api/settings/email/smtp/test: it connects to the
// mail server and signs in, without sending a mail.
func (h *handlers) testSMTP(w http.ResponseWriter, r *http.Request) {
	h.smtpAnswer(w, r, h.d.Mail.TestSMTPSettings(r.Context()))
}
