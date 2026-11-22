package serverattestation

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/attestation"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// status answers GET /api/server-attestation/status: whether a capacity
// test runs, its phase and progress, and the last result.
func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	st := h.svc.Status(r.Context())
	last := st.LastResult
	if !branding.Truthy(last) {
		last = h.svc.LastResult(r.Context())
	}
	o := branding.NewObject()
	o.Set("success", true)
	o.Set("running", st.Running)
	o.Set("phase", st.Phase)
	o.Set("progress", st.Progress)
	o.Set("lastResult", last)
	kit.JSON(w, r, http.StatusOK, o)
}

// started is the answer of a test that started.
type started struct {
	Success bool `json:"success"`
	Started bool `json:"started"`
}

// run answers POST /api/server-attestation/run: it starts a capacity test
// in the background, or answers 409 while one runs.
func (h *handlers) run(w http.ResponseWriter, r *http.Request) {
	if !h.svc.Start() {
		kit.Fail(w, r, http.StatusConflict, "Benchmark already running")
		return
	}
	kit.JSON(w, r, http.StatusOK, started{Success: true, Started: true})
}

// aborted is the answer of an abort: whether a test was running.
type aborted struct {
	Success bool `json:"success"`
	Aborted bool `json:"aborted"`
}

// abort answers POST /api/server-attestation/abort.
func (h *handlers) abort(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, aborted{Success: true, Aborted: h.svc.RequestAbort()})
}

// result answers GET /api/server-attestation/result with the stored
// result of the last test.
func (h *handlers) result(w http.ResponseWriter, r *http.Request) {
	o := branding.NewObject()
	o.Set("success", true)
	o.Set("result", h.svc.LastResult(r.Context()))
	kit.JSON(w, r, http.StatusOK, o)
}

// badgeConfig answers GET /api/server-attestation/badge-config with the
// badge texts and how they read in the request language.
func (h *handlers) badgeConfig(w http.ResponseWriter, r *http.Request) {
	config := h.svc.BadgeConfig(r.Context())
	p, err := attestation.ResolveBadgePresentation(config, nil, h.translate(r), h.brand(r))
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, message(err))
		return
	}
	kit.JSON(w, r, http.StatusOK, badgeAnswer{Success: true, Config: config, Presentation: p})
}

// badgeAnswer is the answer of the badge text routes.
type badgeAnswer struct {
	Success      bool                      `json:"success"`
	Config       attestation.BadgeConfig   `json:"config"`
	Presentation *attestation.Presentation `json:"presentation"`
}

// saveBadgeConfig answers PUT /api/server-attestation/badge-config: it
// stores the texts of the body, cleaned, and notes the change in the
// audit log. A body that is a JSON array, and one with neither badgeTitle
// nor tierLabels (which would store the empty default texts), is refused.
func (h *handlers) saveBadgeConfig(w http.ResponseWriter, r *http.Request) {
	body, err := requestBody(r)
	if err == nil {
		object, isObject := body.(*branding.Object)
		if !isObject || (!object.Has("badgeTitle") && !object.Has("tierLabels")) {
			kit.Fail(w, r, http.StatusBadRequest, "Invalid badge configuration")
			return
		}
	}
	var config attestation.BadgeConfig
	var p *attestation.Presentation
	if err == nil {
		config, err = h.svc.SaveBadgeConfig(r.Context(), body)
	}
	if err == nil {
		p, err = attestation.ResolveBadgePresentation(config, nil, h.translate(r), h.brand(r))
	}
	if err == nil {
		err = h.logAction(r, "server_attestation_badge_updated", "Updated Server Attestation badge labels")
	}
	if err != nil {
		h.log.Error("badge config save failed:", err)
		kit.Fail(w, r, http.StatusInternalServerError, message(err))
		return
	}
	kit.JSON(w, r, http.StatusOK, badgeAnswer{Success: true, Config: config, Presentation: p})
}

// requestBody is req.body as a JavaScript value: the JSON object or array
// the client sent, {} without a body.
func requestBody(r *http.Request) (any, error) {
	var raw json.RawMessage
	if err := kit.Bind(r, &raw); err != nil {
		return nil, err
	}
	return branding.ParseJSON(raw)
}

// public answers GET /api/public/server-attestation, for anyone: the tier,
// connections, time and validity of the last test with the badge texts,
// and nothing else of the result.
func (h *handlers) public(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.svc.PublicBadge(r.Context(), h.translate(r), h.brand(r)))
}

// message is err.message: the text of a JavaScript error, else the text
// of the error the database or the audit log gave.
func message(err error) string {
	var jsErr *branding.Error
	if errors.As(err, &jsErr) {
		return jsErr.Message
	}
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err.Error()
		}
		err = inner
	}
}
