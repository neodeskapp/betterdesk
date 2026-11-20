package settings

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// brandingAnswer is GET /api/settings/branding: the flat settings, the
// nested appearance model made from them, the readability checks and the
// revision.
type brandingAnswer struct {
	Success     bool             `json:"success"`
	Data        *branding.Object `json:"data"`
	Appearance  *branding.Object `json:"appearance"`
	Readability *branding.Object `json:"readability"`
	Revision    string           `json:"revision"`
}

// appearanceAnswer is GET /api/settings/appearance: the same with the model
// first.
type appearanceAnswer struct {
	Success     bool             `json:"success"`
	Data        *branding.Object `json:"data"`
	Flat        *branding.Object `json:"flat"`
	Readability *branding.Object `json:"readability"`
	Revision    string           `json:"revision"`
}

// serverError is the answer of a read that failed: 500 with the
// translated "server error".
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what+" error:", err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

// model returns the settings with their appearance model and readability.
func (h *handlers) model(r *http.Request) (flat, model, readability *branding.Object, err error) {
	flat = h.d.Branding.GetBranding()
	if model, err = h.d.Branding.GetAppearanceModel(flat); err != nil {
		return nil, nil, nil, err
	}
	if readability, err = h.d.Branding.AssessAppearanceReadability(flat); err != nil {
		return nil, nil, nil, err
	}
	return flat, model, readability, nil
}

// getBranding answers GET /api/settings/branding.
func (h *handlers) getBranding(w http.ResponseWriter, r *http.Request) {
	flat, model, readability, err := h.model(r)
	if err != nil {
		h.serverError(w, r, "Get branding", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, brandingAnswer{Success: true, Data: flat, Appearance: model, Readability: readability, Revision: h.d.Branding.Revision()})
}

// getAppearance answers GET /api/settings/appearance, the versioned
// appearance model of the settings page.
func (h *handlers) getAppearance(w http.ResponseWriter, r *http.Request) {
	flat, model, readability, err := h.model(r)
	if err != nil {
		h.serverError(w, r, "Get appearance", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, appearanceAnswer{Success: true, Data: model, Flat: flat, Readability: readability, Revision: h.d.Branding.Revision()})
}

// listBackgrounds answers GET /api/settings/branding/backgrounds: the
// uploaded background pictures, newest first.
func (h *handlers) listBackgrounds(w http.ResponseWriter, r *http.Request) {
	list, err := h.d.Branding.ListBackgrounds()
	if err != nil {
		h.serverError(w, r, "List branding backgrounds", err)
		return
	}
	kit.OK(w, r, list)
}

// listProfiles answers GET /api/settings/branding/profiles: the saved
// appearance profiles by name, without their settings.
func (h *handlers) listProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.d.Branding.ListProfiles(r.Context())
	if err != nil {
		h.serverError(w, r, "List branding profiles", err)
		return
	}
	kit.OK(w, r, profiles)
}

// listThemes answers GET /api/settings/themes: the built-in theme presets.
func (h *handlers) listThemes(w http.ResponseWriter, r *http.Request) {
	themes, err := h.d.Branding.ListThemes()
	if err != nil {
		h.log.Error("List themes error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to list themes")
		return
	}
	kit.OK(w, r, themes)
}

// searchFonts answers GET /api/settings/fonts?q=&category=&source=: the
// curated fonts, or with source=google the longer list, that match the
// search text (cut at 100 characters) and the category (default "all",
// cut at 20).
func (h *handlers) searchFonts(w http.ResponseWriter, r *http.Request) {
	query, err1 := queryText(r, "q", "", 100)
	category, err2 := queryText(r, "category", "all", 20)
	source, err3 := queryText(r, "source", "curated", 20)
	var fonts []any
	err := firstError(err1, err2, err3)
	if err == nil {
		if source == "google" {
			fonts, err = h.d.Branding.SearchGoogleFonts(query, category)
		} else {
			fonts, err = h.d.Branding.SearchFonts(query, category)
		}
	}
	if err != nil {
		h.log.Error("Font search error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to search fonts")
		return
	}
	kit.OK(w, r, fonts)
}

// queryText is String(req.query[name] || fallback).substring(0, limit).
// It fails where JavaScript cannot make text of the value.
func queryText(r *http.Request, name, fallback string, limit int) (string, error) {
	value := goapipath.Query(r, name)
	text := fallback
	if value.Truthy() {
		var err error
		if text, err = value.Text(); err != nil {
			return "", err
		}
	}
	return goapipath.Slice(text, limit), nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// localFonts answers GET /api/settings/fonts/local: the fonts installed on
// this server.
func (h *handlers) localFonts(w http.ResponseWriter, r *http.Request) {
	fonts, err := h.d.Branding.ListLocalFonts()
	if err != nil {
		h.log.Error("List local fonts error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to list fonts")
		return
	}
	kit.OK(w, r, fonts)
}

// brandingCSS answers GET /css/branding.css: the stylesheet made from the
// settings, for everyone, cached for a minute. X-Branding-Revision names
// the settings it was made from. When it cannot be made the answer is
// still 200, with a comment as the body.
func (h *handlers) brandingCSS(w http.ResponseWriter, r *http.Request) {
	css, err := h.d.Branding.GenerateThemeCSS()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if err != nil {
		httpx.WriteBody(w, r, http.StatusOK, "", []byte("/* branding error */"))
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Branding-Revision", h.d.Branding.Revision())
	httpx.WriteBody(w, r, http.StatusOK, "", []byte(css))
}

// themeCSS answers GET /css/theme.css, the older address of the same
// stylesheet, without the revision.
func (h *handlers) themeCSS(w http.ResponseWriter, r *http.Request) {
	css, err := h.d.Branding.GenerateThemeCSS()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if err != nil {
		httpx.WriteBody(w, r, http.StatusOK, "", []byte("/* theme error */"))
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpx.WriteBody(w, r, http.StatusOK, "", []byte(css))
}

// favicon answers GET /branding/favicon.svg: the administrator's own
// favicon, else the BetterDesk mark, cached for five minutes. The answer
// is an SVG document from the console's address, so the console's policy
// gets the directive sandbox: nothing in the document runs, even when
// someone opens the address itself.
func (h *handlers) favicon(w http.ResponseWriter, r *http.Request) {
	policy := w.Header().Get("Content-Security-Policy")
	if policy != "" {
		policy += ";"
	}
	w.Header().Set("Content-Security-Policy", policy+"sandbox")
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteBody(w, r, http.StatusOK, "image/svg+xml; charset=utf-8", []byte(h.d.Branding.GenerateFavicon()))
}
