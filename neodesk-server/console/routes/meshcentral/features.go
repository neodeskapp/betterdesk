package meshcentral

import (
	"net/http"
	"slices"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// The account's client features (the switches of the access sheet) apply
// to its MeshCentral sessions: the desktop opens view only without remote
// control (the mesh agent keeps the viewer's input out), and so does a
// share link the account makes; the terminal,
// the file browser, TCP and UDP tunnels and the power actions other than
// wake are refused without their feature. The mesh viewer has no
// clipboard and no sound, so those two switches have nothing to act on.

// featureRefusals are the answers of refused requests, by feature.
var featureRefusals = map[string]string{
	"terminal":      "The terminal is turned off for your account",
	"file_transfer": "File transfer is turned off for your account",
	"tunnel":        "TCP tunnels are turned off for your account",
	"reboot":        "Remote reboot is turned off for your account",
}

// everyFeature is what an account may not use when its features cannot be
// looked up.
var everyFeature = []string{"remote_control", "file_transfer", "clipboard", "audio", "tunnel", "terminal", "reboot", "privacy_mode"}

// featuresOff returns the client features the request's account may not
// use; every feature when the lookup fails, none without a lookup.
func (h *handlers) featuresOff(r *http.Request) []string {
	if h.deps.Access == nil {
		return nil
	}
	off, err := h.deps.Access.ClientFeaturesOff(r.Context(), kit.Access(r).UserID)
	if err != nil {
		h.deps.Log.Warn("[MESH] client features of the account could not be read:", err.Error())
		return everyFeature
	}
	return off
}

// needsFeature refuses the request with 403 and the feature's message when
// the account may not use the feature.
func (h *handlers) needsFeature(feature string) kit.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slices.Contains(h.featuresOff(r), feature) {
				kit.JSON(w, r, http.StatusForbidden, apiFailure{Error: featureRefusals[feature]})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// wakeActions are the power actions that only wake a device; the API
// server reads the action trimmed and in any letter case.
var wakeActions = []string{"wake", "wakeup", "on"}

// powerNeedsReboot refuses a power action other than wake when the
// account may not restart devices. An action that is no text goes on, for
// the API server to refuse.
func (h *handlers) powerNeedsReboot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action, isText := kit.Body(r).String("action")
		wake := isText && slices.Contains(wakeActions, strings.ToLower(strings.TrimSpace(action)))
		if isText && !wake && slices.Contains(h.featuresOff(r), "reboot") {
			kit.JSON(w, r, http.StatusForbidden, apiFailure{Error: featureRefusals["reboot"]})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// share answers POST /api/mesh/devices/{id}/share: a share link of an
// account that may not use remote control is view only, whatever the body
// says. A body that is no object goes on as it came, for the API server
// to refuse.
func (h *handlers) share(w http.ResponseWriter, r *http.Request) {
	if !validDevice(w, r) {
		return
	}
	body := requestBody(r)
	if slices.Contains(h.featuresOff(r), "remote_control") {
		if value, err := goapi.Parse(middleware.BodyValue(r).Raw()); err == nil {
			if object, ok := value.(*goapi.Object); ok && object != nil {
				body = goapi.Body(object.Set("view_only", true))
			}
		}
	}
	h.pass(w, r, http.MethodPost, devicePath(r.PathValue("id"), "share"), body)
}
