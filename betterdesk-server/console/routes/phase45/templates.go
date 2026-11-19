package phase45

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// Agent templates are enrollment presets with a one-use token
// (agent_templates, which store.Migrate creates). Deleting one still
// answers 501 after its checks: its route calls a database function the
// old console's database service does not have (BUGS C2).

type templateList struct {
	Success   bool          `json:"success"`
	Templates []templateRow `json:"templates"`
}

type templateListFailure struct {
	Success   bool   `json:"success"`
	Error     string `json:"error"`
	Templates []any  `json:"templates"`
}

// templates answers GET /api/agent-templates: every template, the newest
// first. A failed read answers 500 with the database's message.
func (h *handlers) templates(w http.ResponseWriter, r *http.Request) {
	rows, err := listTemplates(r.Context(), h.d.Store)
	if err != nil {
		kit.JSON(w, r, http.StatusInternalServerError, templateListFailure{Error: jsbind.Message(err), Templates: []any{}})
		return
	}
	kit.JSON(w, r, http.StatusOK, templateList{Success: true, Templates: rows})
}

type templateCreated struct {
	Success         bool   `json:"success"`
	EnrollmentToken string `json:"enrollment_token"`
}

// createTemplate answers POST /api/agent-templates {name, description,
// config}: a name is needed (trimmed, at most 100 UTF-16 units), the
// description is cut to 500, config is kept when it is an object or a
// list. The answer 201 carries the new token, 48 hex digits. A name that is
// taken answers 409 name_exists where the database's message says
// "UNIQUE", which only SQLite's does; PostgreSQL's answers 500 with its
// message (BUGS UM3).
func (h *handlers) createTemplate(w http.ResponseWriter, r *http.Request) {
	name, err := textOr(field(r, "name"), "")
	if err != nil {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
		return
	}
	name = goapipath.Slice(branding.Trim(name), 100)
	if name == "" {
		fail(w, r, http.StatusBadRequest, "name_required")
		return
	}
	description, err := textOr(field(r, "description"), "")
	if err != nil {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
		return
	}
	description = goapipath.Slice(description, 500)
	config := field(r, "config")
	switch config.(type) {
	case *branding.Object, []any:
	default:
		config = branding.NewObject()
	}
	token, err := newEnrollmentToken()
	if err != nil {
		fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	var createdBy *int64
	if id := kit.UserID(r); id != 0 {
		createdBy = &id
	}
	if err := insertTemplate(r.Context(), h.d.Store, name, description, branding.Stringify(config), token, createdBy); err != nil {
		message := jsbind.Message(err)
		if strings.Contains(message, "UNIQUE") {
			fail(w, r, http.StatusConflict, "name_exists")
			return
		}
		fail(w, r, http.StatusInternalServerError, message)
		return
	}
	_ = h.d.LogAction(r, "template_created", "Created agent template: "+name)
	kit.JSON(w, r, http.StatusCreated, templateCreated{Success: true, EnrollmentToken: token})
}

// newEnrollmentToken is crypto.randomBytes(24).toString('hex').
func newEnrollmentToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("could not make an enrollment token")
	}
	return hex.EncodeToString(b), nil
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
// an agent calls without a session: both are needed, the token must be a
// template's, and the answer is the template's name and its configuration
// as JSON.parse reads it ({} when it cannot). The token is not bound to
// the device here; /api/bd/register does that.
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
	token, device = goapipath.Slice(token, 64), goapipath.Slice(device, 64)
	if token == "" || device == "" {
		fail(w, r, http.StatusBadRequest, "missing_fields")
		return
	}
	row, err := templateByToken(r.Context(), h.d.Store, branding.Trim(token))
	if err != nil {
		fail(w, r, http.StatusInternalServerError, jsbind.Message(err))
		return
	}
	if row == nil {
		fail(w, r, http.StatusUnauthorized, "invalid_token")
		return
	}
	config := any(branding.NewObject())
	if text := row.ConfigJSON.Value(); text.Truthy() {
		if parsed, err := branding.ParseJSON([]byte(text.ToString())); err == nil {
			config = parsed
		}
	}
	_ = h.d.Audit.Write(r.Context(), 0, "device_enrolled", "Device "+device+" enrolled via template \""+row.Name.Value().ToString()+"\"", kit.ClientIP(r))
	nameJSON, _ := row.Name.MarshalJSON()
	body := `{"success":true,"template_name":` + string(nameJSON) + `,"preset_config":` + branding.Stringify(config) + `}`
	httpx.WriteJSONBytes(w, r, http.StatusOK, []byte(body))
}
