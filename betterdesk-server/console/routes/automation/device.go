package automation

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// The routes of the device agent. RequireDeviceToken comes first: the
// request's device is the one its token is bound to, and nothing the
// request names counts.

// resultStatuses are the states an agent may report for a command.
var resultStatuses = []string{"completed", "failed", "running"}

type pendingList struct {
	Commands []record `json:"commands"`
}

// pendingCommands answers GET commands for an agent: the commands of the
// token's device that wait for it, the one created first first.
func (h *handlers) pendingCommands(w http.ResponseWriter, r *http.Request) {
	commands, err := pendingCommandsOf(r.Context(), h.db, kit.Device(r).DeviceID)
	if err != nil {
		h.serverError(w, r, "Pending commands error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, pendingList{Commands: commands})
}

// commandResult answers POST commands/{id}/result with the body {status,
// result?}: the agent reports that a command of its device runs, or has
// completed or failed. The status is checked first, then whether the
// command exists, then whether it is the device's. The result is stored
// as String() made it, or NULL when there is none; any state may follow
// any other, also after a command completed.
func (h *handlers) commandResult(w http.ResponseWriter, r *http.Request) {
	device := kit.Device(r).DeviceID
	body := kit.Body(r)
	status := jsbind.Of(body.Get("status"))
	if !status.Truthy() || !oneOf(resultStatuses, status) {
		fail(w, r, http.StatusBadRequest, "Status must be completed, failed, or running")
		return
	}
	command, err := commandByID(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.serverError(w, r, "Command result error", err)
		return
	}
	if command == nil {
		fail(w, r, http.StatusNotFound, "Command not found")
		return
	}
	// cmd.device_id !== req.deviceId
	if owner := command.number("device_id"); !owner.IsString() || owner.Str() != device {
		fail(w, r, http.StatusForbidden, "Device ID mismatch")
		return
	}
	result := jsbind.Null
	if raw := body.Get("result"); raw.Exists() {
		text, err := asText(raw)
		if err != nil {
			h.serverError(w, r, "Command result error", err)
			return
		}
		result = jsbind.String(text)
	}
	id := command.number("id")
	if err := updateRemoteCommand(r.Context(), h.db, id, status.Str(), result); err != nil {
		h.serverError(w, r, "Command result error", err)
		return
	}
	h.log.Info("Command #" + id.ToString() + " result from " + device + ": " + status.Str())
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}
