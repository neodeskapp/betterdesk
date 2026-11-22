package automation

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/mail"
)

// The three SMTP routes hand the request to the mail service, which
// decides the answer, and write what it asks for: the answer, the audit
// log entry of a save, and the log line of a failure.

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

// smtp answers GET smtp: the SMTP settings in use, the password masked,
// or {"configured":false}.
func (h *handlers) smtp(w http.ResponseWriter, r *http.Request) {
	h.smtpAnswer(w, r, h.d.Mail.GetSMTPSettings(r.Context()))
}

// saveSMTP answers PUT smtp with the body {host, port, secure, user, pass,
// from, alert_email, tlsVerify}. Only the host is required. An empty or
// masked password keeps the stored one.
func (h *handlers) saveSMTP(w http.ResponseWriter, r *http.Request) {
	h.smtpAnswer(w, r, h.d.Mail.PutSMTPSettings(r.Context(), kit.Body(r)))
}

// testSMTP answers POST smtp/test: it connects to the mail server and
// signs in, without sending a mail.
func (h *handlers) testSMTP(w http.ResponseWriter, r *http.Request) {
	h.smtpAnswer(w, r, h.d.Mail.TestSMTPSettings(r.Context()))
}
