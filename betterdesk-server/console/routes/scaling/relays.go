package scaling

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

var (
	addressPattern = regexp.MustCompile(`^[A-Za-z0-9._\-:[\]]+:[0-9]{1,5}$`)
	// Letters and digits of any script, white space and . _ - # / ( )
	namePattern = regexp.MustCompile(`^[\p{L}\p{N}\s\x0B\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}._\-#/()]{1,80}$`)
)

const (
	maxRelays = 100
	maxRules  = 200
)

// validateRelay returns what is wrong with the body of a relay, "" when
// it can be stored.
func validateRelay(body middleware.JSONObject) string {
	name := text(body.Get("name"), "")
	address := text(body.Get("address"), "")
	if n := utf16Len(name); n < 1 || n > 80 {
		return "name must be 1-80 characters"
	}
	if !namePattern.MatchString(name) {
		return "name contains invalid characters"
	}
	if !addressPattern.MatchString(address) {
		return "address must be host:port (e.g. relay.example.com:21117)"
	}
	port, _ := parseIntText(address[strings.LastIndexByte(address, ':')+1:])
	if port < 1 || port > 65535 {
		return "invalid port"
	}
	if location := text(body.Get("location"), ""); utf16Len(location) > 80 {
		return "location too long"
	}
	if n, ok := parseInt(body.Get("max_sessions")); ok && (n < 1 || n > 100000) {
		return "max_sessions out of range"
	}
	if n, ok := parseInt(body.Get("max_bandwidth_mbps")); ok && (n < 1 || n > 1000000) {
		return "max_bandwidth_mbps out of range"
	}
	return ""
}

// relayFrom builds the stored relay from a body: a new one with an id and
// its time of creation, or the existing one with its members replaced.
// The live numbers are not known to the console and are stored as 0.
func (h *handlers) relayFrom(body middleware.JSONObject, existing *object) (*object, error) {
	out, err := h.fresh(existing)
	if err != nil {
		return nil, err
	}
	out.set("name", text(body.Get("name"), ""))
	out.set("address", text(body.Get("address"), ""))
	out.set("location", text(body.Get("location"), ""))
	out.set("max_sessions", numberOr(body.Get("max_sessions"), 50))
	out.set("max_bandwidth_mbps", numberOr(body.Get("max_bandwidth_mbps"), 100))
	out.set("updated_at", h.isoNow())
	for _, key := range []string{"connected_devices", "active_sessions", "bandwidth_mbps", "cpu", "memory"} {
		out.set(key, float64(0))
	}
	out.set("status", "unknown")
	return out, nil
}

// fresh is a copy of an existing entry, or a new entry with an id and its
// time of creation.
func (h *handlers) fresh(existing *object) (*object, error) {
	if existing != nil {
		return existing.clone(), nil
	}
	id, err := h.newID()
	if err != nil {
		return nil, err
	}
	out := newObject()
	out.set("id", id)
	out.set("created_at", h.isoNow())
	return out, nil
}

// numberOr is the whole number at the start of a body member, or the
// fallback when there is none.
func numberOr(v middleware.Value, fallback float64) float64 {
	if n, ok := parseInt(v); ok {
		return n
	}
	return fallback
}

// find returns the index of the element whose "id" is the text id, -1
// when there is none. A null in the list is an error.
func find(items []any, id string) (int, error) {
	for i, item := range items {
		value, err := member(item, "id")
		if err != nil {
			return -1, err
		}
		if value == id {
			return i, nil
		}
	}
	return -1, nil
}

// addressTaken reports whether an element other than skip has the
// address.
func addressTaken(items []any, address string, skip int) (bool, error) {
	for i, item := range items {
		value, err := member(item, "address")
		if err != nil {
			return false, err
		}
		if i != skip && value == address {
			return true, nil
		}
	}
	return false, nil
}

// listRelays answers GET /api/panel/scaling/relays: {"data":[...]}.
func (h *handlers) listRelays(w http.ResponseWriter, r *http.Request) {
	relays, err := h.load(r.Context(), keyRelays)
	if err != nil {
		h.log.Error("[Scaling] list relays:", err)
		fail(w, r, http.StatusInternalServerError, "failed to load relay nodes")
		return
	}
	kit.JSON(w, r, http.StatusOK, dataAnswer{list(relays)})
}

// getRelay answers GET /api/panel/scaling/relays/{nodeId}.
func (h *handlers) getRelay(w http.ResponseWriter, r *http.Request) {
	relays, err := h.load(r.Context(), keyRelays)
	i := -1
	if err == nil {
		i, err = find(relays, r.PathValue("nodeId"))
	}
	if err != nil {
		h.log.Error("[Scaling] read relay:", err)
		fail(w, r, http.StatusInternalServerError, "failed to load relay node")
		return
	}
	if i < 0 {
		fail(w, r, http.StatusNotFound, "not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, dataAnswer{relays[i]})
}

// createRelay answers POST /api/panel/scaling/relays with the body
// {"name","address","location","max_sessions","max_bandwidth_mbps"}:
// 201 and the stored relay. An address can be in the list once, and the
// list holds 100 relays.
func (h *handlers) createRelay(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	if problem := validateRelay(body); problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	relays, err := h.load(r.Context(), keyRelays)
	var taken bool
	if err == nil {
		taken, err = addressTaken(relays, text(body.Get("address"), ""), -1)
	}
	if err == nil && taken {
		fail(w, r, http.StatusConflict, "a relay with this address already exists")
		return
	}
	if err == nil && len(relays) >= maxRelays {
		fail(w, r, http.StatusBadRequest, "too many relay entries (max 100)")
		return
	}
	var relay *object
	if err == nil {
		relay, err = h.relayFrom(body, nil)
	}
	if err == nil {
		err = h.save(r.Context(), keyRelays, append(relays, relay))
	}
	if err != nil {
		h.log.Error("[Scaling] create relay:", err)
		fail(w, r, http.StatusInternalServerError, "failed to save relay node")
		return
	}
	kit.JSON(w, r, http.StatusCreated, dataAnswer{relay})
}

// updateRelay answers PUT /api/panel/scaling/relays/{nodeId} with the
// body of createRelay: the relay with its members replaced. Members the
// stored relay has beyond those stay.
func (h *handlers) updateRelay(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	if problem := validateRelay(body); problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	relays, err := h.load(r.Context(), keyRelays)
	i := -1
	if err == nil {
		i, err = find(relays, r.PathValue("nodeId"))
	}
	if err == nil && i < 0 {
		fail(w, r, http.StatusNotFound, "not found")
		return
	}
	var taken bool
	if err == nil {
		taken, err = addressTaken(relays, text(body.Get("address"), ""), i)
	}
	if err == nil && taken {
		fail(w, r, http.StatusConflict, "another relay already uses this address")
		return
	}
	var relay *object
	if err == nil {
		existing, _ := relays[i].(*object)
		relay, err = h.relayFrom(body, existing)
	}
	if err == nil {
		relays[i] = relay
		err = h.save(r.Context(), keyRelays, relays)
	}
	if err != nil {
		h.log.Error("[Scaling] update relay:", err)
		fail(w, r, http.StatusInternalServerError, "failed to update relay node")
		return
	}
	kit.JSON(w, r, http.StatusOK, dataAnswer{relay})
}

// remove answers a DELETE for an entry of a stored list: {"ok":true}, or
// 404 when no entry has the id.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request, key, id, failure string) {
	items, err := h.load(r.Context(), key)
	i := -1
	if err == nil {
		i, err = find(items, id)
	}
	if err == nil && i < 0 {
		// Every entry with the id goes, and without one nothing is stored
		fail(w, r, http.StatusNotFound, "not found")
		return
	}
	if err == nil {
		kept := []any{}
		for _, item := range items {
			if value, _ := member(item, "id"); value != id {
				kept = append(kept, item)
			}
		}
		err = h.save(r.Context(), key, kept)
	}
	if err != nil {
		h.log.Error("[Scaling] delete from "+key+":", err)
		fail(w, r, http.StatusInternalServerError, failure)
		return
	}
	kit.JSON(w, r, http.StatusOK, okAnswer{true})
}

// deleteRelay answers DELETE /api/panel/scaling/relays/{nodeId}.
func (h *handlers) deleteRelay(w http.ResponseWriter, r *http.Request) {
	h.remove(w, r, keyRelays, r.PathValue("nodeId"), "failed to delete relay node")
}

const noTelemetry = "live relay telemetry not yet available"

// relayHealth answers GET /api/panel/scaling/relays/{nodeId}/health. The
// console has no live data of relays: the answer is the same for every
// id, known or not.
func (h *handlers) relayHealth(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, noteAnswer{Data: nil, Note: noTelemetry})
}

// relayMetrics answers GET /api/panel/scaling/relays/{nodeId}/metrics
// with an empty list, for every id.
func (h *handlers) relayMetrics(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, noteAnswer{Data: []any{}, Note: noTelemetry})
}

type capacityData struct {
	RelayCount       int     `json:"relay_count"`
	MaxDevices       int     `json:"max_devices"`
	MaxSessions      float64 `json:"max_sessions"`
	MaxBandwidthMbps float64 `json:"max_bandwidth_mbps"`
}

// capacity answers GET /api/panel/scaling/capacity: the sums of what the
// relays are configured for, 50 sessions and 100 Mbit/s when they sum to
// nothing, and 500 devices plus 200 for each relay.
func (h *handlers) capacity(w http.ResponseWriter, r *http.Request) {
	relays, err := h.load(r.Context(), keyRelays)
	if err != nil {
		h.log.Error("[Scaling] capacity:", err)
		fail(w, r, http.StatusInternalServerError, "failed to compute capacity")
		return
	}
	data := capacityData{RelayCount: len(relays), MaxDevices: 500 + len(relays)*200}
	for _, relay := range relays {
		sessions, err := member(relay, "max_sessions")
		if err != nil {
			fail(w, r, http.StatusInternalServerError, "failed to compute capacity")
			return
		}
		bandwidth, _ := member(relay, "max_bandwidth_mbps")
		data.MaxSessions += number(sessions)
		data.MaxBandwidthMbps += number(bandwidth)
	}
	if data.MaxSessions == 0 {
		data.MaxSessions = 50
	}
	if data.MaxBandwidthMbps == 0 {
		data.MaxBandwidthMbps = 100
	}
	kit.JSON(w, r, http.StatusOK, dataAnswer{data})
}

// number is a stored number, 0 for anything else.
func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}
