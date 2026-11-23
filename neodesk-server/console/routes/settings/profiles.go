package settings

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// The routes that write the saved appearance profiles. A refusal answers
// with the fixed text of the service's error; a failure of the database
// with the server error.

type profileID struct {
	value int64
	// text is the id as JavaScript wrote the number, for the audit log
	text string
}

// parseProfileID is parseInt(req.params.id, 10) of the routes: leading
// white space and a sign, then the digits up to the first other
// character. ok is false where JavaScript has no finite number and for an
// id beyond 32 bits, which no profile has.
func parseProfileID(r *http.Request) (profileID, bool) {
	s := branding.Trim(r.PathValue("id"))
	sign := ""
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = "-"
		}
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return profileID{}, false
	}
	// digits only: the one error is a number too large, which is +-Inf
	n, _ := strconv.ParseFloat(sign+s[:end], 64)
	if n > math.MaxInt32 || n < math.MinInt32 {
		return profileID{}, false
	}
	text, _ := branding.ToString(n)
	return profileID{value: int64(n), text: text}, true
}

// profileError answers a failed profile route: 409 for a name that is
// taken, 400 with the text of another refusal of the service ("Profile
// not found" among them), and 500 with the server error for the rest,
// which is logged.
func (h *handlers) profileError(w http.ResponseWriter, r *http.Request, what string, err error) {
	var refusal *branding.Error
	switch {
	case errors.Is(err, branding.ErrProfileNameTaken):
		kit.Fail(w, r, http.StatusConflict, inCallersLanguage(r, branding.Message(err)))
	case errors.As(err, &refusal) && refusal.Err == nil:
		kit.Fail(w, r, http.StatusBadRequest, inCallersLanguage(r, refusal.Message))
	default:
		h.log.Error(what+" error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
	}
}

// jsText is String(value || ”).trim().
func jsText(value any) (string, error) {
	if !branding.Truthy(value) {
		return "", nil
	}
	text, err := branding.ToString(value)
	return branding.Trim(text), err
}

type idData struct {
	ID int64 `json:"id"`
}

type idAnswer struct {
	Success bool   `json:"success"`
	Data    idData `json:"data"`
}

// createProfile answers POST /api/settings/branding/profiles with
// {name, description, branding}: a profile of the settings in branding
// when that is an object, else of the current settings.
func (h *handlers) createProfile(w http.ResponseWriter, r *http.Request) {
	fail := func(err error) { h.profileError(w, r, "Create branding profile", err) }
	body, err := requestBody(r)
	if err != nil {
		fail(err)
		return
	}
	name, err := jsText(branding.Field(body, "name"))
	if err != nil {
		fail(branding.ErrInvalidProfileName)
		return
	}
	description, err := jsText(branding.Field(body, "description"))
	if err != nil {
		fail(branding.ErrInvalidProfileDescription)
		return
	}
	if name == "" {
		kit.Fail(w, r, http.StatusBadRequest, inCallersLanguage(r, "Profile name is required"))
		return
	}
	var preset any
	switch settings := branding.Field(body, "branding").(type) {
	case *branding.Object, []any:
		theme := branding.NewObject()
		theme.Set("version", "1.0")
		theme.Set("type", "neodesk-theme")
		theme.Set("branding", settings)
		preset = theme
	}
	id, err := h.d.Branding.CreateProfile(r.Context(), name, description, preset)
	if err != nil {
		fail(err)
		return
	}
	_ = h.d.LogAction(r, "branding_profile_create", "Created profile: "+name)
	kit.JSON(w, r, http.StatusOK, idAnswer{Success: true, Data: idData{ID: id}})
}

// updateProfile answers PUT /api/settings/branding/profiles/{id} with
// {name, description, branding}; what the body leaves out stays.
func (h *handlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(r)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid profile id")
		return
	}
	fail := func(err error) { h.profileError(w, r, "Update branding profile", err) }
	body, err := requestBody(r)
	if err != nil {
		fail(err)
		return
	}
	update := branding.ProfileUpdate{
		Name:        branding.Field(body, "name"),
		Description: branding.Field(body, "description"),
		Branding:    branding.Field(body, "branding"),
	}
	if err := h.d.Branding.UpdateProfile(r.Context(), id.value, update); err != nil {
		fail(err)
		return
	}
	_ = h.d.LogAction(r, "branding_profile_update", "Updated profile #"+id.text)
	kit.OK(w, r, nil)
}

type appliedAnswer struct {
	Success  bool             `json:"success"`
	Data     *branding.Object `json:"data"`
	Revision string           `json:"revision"`
}

// applyProfile answers POST /api/settings/branding/profiles/{id}/apply:
// the profile's settings become the current ones.
func (h *handlers) applyProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(r)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid profile id")
		return
	}
	b := h.d.Branding
	applied, err := b.ApplyProfile(r.Context(), id.value)
	if err != nil {
		h.serverError(w, r, "Apply branding profile", err)
		return
	}
	if !applied {
		kit.Fail(w, r, http.StatusNotFound, "Profile not found or invalid")
		return
	}
	_ = h.d.LogAction(r, "branding_profile_apply", "Applied profile #"+id.text)
	kit.JSON(w, r, http.StatusOK, appliedAnswer{Success: true, Data: b.GetBranding(), Revision: b.Revision()})
}

// duplicateProfile answers POST /api/settings/branding/profiles/{id}/duplicate
// with {name}: a copy, named "<name> (copy)" without a name.
func (h *handlers) duplicateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(r)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid profile id")
		return
	}
	fail := func(err error) { h.profileError(w, r, "Duplicate branding profile", err) }
	body, err := requestBody(r)
	if err != nil {
		fail(err)
		return
	}
	name := branding.Field(body, "name")
	if !branding.Truthy(name) {
		name = ""
	}
	newID, err := h.d.Branding.DuplicateProfile(r.Context(), id.value, name)
	if err != nil {
		fail(err)
		return
	}
	kit.JSON(w, r, http.StatusOK, idAnswer{Success: true, Data: idData{ID: newID}})
}

// deleteProfile answers DELETE /api/settings/branding/profiles/{id}, 404
// when there is no such profile.
func (h *handlers) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(r)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid profile id")
		return
	}
	deleted, err := h.d.Branding.DeleteProfile(r.Context(), id.value)
	if err != nil {
		h.profileError(w, r, "Delete branding profile", err)
		return
	}
	if !deleted {
		kit.Fail(w, r, http.StatusNotFound, "Profile not found")
		return
	}
	_ = h.d.LogAction(r, "branding_profile_delete", "Deleted profile #"+id.text)
	kit.OK(w, r, nil)
}
