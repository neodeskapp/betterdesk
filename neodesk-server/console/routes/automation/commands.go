package automation

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// deviceScope is the device scope of the request's account, nil when it
// sees every device: whether there is a limit is decided first, and only
// for an account that has one is the scope read over every device of the
// device layer, deleted ones included, which knows their folders and tags.
func (h *handlers) deviceScope(r *http.Request) (*access.DeviceScope, error) {
	ctx := r.Context()
	return h.d.DeviceGroups.LimitedScope(ctx, kit.User(r), func() []*goapi.Object {
		return h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{IncludeDeleted: true})
	})
}

type commandSent struct {
	Success bool    `json:"success"`
	Command *record `json:"command"`
}

// sendCommand answers POST commands with the body {device_id, payload,
// command_type?} and the command as it is stored: the command waits for
// the device's agent to fetch it.
//
// The device has to be the id, as text, of a device the API server knows
// and the account's device scope holds (403 otherwise), the payload a
// text that is not blank and the type, when given, one of five.
func (h *handlers) sendCommand(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	device := jsbind.Of(body.Get("device_id"))
	if !device.Truthy() {
		fail(w, r, http.StatusBadRequest, "device_id is required")
		return
	}
	if !device.IsString() {
		fail(w, r, http.StatusBadRequest, "Invalid device_id")
		return
	}
	payload := jsbind.Of(body.Get("payload"))
	if !payload.IsString() || jsbind.Trim(payload.Str()) == "" {
		fail(w, r, http.StatusBadRequest, "payload is required")
		return
	}
	commandType := jsbind.Of(body.Get("command_type"))
	if commandType.Truthy() && !oneOf(commandTypes, commandType) {
		fail(w, r, http.StatusBadRequest, "Invalid command_type. Valid: shell, powershell, script, restart_service, reboot")
		return
	}
	commandType = commandType.Or(jsbind.String("shell"))
	if h.d.GoAPI.GetPeer(r.Context(), device.Str()) == nil {
		fail(w, r, http.StatusBadRequest, "Unknown device")
		return
	}
	scope, err := h.deviceScope(r)
	if err != nil {
		h.serverError(w, r, "Command create error", err)
		return
	}
	if !scope.Has(device.Str()) {
		fail(w, r, http.StatusForbidden, "Device is outside your device scope")
		return
	}

	command, err := createCommand(r.Context(), h.db, newCommand{
		DeviceID:    device,
		CommandType: commandType.Str(),
		Payload:     jsbind.Trim(payload.Str()),
		CreatedBy:   kit.User(r).Username,
	})
	if err != nil {
		h.serverError(w, r, "Command create error", err)
		return
	}
	_ = h.d.LogAction(r, "remote_command_sent", commandType.Str()+" to "+device.Str())
	h.log.Info("Command sent to " + device.Str() + ": " + commandType.Str())
	kit.JSON(w, r, http.StatusCreated, commandSent{Success: true, Command: command})
}

type commandList struct {
	Commands []record `json:"commands"`
	Total    int      `json:"total"`
}

// commands answers GET commands?device_id=&status=&limit=: the commands
// of the devices in the account's device scope, the one created last
// first, each without its result (see listCommands). Each filter is one
// text, and an empty one does not filter; the limit is limitOf's.
// Anything else is refused with 400.
func (h *handlers) commands(w http.ResponseWriter, r *http.Request) {
	var filter commandFilter
	if !readFilters(w, r, filterInto{"device_id", &filter.DeviceID}, filterInto{"status", &filter.Status}) {
		return
	}
	limit, ok := limitOf(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid limit")
		return
	}
	filter.Limit = limit
	scope, err := h.deviceScope(r)
	if err != nil {
		h.serverError(w, r, "Commands list error", err)
		return
	}
	if !scope.Unlimited() {
		filter.Devices = scope.IDs()
		filter.Limited = true
	}
	commands, err := listCommands(r.Context(), h.db, filter)
	if err != nil {
		h.serverError(w, r, "Commands list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, commandList{Commands: commands, Total: len(commands)})
}

// command answers GET commands/{id}: the command with its result. A
// command for a device outside the account's device scope is not found.
func (h *handlers) command(w http.ResponseWriter, r *http.Request) {
	id, ok := idOf(r)
	if !ok {
		fail(w, r, http.StatusNotFound, "Command not found")
		return
	}
	command, err := commandByID(r.Context(), h.db, id)
	if err != nil {
		h.serverError(w, r, "Command get error", err)
		return
	}
	if command != nil {
		scope, err := h.deviceScope(r)
		if err != nil {
			h.serverError(w, r, "Command get error", err)
			return
		}
		if !scope.Has(command.text("device_id")) {
			command = nil
		}
	}
	if command == nil {
		fail(w, r, http.StatusNotFound, "Command not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, command)
}
