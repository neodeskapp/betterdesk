package bdapi

import (
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The desktop client's operator mode: sign-in, the device list, and the
// record of the operator's remote sessions.

// operatorLogin answers POST /api/bd/operator/login {username, password,
// device_id}. The route tested the brute force check's answer, an object,
// for its truth, so every request with a name and a password is refused
// as blocked and no token is ever issued (BUGS S4). What the route did
// after that check never runs.
func (h *handlers) operatorLogin(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	username, okName := jsreq.Prop(body, "username").(string)
	password, okPassword := jsreq.Prop(body, "password").(string)
	if !okName || username == "" || !okPassword || password == "" {
		fail(w, r, http.StatusBadRequest, "Username and password are required")
		return
	}
	if _, err := h.d.Authn.CheckBruteForce(r.Context(), username, clientIP(r)); err != nil {
		h.serverError(w, r, "Operator login error:", err, "Authentication failed")
		return
	}
	fail(w, r, http.StatusTooManyRequests, "Too many failed attempts. Try again later.")
}

type operatorDevice struct {
	ID         any  `json:"id"`
	Hostname   any  `json:"hostname"`
	Platform   any  `json:"platform"`
	Online     bool `json:"online"`
	LastOnline any  `json:"last_online"`
}

type devicesAnswer struct {
	Success bool             `json:"success"`
	Devices []operatorDevice `json:"devices"`
}

// operatorDevices answers GET /api/bd/operator/devices: every device of
// the console's table that is not deleted, after bringing it up to date
// with the signal server's, by id. hostname and platform come from the
// device's info where it has them; an info that is no JSON object leaves
// the note as the hostname.
func (h *handlers) operatorDevices(w http.ResponseWriter, r *http.Request) {
	peers, err := h.d.DeviceGroups.AllPeers(r.Context())
	if err != nil {
		h.serverError(w, r, "Operator devices error:", err, "Failed to fetch devices")
		return
	}
	devices := make([]operatorDevice, 0, len(peers))
	for _, p := range peers {
		note := orValue(p.Get("note"), "")
		hostname, platform := any(""), any("")
		hostname = note
		if info, err := peerInfo(p.Get("info")); err == nil {
			if name := info.Get("hostname"); name.Truthy() {
				hostname = name
			}
			if os := info.Get("os").Or(info.Get("platform")); os.Truthy() {
				platform = os
			}
		}
		devices = append(devices, operatorDevice{
			ID:         p.Get("id"),
			Hostname:   hostname,
			Platform:   platform,
			Online:     goapi.Truthy(orValue(p.Get("status_online"), p.Get("online"))),
			LastOnline: orValue(p.Get("last_online"), ""),
		})
	}
	kit.JSON(w, r, http.StatusOK, devicesAnswer{Success: true, Devices: devices})
}

// peerInfo is `typeof p.info === 'string' ? JSON.parse(p.info) : (p.info
// || {})` followed by a read of its members: a text that is no JSON, and
// one that is JSON null, fail.
func peerInfo(v any) (jsjson.Value, error) {
	s, ok := v.(string)
	if !ok {
		if goapi.Truthy(v) {
			return jsjson.Parse(jsreq.Stringify(v))
		}
		return jsjson.Parse("{}")
	}
	info, err := jsjson.Parse(s)
	if err == nil && info.Kind() == jsjson.Null {
		err = errNullInfo
	}
	return info, err
}

// errNullInfo is the TypeError of reading a member of null.
var errNullInfo = errNull{}

type errNull struct{}

func (errNull) Error() string { return "Cannot read properties of null" }

// sessionAction is normalizeSessionAction: start or session_start,
// end or session_end, in any letter case and with white space around.
func sessionAction(v any) (string, error) {
	s, err := textOr(v, "")
	if err != nil {
		return "", err
	}
	switch jsbind.ToLower(trim(s)) {
	case "start", "session_start":
		return "session_start", nil
	case "end", "session_end":
		return "session_end", nil
	}
	return "", nil
}

// recordSession answers POST /api/bd/operator/sessions {action,
// device_id, hostname, session_id}: the start or end of an operator's
// session with a device goes into the connection audit, under the
// operator's name and the client id of the token.
func (h *handlers) recordSession(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	field := func(name, fallback string) (string, error) { return textOr(jsreq.Prop(body, name), fallback) }
	action, err := sessionAction(jsreq.Prop(body, "action"))
	var deviceID, hostname, sessionID string
	if err == nil {
		deviceID, err = field("device_id", "")
	}
	if err == nil {
		hostname, err = field("hostname", "")
	}
	if err == nil {
		sessionID, err = field("session_id", "")
		if err == nil && !goapi.Truthy(jsreq.Prop(body, "session_id")) {
			sessionID = randomUUID(h.random)
		}
	}
	if err != nil {
		h.serverError(w, r, "Record operator session error:", err, "Failed to record operator session")
		return
	}
	deviceID, hostname, sessionID = trim(deviceID), cut(trim(hostname), 128), cut(trim(sessionID), 96)
	if action == "" {
		fail(w, r, http.StatusBadRequest, "Invalid session action")
		return
	}
	if !deviceIDPattern(deviceID) {
		fail(w, r, http.StatusBadRequest, "Invalid device_id")
		return
	}
	c := callerOf(r)
	operator := "operator"
	if c.user != nil && c.user.Username != "" {
		operator = c.user.Username
	}
	clientID := ""
	if c.token != nil {
		clientID = cut(c.token.ClientID.String, 64)
	}
	err = insertConnection(r.Context(), h.d.Store, connection{
		hostID: operator, hostUUID: clientID, peerID: deviceID, peerName: hostname,
		action: action, connType: 1, sessionID: sessionID, ip: clientIP(r),
	})
	if err != nil {
		h.serverError(w, r, "Record operator session error:", err, "Failed to record operator session")
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "session_id", sessionID, "action", action))
}

// historyEntry is a session in the history, in the order of its keys.
// The times are kept as text, as String() made them, and as the
// milliseconds Date.parse reads from that text.
type historyEntry struct {
	ID           string `json:"id"`
	DeviceID     any    `json:"device_id"`
	Hostname     any    `json:"hostname"`
	Operator     any    `json:"operator"`
	StartedAt    string `json:"started_at"`
	EndedAt      string `json:"ended_at"`
	DurationSecs int64  `json:"duration_secs"`
	Action       any    `json:"action"`

	startedMs, endedMs float64
}

type historyAnswer struct {
	Success  bool            `json:"success"`
	Sessions []*historyEntry `json:"sessions"`
	Count    int             `json:"count"`
}

// sessionHistory answers GET /api/bd/operator/sessions?limit=: the
// operator sessions put together from the newest rows of the connection
// audit (four rows for each session asked for), the newest first, 100
// unless limit says otherwise, at most 200. Every connection of the audit
// counts, not only the operator's own.
func (h *handlers) sessionHistory(w http.ResponseWriter, r *http.Request) {
	limit := 100
	raw := jsreq.Query(r, "limit")
	if raw != nil {
		if _, err := text(raw); err != nil {
			h.serverError(w, r, "Session history error:", err, "Failed to fetch session history")
			return
		}
	}
	if n, ok := jsreq.ParseInt(raw, 10); ok && !math.IsInf(n, 0) {
		limit = int(math.Min(math.Max(n, 1), 200))
	}
	rows, err := auditConnections(r.Context(), h.d.Store, limit*4)
	if err != nil {
		h.serverError(w, r, "Session history error:", err, "Failed to fetch session history")
		return
	}
	sessions := h.buildHistory(rows, limit)
	kit.JSON(w, r, http.StatusOK, historyAnswer{Success: true, Sessions: sessions, Count: len(sessions)})
}

// timestamp is toTimestamp of the route file for a time as text:
// Date.parse of it, 0 when it cannot be read.
func timestamp(s string) float64 {
	if ms, ok := jsreq.ParseDate(s, time.Local); ok {
		return float64(ms)
	}
	return 0
}

// createdAt is String(entry.created_at) and toTimestamp of it: the text
// SQLite keeps and what Date.parse reads from it, or for a PostgreSQL time
// Date.prototype.toString and its instant to the second, which is all that
// text holds.
func (h *handlers) createdAt(t store.Time) (string, float64) {
	text := createdText(h.d.Store, t)
	if instant, ok := t.Instant(); ok && h.d.Store.IsPostgres() {
		return text, float64(instant.Truncate(time.Second).UnixMilli())
	}
	return text, timestamp(text)
}

// buildHistory is buildSessionHistory: the start and end rows grouped by
// session (by session id, else host, device and row id), oldest first,
// each session with the time of its start and its end as the audit wrote
// them and its length in seconds up to now when it has not ended.
func (h *handlers) buildHistory(entries []auditEntry, limit int) []*historyEntry {
	now := float64(h.now().UnixMilli())
	type row struct {
		e       auditEntry
		created string
		at      float64
	}
	var rows []row
	for _, e := range entries {
		action := e.action.ToString()
		if !e.action.IsString() || action != "session_start" && action != "session_end" {
			continue
		}
		created, at := h.createdAt(e.createdAt)
		rows = append(rows, row{e: e, created: created, at: at})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at < rows[j].at })

	var order []string
	grouped := map[string]*historyEntry{}
	for _, rw := range rows {
		e := rw.e
		key := e.sess.ToString()
		if !e.sess.Truthy() {
			key = e.hostID.ToString() + ":" + e.peerID.ToString() + ":" + e.id.ToString()
		}
		entry := grouped[key]
		if entry == nil {
			entry = &historyEntry{
				ID:       key,
				DeviceID: valueOr(e.peerID, ""),
				Hostname: valueOr(e.peerName, ""),
				Operator: valueOr(e.hostID, "operator"),
				Action:   valueOr(e.action, "session_start"),
			}
			grouped[key] = entry
			order = append(order, key)
		}
		if !goapi.Truthy(entry.DeviceID) && e.peerID.Truthy() {
			entry.DeviceID = valueOr(e.peerID, "")
		}
		if !goapi.Truthy(entry.Hostname) && e.peerName.Truthy() {
			entry.Hostname = valueOr(e.peerName, "")
		}
		switch e.action.Str() {
		case "session_start":
			if rw.created != "" {
				entry.StartedAt, entry.startedMs = rw.created, rw.at
			}
			entry.Action = "session_start"
		case "session_end":
			if rw.created != "" {
				entry.EndedAt, entry.endedMs = rw.created, rw.at
			}
			entry.Action = "session_end"
		}
	}

	out := make([]*historyEntry, 0, len(order))
	for _, key := range order {
		entry := grouped[key]
		started := entry.startedMs
		ended := now
		if entry.EndedAt != "" {
			ended = entry.endedMs
		}
		if started > 0 && ended >= started {
			entry.DurationSecs = int64(math.Max(0, math.Floor((ended-started)/1000+0.5)))
		}
		out = append(out, entry)
	}
	at := func(e *historyEntry) float64 {
		if e.StartedAt != "" {
			return e.startedMs
		}
		return e.endedMs
	}
	sort.SliceStable(out, func(i, j int) bool { return at(out[i]) > at(out[j]) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// valueOr is (v || fallback) for a column value.
func valueOr(v jsbind.Value, fallback string) any {
	if v.Truthy() {
		return jsbind.CellOf(cellValue(v))
	}
	return fallback
}

// cellValue is the Go value of a column value for jsbind.CellOf.
func cellValue(v jsbind.Value) any {
	switch {
	case v.IsString():
		return v.Str()
	case v.IsNumber():
		return v.ToNumber()
	}
	return v.ToString()
}
