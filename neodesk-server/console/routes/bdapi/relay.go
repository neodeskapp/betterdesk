package bdapi

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/bdrelay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The relay between two devices: a device asks to connect to another,
// gets a session and the token of its side, and the target hears of it on
// its signal socket, or with its next heartbeat. The sessions and sockets
// are d.Relay.

type heartbeatAnswer struct {
	Success            bool              `json:"success"`
	ServerTime         int64             `json:"server_time"`
	PendingConnections []bdrelay.Pending `json:"pending_connections"`
}

// heartbeat answers POST /api/nd/heartbeat {device_id}: the device is
// online, with the sessions that wait for it. The device is the body's
// device_id when that is truthy, which the match guard compared by its
// text (so 900100001 and ["900100001"] pass for that device), else the
// token's; a value that is no text marks the device the database finds
// for it and waits for no session.
func (h *handlers) heartbeat(w http.ResponseWriter, r *http.Request) {
	// device_id is required: never answered, the token names a device
	id := jsbind.String(kit.Device(r).DeviceID)
	if raw := kit.Body(r).Get("device_id"); raw.Truthy() {
		id = jsbind.Of(raw)
	}
	_ = updateOnlineStatus(r.Context(), h.d.Store, id)
	pending := []bdrelay.Pending{}
	if id.IsString() {
		pending = h.d.Relay.PendingFor(id.Str())
	}
	kit.JSON(w, r, http.StatusOK, heartbeatAnswer{Success: true, ServerTime: h.now().UnixMilli(), PendingConnections: pending})
}

type connectAnswer struct {
	Success      bool   `json:"success"`
	SessionID    string `json:"session_id"`
	Token        string `json:"token"`
	TargetOnline bool   `json:"target_online"`
	RelayURL     string `json:"relay_url"`
}

// connect answers POST /api/nd/connect {target_id, initiator_id,
// public_key}: a relay session from the token's device to the target. The
// token must be a device token, and initiator_id, when given, must be that
// device. The target must be a device the console knows that is not
// banned. A target whose socket is open is told at once; the answer says
// whether it was.
//
// A target_id that is no text finds its device as the database compares
// it, but the session waits for a device with no id, which never comes.
func (h *handlers) connect(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	target := jsreq.Prop(body, "target_id")
	if !goapi.Truthy(target) {
		fail(w, r, http.StatusBadRequest, "target_id is required")
		return
	}
	c := callerOf(r)
	source := ""
	switch {
	case c.token.IsDeviceToken():
		source = c.token.ClientID.String
	case c.byDeviceToken:
		source = c.deviceID
	}
	if source == "" {
		fail(w, r, http.StatusForbidden, "A device-bound access token is required")
		return
	}
	if initiator := jsreq.Prop(body, "initiator_id"); goapi.Truthy(initiator) && initiator != source {
		fail(w, r, http.StatusForbidden, "initiator_id mismatch")
		return
	}
	found, err := h.deviceByID(r.Context(), jsbind.Of(kit.Body(r).Get("target_id")))
	if err != nil {
		h.serverError(w, r, "Connect error:", err, "Connection request failed")
		return
	}
	if found == nil {
		fail(w, r, http.StatusNotFound, "Target device not found")
		return
	}
	if found.banned {
		fail(w, r, http.StatusForbidden, "Target device is banned")
		return
	}
	targetID, _ := target.(string)
	sessionID, initiatorToken, _ := h.d.Relay.CreateRelaySession(source, targetID)
	notified := false
	if targetID != "" {
		notified = h.d.Relay.NotifyTarget(targetID, goapi.NewObject(
			"type", "incoming_connection",
			"session_id", sessionID,
			"initiator_id", source,
			"initiator_pk", orValue(jsreq.Prop(body, "public_key"), nil),
		))
	}
	kit.JSON(w, r, http.StatusOK, connectAnswer{
		Success: true, SessionID: sessionID, Token: initiatorToken, TargetOnline: notified,
		RelayURL: "/ws/nd-relay?session=" + sessionID + "&token=" + initiatorToken + "&role=initiator",
	})
}

type sessionAnswer struct {
	Success bool                 `json:"success"`
	Session *bdrelay.SessionInfo `json:"session"`
}

// session answers GET /api/nd/session/{id} for the two devices of the
// session.
func (h *handlers) session(w http.ResponseWriter, r *http.Request) {
	info := h.d.Relay.GetRelaySession(r.PathValue("id"))
	if info == nil {
		fail(w, r, http.StatusNotFound, "Session not found or expired")
		return
	}
	device := kit.Device(r).DeviceID
	if info.InitiatorID != device && info.TargetID != device {
		fail(w, r, http.StatusForbidden, "Not a participant of this session")
		return
	}
	kit.JSON(w, r, http.StatusOK, sessionAnswer{Success: true, Session: info})
}

// endSession answers DELETE /api/nd/session/{id}: one of the two devices
// drops the session. Its sockets stay open.
func (h *handlers) endSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info := h.d.Relay.GetRelaySession(id)
	if info == nil {
		fail(w, r, http.StatusNotFound, "Session not found")
		return
	}
	device := kit.Device(r).DeviceID
	if info.InitiatorID != device && info.TargetID != device {
		fail(w, r, http.StatusForbidden, "Not a participant of this session")
		return
	}
	h.d.Relay.DeleteSession(id)
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

type onlinePeer struct {
	ID       any    `json:"id"`
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
	Online   bool   `json:"online"`
}

type peersAnswer struct {
	Success bool         `json:"success"`
	Peers   []onlinePeer `json:"peers"`
}

// peers answers GET /api/nd/peers: the devices with an open signal socket
// that the console knows and has not banned.
func (h *handlers) peers(w http.ResponseWriter, r *http.Request) {
	out := []onlinePeer{}
	for _, id := range h.d.Relay.OnlineDeviceIDs() {
		found, err := h.deviceByID(r.Context(), jsbind.String(id))
		if err != nil {
			h.serverError(w, r, "Peers error:", err, "Failed to list peers")
			return
		}
		if found != nil && !found.banned {
			out = append(out, onlinePeer{ID: found.id, Hostname: found.note, Online: true})
		}
	}
	kit.JSON(w, r, http.StatusOK, peersAnswer{Success: true, Peers: out})
}

type peerAnswerInfo struct {
	ID       any    `json:"id"`
	Hostname string `json:"hostname"`
	// Online is missing for a device that never had a signal socket.
	Online *bool `json:"online,omitempty"`
	Banned bool  `json:"banned"`
}

type peerAnswer struct {
	Success bool           `json:"success"`
	Peer    peerAnswerInfo `json:"peer"`
}

// peer answers GET /api/nd/peer/{id}.
func (h *handlers) peer(w http.ResponseWriter, r *http.Request) {
	found, err := h.deviceByID(r.Context(), jsbind.String(r.PathValue("id")))
	if err != nil {
		h.serverError(w, r, "Peer error:", err, "Failed to get peer info")
		return
	}
	if found == nil {
		fail(w, r, http.StatusNotFound, "Device not found")
		return
	}
	info := peerAnswerInfo{ID: found.id, Hostname: found.note, Banned: found.banned}
	if online, known := h.d.Relay.IsDeviceOnline(found.id.Value().ToString()); known {
		info.Online = &online
	}
	kit.JSON(w, r, http.StatusOK, peerAnswer{Success: true, Peer: info})
}
