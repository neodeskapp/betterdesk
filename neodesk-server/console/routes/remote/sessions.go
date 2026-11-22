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
// a session on the remote relay, those of the devices the account's
// device scope holds.
func (h *handlers) sessions(w http.ResponseWriter, r *http.Request) {
	ids := []string{}
	if h.src.relay != nil {
		if active := h.src.relay.ActiveSessions(); active != nil {
			ids = active
		}
	}
	visible, err := h.inScope(r, ids)
	if err != nil {
		h.scopeFailed(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, sessionList{Sessions: visible})
}

// session answers GET /api/remote/session/:deviceId with the state of a
// device's session on the remote relay. A device outside the account's
// device scope has no session the account may see.
func (h *handlers) session(w http.ResponseWriter, r *http.Request) {
	if h.src.relay == nil {
		kit.JSON(w, r, http.StatusNotFound, sessionFailure{Error: "Remote relay not available"})
		return
	}
	id := r.PathValue("deviceId")
	visible, err := h.inScope(r, []string{id})
	if err != nil {
		h.scopeFailed(w, r, err)
		return
	}
	if len(visible) == 0 {
		kit.JSON(w, r, http.StatusNotFound, sessionFailure{Error: "Session not found"})
		return
	}
	state := h.src.relay.SessionState(id)
	if state == nil {
		kit.JSON(w, r, http.StatusNotFound, sessionFailure{Error: "Session not found"})
		return
	}
	kit.JSON(w, r, http.StatusOK, state)
}

// inScope keeps the device ids, in their order, whose device the
// account's device scope holds. For an account without a limit every id
// stays and no device is looked up; with a limit, an id whose device the
// console does not know, or cannot read, is left out.
func (h *handlers) inScope(r *http.Request, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	ctx, user := r.Context(), kit.User(r)
	unlimited, err := h.src.canAccess(ctx, user, nil)
	if err != nil || unlimited {
		return ids, err
	}
	visible := make([]string, 0, len(ids))
	for _, id := range ids {
		device, err := h.src.relay.Device(ctx, id)
		if err != nil || device == nil {
			continue
		}
		allowed, err := h.src.canAccess(ctx, user, device)
		if err != nil {
			return nil, err
		}
		if allowed {
			visible = append(visible, id)
		}
	}
	return visible, nil
}

// scopeFailed answers a device scope check that failed, as the device
// scope guard of the device routes does (kit.RequireDeviceInScope).
func (h *handlers) scopeFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Device scope check failed:", err.Error())
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
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
