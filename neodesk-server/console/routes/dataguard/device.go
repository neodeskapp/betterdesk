package dataguard

import (
	"math"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
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
// device. event_type, policy_name and action are text, null or missing,
// empty ones getting their defaults (info, "", log); policy_id is the id
// of a policy, or null; details is an object, or null for {}. Anything
// else is 400 and stores nothing.
func (h *handlers) report(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	source, isText := body.String("event_source")
	if !isText || source == "" {
		fail(w, r, http.StatusBadRequest, "event_source is required (usb | file)")
		return
	}
	for _, name := range []string{"event_type", "policy_name", "action"} {
		if v := jsbind.Of(body.Get(name)); !v.IsUndefined() && !v.IsNull() && !v.IsString() {
			fail(w, r, http.StatusBadRequest, "Invalid "+name)
			return
		}
	}
	policyID, ok, err := h.policyOf(r, jsbind.Of(body.Get("policy_id")))
	if err == nil && !ok {
		fail(w, r, http.StatusBadRequest, "Invalid policy_id")
		return
	}
	details := jsbind.Of(body.Get("details"))
	switch {
	case details.IsUndefined(), details.IsNull():
		details = jsbind.String("{}")
	case details.IsObject():
		details = jsbind.String(details.JSON())
	default:
		fail(w, r, http.StatusBadRequest, "Invalid details")
		return
	}
	var id jsbind.Cell
	if err == nil {
		id, err = insertEvent(r.Context(), h.db, newEvent{
			DeviceID:    jsbind.String(kit.Device(r).DeviceID),
			EventSource: jsbind.String(source),
			EventType:   jsbind.Of(body.Get("event_type")).Or(jsbind.String("info")),
			PolicyID:    policyID,
			PolicyName:  jsbind.Of(body.Get("policy_name")).Or(jsbind.String("")),
			Action:      jsbind.Of(body.Get("action")).Or(jsbind.String("log")),
			Details:     details,
		})
	}
	if err != nil {
		h.log.Error("POST /dlp-events error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to record event")
		return
	}
	kit.JSON(w, r, http.StatusCreated, reported{OK: true, ID: id})
}

// policyOf reads the policy_id of an event: null when it is missing or
// null, else a whole number that is the id of a policy. ok is false for
// any other value.
func (h *handlers) policyOf(r *http.Request, v jsbind.Value) (id jsbind.Value, ok bool, err error) {
	if v.IsUndefined() || v.IsNull() {
		return jsbind.Null, true, nil
	}
	n := v.ToNumber()
	if !v.IsNumber() || n < 1 || n > math.MaxInt32 || n != math.Trunc(n) {
		return jsbind.Null, false, nil
	}
	exists, err := policyExists(r.Context(), h.db, v)
	return v, exists, err
}
