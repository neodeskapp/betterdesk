package phase45

import (
	"math"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// Agent templates were meant to be enrollment presets with a one-use
// token. The old console read and wrote them through database calls its
// database service does not have: the list is always empty, creating and
// deleting answer 501 after their checks (BUGS C2), and enrollment finds
// no template.

type templateList struct {
	Success   bool  `json:"success"`
	Templates []any `json:"templates"`
}

// templates answers GET /api/agent-templates.
func (h *handlers) templates(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, templateList{Success: true, Templates: []any{}})
}

// createTemplate answers POST /api/agent-templates {name, description,
// config}: a name is needed, and then the store cannot write it.
func (h *handlers) createTemplate(w http.ResponseWriter, r *http.Request) {
	name, err := textOr(field(r, "name"), "")
	if err != nil {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
		return
	}
	if goapipath.Slice(branding.Trim(name), 100) == "" {
		fail(w, r, http.StatusBadRequest, "name_required")
		return
	}
	if _, err := textOr(field(r, "description"), ""); err != nil {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
		return
	}
	fail(w, r, http.StatusNotImplemented, "db_backend_missing_run")
}

// deleteTemplate answers DELETE /api/agent-templates/{id}: the id must be
// a positive number, and then the store cannot delete it.
func (h *handlers) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := jsreq.ParseInt(r.PathValue("id"), 10)
	if !ok || math.IsInf(id, 0) || id <= 0 {
		fail(w, r, http.StatusBadRequest, "invalid_id")
		return
	}
	fail(w, r, http.StatusNotImplemented, "db_backend_missing_run")
}

// enroll answers POST /api/bd/enroll {enrollment_token, device_id}, which
// an agent calls without a session: both are needed, and no template has
// the token.
func (h *handlers) enroll(w http.ResponseWriter, r *http.Request) {
	token, err := textOr(field(r, "enrollment_token"), "")
	if err != nil {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
		return
	}
	device, err := textOr(field(r, "device_id"), "")
	if err != nil {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
		return
	}
	if goapipath.Slice(token, 64) == "" || goapipath.Slice(device, 64) == "" {
		fail(w, r, http.StatusBadRequest, "missing_fields")
		return
	}
	fail(w, r, http.StatusUnauthorized, "invalid_token")
}
