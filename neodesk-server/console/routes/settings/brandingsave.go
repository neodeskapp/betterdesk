package settings

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// requestBody is req.body as the branding service reads it: the JSON the
// client sent, the fields of a form, {} for anything else. It is read
// with branding.ParseJSON because the cleaning rules depend on what was
// really sent (a number, a list, a repeated key).
func requestBody(r *http.Request) (any, error) {
	return branding.ParseJSON(middleware.BodyValue(r).Raw())
}

// failMessage answers status with err.message, or with fallback when the
// error has no message.
func failMessage(w http.ResponseWriter, r *http.Request, status int, err error, fallback string) {
	message := branding.Message(err)
	if message == "" {
		message = fallback
	}
	kit.Fail(w, r, status, inCallersLanguage(r, message))
}

// saveBranding answers POST /api/settings/branding: the settings in the
// flat or the nested shape are cleaned and stored, and the answer is what
// GET /api/settings/branding answers now. A body that only switches the
// theme (themeMode with at most colors and glassColor) is audited as a
// theme switch.
func (h *handlers) saveBranding(w http.ResponseWriter, r *http.Request) {
	updates, err := requestBody(r)
	if err != nil {
		h.serverError(w, r, "Save branding", err)
		return
	}
	switch updates.(type) {
	case *branding.Object, []any:
	default:
		kit.Fail(w, r, http.StatusBadRequest, "Invalid branding data")
		return
	}
	if problems := branding.CheckSettings(updates, h.d.Branding.GetBranding()); len(problems) > 0 {
		refuseSettings(w, r, problems)
		return
	}
	b := h.d.Branding
	if err := b.SaveBranding(r.Context(), updates); err != nil {
		h.serverError(w, r, "Save branding", err)
		return
	}
	saved := b.GetBranding()
	readability, err := b.AssessAppearanceReadability(saved)
	if err != nil {
		h.serverError(w, r, "Save branding", err)
		return
	}
	action, details := "branding_update", "Updated branding configuration"
	if themeOnly(updates) {
		mode, err := branding.ToString(branding.Field(updates, "themeMode"))
		if err != nil {
			h.serverError(w, r, "Save branding", err)
			return
		}
		action, details = "theme_toggle", "Theme switched to "+mode
	}
	_ = h.d.LogAction(r, action, details)
	model, err := b.GetAppearanceModel(saved)
	if err != nil {
		h.serverError(w, r, "Save branding", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, brandingAnswer{Success: true, Data: saved, Appearance: model, Readability: readability, Revision: b.Revision()})
}

type refusedField struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type refusedSettings struct {
	Success bool           `json:"success"`
	Error   string         `json:"error"`
	Field   string         `json:"field"`
	Fields  []refusedField `json:"fields"`
}

// refuseSettings answers 400 for values a save would clean or drop
// (branding.CheckSettings): error and field name the first, fields lists
// each with its reason in the caller's language. Nothing is stored.
func refuseSettings(w http.ResponseWriter, r *http.Request, problems []branding.Problem) {
	answer := refusedSettings{Fields: make([]refusedField, 0, len(problems))}
	for _, p := range problems {
		var vars []any
		for name, value := range p.Params {
			vars = append(vars, name, value)
		}
		message := kit.T(r, "settings.appearance.refused_"+p.Reason, kit.Vars(vars...))
		answer.Fields = append(answer.Fields, refusedField{Field: p.Field, Message: message})
	}
	answer.Error, answer.Field = answer.Fields[0].Message, answer.Fields[0].Field
	kit.JSON(w, r, http.StatusBadRequest, answer)
}

// themeOnly reports a body whose keys are all themeMode, colors or
// glassColor, with a themeMode that is not empty. A list has index keys
// and never is one.
func themeOnly(updates any) bool {
	o, ok := updates.(*branding.Object)
	if !ok || o.Len() == 0 {
		return false
	}
	for _, key := range o.Keys() {
		if key != "themeMode" && key != "colors" && key != "glassColor" {
			return false
		}
	}
	return branding.Truthy(branding.Field(o, "themeMode"))
}

type messageAnswer struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// resetBranding answers POST /api/settings/branding/reset: every stored
// setting is deleted, so the defaults apply.
func (h *handlers) resetBranding(w http.ResponseWriter, r *http.Request) {
	if err := h.d.Branding.ResetBranding(r.Context()); err != nil {
		h.serverError(w, r, "Reset branding", err)
		return
	}
	_ = h.d.LogAction(r, "branding_reset", "Reset branding to defaults")
	kit.JSON(w, r, http.StatusOK, messageAnswer{Success: true, Message: "Branding reset to defaults"})
}

// exportBranding answers GET /api/settings/branding/export: the current
// settings as a theme file to download.
func (h *handlers) exportBranding(w http.ResponseWriter, r *http.Request) {
	preset, err := h.d.Branding.ExportPreset()
	if err != nil {
		h.serverError(w, r, "Export branding", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="neodesk-theme.json"`)
	kit.JSON(w, r, http.StatusOK, preset)
}

// importBranding answers POST /api/settings/branding/import with a theme
// file as the body.
func (h *handlers) importBranding(w http.ResponseWriter, r *http.Request) {
	preset, err := requestBody(r)
	if err != nil {
		h.serverError(w, r, "Import branding", err)
		return
	}
	ok, err := h.d.Branding.ImportPreset(r.Context(), preset)
	if err != nil {
		h.serverError(w, r, "Import branding", err)
		return
	}
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, inCallersLanguage(r, "Invalid theme preset file"))
		return
	}
	_ = h.d.LogAction(r, "branding_import", "Imported branding preset")
	kit.JSON(w, r, http.StatusOK, messageAnswer{Success: true, Message: "Theme imported successfully"})
}

// applyTheme answers POST /api/settings/themes/{id}/apply: a built-in
// theme becomes the current settings.
func (h *handlers) applyTheme(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result, err := h.d.Branding.ApplyTheme(r.Context(), id)
	if err != nil {
		h.log.Error("Apply theme error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to apply theme")
		return
	}
	switch result {
	case branding.ThemeInvalidID:
		kit.Fail(w, r, http.StatusBadRequest, "Invalid theme id")
		return
	case branding.ThemeNotFound:
		kit.Fail(w, r, http.StatusNotFound, "Theme not found")
		return
	case branding.ThemeInvalidFormat:
		kit.Fail(w, r, http.StatusBadRequest, "Invalid theme format")
		return
	}
	_ = h.d.LogAction(r, "theme_apply", "Applied theme: "+id)
	kit.JSON(w, r, http.StatusOK, messageAnswer{Success: true, Message: `Theme "` + id + `" applied`})
}
