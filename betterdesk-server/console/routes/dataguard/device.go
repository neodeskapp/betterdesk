package dataguard

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// The two routes of the desktop agent, which takes its device token
// instead of a session. At both mounts a write with the device token
// passes the CSRF gate without a CSRF token when no one is signed in; with
// a session it needs one like any other write.

// activePolicies answers GET dlp-policies: the policies that are on, each
// as {id, name, rules}. The rules of a policy that is off are not read.
func (h *handlers) activePolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := listActivePolicies(r.Context(), h.db)
	if err != nil {
		h.log.Error("GET /dlp-policies error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch policies")
		return
	}
	kit.JSON(w, r, http.StatusOK, policies)
}

type reported struct {
	OK bool        `json:"ok"`
	ID jsbind.Cell `json:"id"`
}

// report answers POST dlp-events with the body {event_source, event_type?,
// policy_id?, policy_name?, action?, details?}: an event of the token's
// device. Empty values get their defaults (info, null, "", log, {}); a
// details that is no text is stored as its JSON.
func (h *handlers) report(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	source, isText := body.String("event_source")
	if !isText || source == "" {
		fail(w, r, http.StatusBadRequest, "event_source is required (usb | file)")
		return
	}
	details := jsbind.Of(body.Get("details"))
	switch {
	case !details.Truthy():
		details = jsbind.String("{}")
	case !details.IsString():
		details = jsbind.String(details.JSON())
	}
	id, err := insertEvent(r.Context(), h.db, newEvent{
		DeviceID:    jsbind.String(kit.Device(r).DeviceID),
		EventSource: jsbind.String(source),
		EventType:   jsbind.Of(body.Get("event_type")).Or(jsbind.String("info")),
		PolicyID:    jsbind.Of(body.Get("policy_id")).Or(jsbind.Null),
		PolicyName:  jsbind.Of(body.Get("policy_name")).Or(jsbind.String("")),
		Action:      jsbind.Of(body.Get("action")).Or(jsbind.String("log")),
		Details:     details,
	})
	if err != nil {
		h.log.Error("POST /dlp-events error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to record event")
		return
	}
	kit.JSON(w, r, http.StatusCreated, reported{OK: true, ID: id})
}
