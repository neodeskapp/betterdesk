package commercialization

import (
	"encoding/json"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/helpmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

type emailConfigAnswer struct {
	Success bool            `json:"success"`
	Config  helpmail.Config `json:"config"`
}

// emailConfig answers GET /api/panel/commercialization/email-config: who
// gets a mail for a help request.
func (h *handlers) emailConfig(w http.ResponseWriter, r *http.Request) {
	config, err := helpmail.LoadConfig(r.Context(), h.d.Store)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, emailConfigAnswer{Success: true, Config: config})
}

// saveEmailConfig answers PUT /api/panel/commercialization/email-config
// with the body {help_requests_enabled, notify_assigned_operators,
// fallback_alert_email, include_folder_in_subject}: each switch is on
// unless the body has it as false. A body with none of them is refused.
func (h *handlers) saveEmailConfig(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, "help_requests_enabled", "notify_assigned_operators", "fallback_alert_email", "include_folder_in_subject") {
		return
	}
	config := helpmail.ConfigFrom(body)
	if err := helpmail.SaveConfig(r.Context(), h.d.Store, config); err != nil {
		fail(w, r, http.StatusInternalServerError, err)
		return
	}
	// A failure to write the audit entry does not change the answer
	_ = h.d.LogAction(r, "commercialization_email_config_updated", "Updated commercialization email notifications")
	kit.JSON(w, r, http.StatusOK, emailConfigAnswer{Success: true, Config: config})
}

// refuseNothingToSave answers 400 for a body that is no object or has
// none of fields, which a save would otherwise take as "set the
// defaults". It reports whether it answered.
func refuseNothingToSave(w http.ResponseWriter, r *http.Request, body any, fields ...string) bool {
	if obj, ok := body.(*goapi.Object); ok {
		for _, field := range fields {
			if obj.Has(field) {
				return false
			}
		}
	}
	kit.Fail(w, r, http.StatusBadRequest, "No settings to save")
	return true
}

// smtpConfigured is the answer of smtp-status.
type smtpConfigured struct {
	Configured bool `json:"configured"`
}

// smtpStatus answers GET /api/panel/commercialization/smtp-status with
// {"configured":true|false}, for the billing page to say whether help
// request mails can go out. The settings themselves are the settings
// page's, behind server.config.
func (h *handlers) smtpStatus(w http.ResponseWriter, r *http.Request) {
	var status smtpConfigured
	settings, err := json.Marshal(h.d.Mail.GetSMTPSettings(r.Context()).Body)
	if err == nil {
		err = json.Unmarshal(settings, &status)
	}
	if err != nil {
		h.log.Child("Settings").Error("SMTP status:", err)
	}
	kit.JSON(w, r, http.StatusOK, status)
}
