package bdapi

import (
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The desktop client's operator mode: sign-in, the device list, and the
// record of the operator's remote sessions.

// operatorLogin answers POST /api/bd/operator/login {username, password,
// device_id}: an access token bound to the device id, for the desktop
// client in operator mode. Only an account with the role admin or
// operator and without a second factor gets one: the desktop client has
// no step that asks for the second factor (DECISIONS A37). The sign-in
// lockout counts here as at every sign-in.
func (h *handlers) operatorLogin(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	username, okName := jsreq.Prop(body, "username").(string)
	password, okPassword := jsreq.Prop(body, "password").(string)
	if !okName || username == "" || !okPassword || password == "" {
		fail(w, r, http.StatusBadRequest, "Username and password are required")
		return
	}
	ip := clientIP(r)
	// The API server's sign-in, when Authenticate asks it, counts the
	// desktop client's address
	ctx := authn.WithClientAddress(r.Context(), ip)
	brute, err := h.d.Authn.CheckBruteForce(ctx, username, ip)
	if err != nil {
		h.serverError(w, r, "Operator login error:", err, "Authentication failed")
		return
	}
	if brute.Blocked {
		fail(w, r, http.StatusTooManyRequests, "Too many failed attempts. Try again later.")
		return
	}
	outcome, err := h.d.Authn.Authenticate(ctx, username, password)
	if err != nil {
		h.serverError(w, r, "Operator login error:", err, "Authentication failed")
		return
	}
	refuse := func(status int, answer any) {
		if err := h.d.Authn.RecordAttempt(ctx, username, ip, false); err != nil {
			h.log.Warn("Operator login attempt not recorded:", err.Error())
		}
		kit.JSON(w, r, status, answer)
	}
	user := outcome.User
	switch {
	case outcome.Failure == authn.FailureUsernameCollision:
		refuse(http.StatusConflict, goapi.NewObject(
			"error", "Username collision: a local account with this name already exists", "code", "username_collision"))
		return
	case user == nil:
		refuse(http.StatusUnauthorized, failure{Error: "Invalid credentials"})
		return
	case !user.Role.Valid || user.Role.String != "admin" && user.Role.String != "operator":
		refuse(http.StatusForbidden, failure{Error: "Insufficient permissions. Admin or operator role required."})
		return
	}
	if err := h.d.Authn.RecordAttempt(ctx, username, ip, true); err != nil {
		h.log.Warn("Operator login attempt not recorded:", err.Error())
	}
	if user.TOTPRequired {
		kit.JSON(w, r, http.StatusForbidden, goapi.NewObject(
			"error", "Two-factor authentication is required for this account", "code", "totp_required"))
		return
	}
	device, err := textOr(jsreq.Prop(body, "device_id"), "")
	if err != nil {
		h.serverError(w, r, "Operator login error:", err, "Authentication failed")
		return
	}
	// The token is stored for a device_id of the form of a device id (3 to
	// 64 letters, digits, "_" and "-"); any other counts as none
	if !deviceIDPattern(device) {
		device = ""
	}
	userID := user.ID.V
	token, err := h.d.Authn.GenerateAccessToken(ctx, userID, device, "", ip)
	if err == nil {
		err = h.d.Authn.TouchLastLogin(ctx, userID)
	}
	if err == nil {
		named := device
		if named == "" {
			named = "unknown"
		}
		err = h.d.Audit.Write(ctx, userID, "operator_login", "Operator login from desktop client ("+named+")", ip)
	}
	if err != nil {
		h.serverError(w, r, "Operator login error:", err, "Authentication failed")
		return
	}
	name := user.Username
	if name == "" {
		name = username
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject(
		"access_token", token,
		"user", goapi.NewObject("name", name, "role", user.Role.String),
	))
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
// the console's table that is not deleted and is in the account's device
// scope, after bringing it up to date with the signal server's, by id.
// hostname and platform come from the device's info where it has them; an
// info that is no JSON object leaves the note as the hostname.
func (h *handlers) operatorDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peers, err := h.d.DeviceGroups.AllPeers(ctx)
	if err != nil {
		h.serverError(w, r, "Operator devices error:", err, "Failed to fetch devices")
		return
	}
	user := callerOf(r).user
	scope, err := h.d.DeviceGroups.DeviceScopeForUser(ctx, &session.User{ID: user.ID.V, Username: user.Username, Role: user.Role.String}, peers)
	if err != nil {
		h.serverError(w, r, "Operator devices error:", err, "Failed to fetch devices")
		return
	}
	peers = access.FilterByScope(peers, scope, peerID)
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

// peerID is the id of a device row as text.
func peerID(p *goapi.Object) string {
	id, _ := text(p.Get("id"))
	return id
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

// operatorSessionType is the connection type of the rows an operator's
// sessions write into the connection audit. The client audit routes take
// only the types 0 to 4, so no one without an operator's token can write a
// row of this type, and the session history reads only rows of it.
const operatorSessionType = 100

// recordSession answers POST /api/bd/operator/sessions {action,
// device_id, hostname, session_id}: the start or end of an operator's
// session with a device goes into the connection audit, under the
// operator's name and the client id of the token, with the connection
// type operatorSessionType.
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
		action: action, connType: operatorSessionType, sessionID: sessionID, ip: clientIP(r),
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
// calling operator's sessions put together from the newest rows of the
// connection audit that the operator recorded through recordSession (four
// rows for each session asked for), the newest first, 100 unless limit
// says otherwise, at most 200.
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
	operator := "operator"
	if c := callerOf(r); c.user != nil && c.user.Username != "" {
		operator = c.user.Username
	}
	rows, err := auditConnections(r.Context(), h.d.Store, operator, limit*4)
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

// createdAt is the time of a connection row as text (createdText) and in
// milliseconds: what Date.parse reads from the text SQLite keeps, the
// instant of a PostgreSQL time.
func (h *handlers) createdAt(t store.Time) (string, float64) {
	text := createdText(h.d.Store, t)
	if instant, ok := t.Instant(); ok && h.d.Store.IsPostgres() {
		return text, float64(instant.UnixMilli())
	}
	return text, timestamp(text)
}

// buildHistory is buildSessionHistory: the start and end rows grouped by
// host and session (by session id, else device and row id), oldest first,
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
		id := e.sess.ToString()
		if !e.sess.Truthy() {
			id = e.hostID.ToString() + ":" + e.peerID.ToString() + ":" + e.id.ToString()
		}
		key := e.hostID.ToString() + "\n" + id
		entry := grouped[key]
		if entry == nil {
			entry = &historyEntry{
				ID:       id,
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
