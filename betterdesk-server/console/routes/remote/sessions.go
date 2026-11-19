package remote

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
)

type sessionList struct {
	Sessions []string `json:"sessions"`
}

type sessionFailure struct {
	Error string `json:"error"`
}

// sessions answers GET /api/remote/sessions with the device ids that have
// a session on the remote relay. The relay never forgets one.
func (h *handlers) sessions(w http.ResponseWriter, r *http.Request) {
	ids := []string{}
	if h.src.relay != nil {
		if active := h.src.relay.ActiveSessions(); active != nil {
			ids = active
		}
	}
	kit.JSON(w, r, http.StatusOK, sessionList{Sessions: ids})
}

// session answers GET /api/remote/session/:deviceId with the state of a
// device's session on the remote relay.
func (h *handlers) session(w http.ResponseWriter, r *http.Request) {
	if h.src.relay == nil {
		kit.JSON(w, r, http.StatusNotFound, sessionFailure{Error: "Remote relay not available"})
		return
	}
	state := h.src.relay.SessionState(r.PathValue("deviceId"))
	if state == nil {
		kit.JSON(w, r, http.StatusNotFound, sessionFailure{Error: "Session not found"})
		return
	}
	kit.JSON(w, r, http.StatusOK, state)
}

// agentToken is the answer of POST /api/bd/remote-agent-token.
type agentToken struct {
	Success   bool   `json:"success"`
	DeviceID  string `json:"device_id"`
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// remoteAgentToken answers POST /api/bd/remote-agent-token: the
// single-use token an agent opens its remote relay socket with, for the
// device of its device token. The body's device_id, when it is not the
// empty text, must be that device's id; the device guards in front refuse
// one that is no text.
func (h *handlers) remoteAgentToken(w http.ResponseWriter, r *http.Request) {
	deviceID := kit.Device(r).DeviceID
	id := deviceID
	if claim, isText := kit.Body(r).Get("device_id").Str(); isText && claim != "" {
		id = claim
	}
	if id == "" || !deviceIDPattern.MatchString(id) {
		kit.JSON(w, r, http.StatusBadRequest, sessionFailure{Error: "device_id is required"})
		return
	}
	if id != deviceID {
		kit.JSON(w, r, http.StatusForbidden, sessionFailure{Error: "device_id mismatch"})
		return
	}
	if h.src.relay == nil {
		h.log.Error("[BD-API] remote-agent-token error: the remote relay is not running")
		kit.JSON(w, r, http.StatusInternalServerError, sessionFailure{Error: "Token issuance failed"})
		return
	}
	token, expiresIn := h.src.relay.IssueRemoteAgentToken(id)
	kit.JSON(w, r, http.StatusOK, agentToken{Success: true, DeviceID: id, Token: token, ExpiresIn: expiresIn})
}
