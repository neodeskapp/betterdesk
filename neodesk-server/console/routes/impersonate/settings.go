package impersonate

import (
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// settingsData is Settings, Sign-in, "Mail people when an admin views
// NeoDesk as them" (impersonation.NoticeMailKey, on by default).
type settingsData struct {
	NoticeMail bool `json:"notice_mail"`
}

type settingsAnswer struct {
	Success bool         `json:"success"`
	Data    settingsData `json:"data"`
}

// settings answers GET /api/settings/impersonation.
func (h *handlers) settings(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, settingsAnswer{Success: true, Data: settingsData{NoticeMail: h.svc.NoticeMail(r.Context())}})
}

// saveSettings answers PUT /api/settings/impersonation with {notice_mail}:
// true or false. Anything else is refused.
func (h *handlers) saveSettings(w http.ResponseWriter, r *http.Request) {
	on, ok := kit.Body(r).Get("notice_mail").Bool()
	if !ok {
		fail(w, r, http.StatusBadRequest, "invalid_value", "notice_mail must be true or false")
		return
	}
	if err := h.svc.SetNoticeMail(r.Context(), on); err != nil {
		h.serverError(w, r, "Save impersonation settings error:", err)
		return
	}
	if err := h.d.LogAction(r, "impersonation_settings_changed", "Mail people when an admin views NeoDesk as them: "+strconv.FormatBool(on)); err != nil {
		h.serverError(w, r, "Save impersonation settings error:", err)
		return
	}
	h.settings(w, r)
}
