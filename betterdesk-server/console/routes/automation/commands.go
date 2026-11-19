package automation

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type commandSent struct {
	Success bool `json:"success"`
	Command any  `json:"command"`
}

// sendCommand answers POST commands with the body {device_id, payload,
// command_type?}: the command waits for the device's agent to fetch it.
//
// The payload has to be a text and the type, when given, one of five.
// The device can be anything that is not empty: the database decides what
// it stores for a number or a list.
//
// A device that JavaScript cannot make text of (an object with a member
// named toString) gets its command stored on PostgreSQL and the request
// answered with 500 all the same, without an audit log entry: the old
// route failed while it wrote its log line.
func (h *handlers) sendCommand(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	device := member(body, "device_id")
	if !device.value.Truthy() {
		fail(w, r, http.StatusBadRequest, "device_id is required")
		return
	}
	payload := jsbind.Of(body.Get("payload"))
	if !payload.Truthy() {
		fail(w, r, http.StatusBadRequest, "payload is required")
		return
	}
	if !payload.IsString() {
		h.serverError(w, r, "Command create error", errNotAFunction("payload.trim"))
		return
	}
	if jsbind.Trim(payload.Str()) == "" {
		fail(w, r, http.StatusBadRequest, "payload is required")
		return
	}
	commandType := jsbind.Of(body.Get("command_type"))
	if commandType.Truthy() && !oneOf(commandTypes, commandType) {
		fail(w, r, http.StatusBadRequest, "Invalid command_type. Valid: shell, powershell, script, restart_service, reboot")
		return
	}
	commandType = commandType.Or(jsbind.String("shell"))

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
	deviceText, err := asText(body.Get("device_id"))
	if err != nil {
		h.serverError(w, r, "Command create error", err)
		return
	}
	_ = h.d.LogAction(r, "remote_command_sent", commandType.Str()+" to "+deviceText)
	h.log.Info("Command sent to " + deviceText + ": " + commandType.Str())
	kit.JSON(w, r, http.StatusCreated, commandSent{Success: true, Command: command})
}

type commandList struct {
	Commands []record `json:"commands"`
	Total    int      `json:"total"`
}

// commands answers GET commands?device_id=&status=&limit=: the commands,
// the one created last first. Each filter is one text, and an empty one
// does not filter; the limit is limitOf's. Anything else is refused with
// 400.
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
	commands, err := listCommands(r.Context(), h.db, filter)
	if err != nil {
		h.serverError(w, r, "Commands list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, commandList{Commands: commands, Total: len(commands)})
}

// command answers GET commands/{id}: the command with its result.
func (h *handlers) command(w http.ResponseWriter, r *http.Request) {
	command, err := commandByID(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.serverError(w, r, "Command get error", err)
		return
	}
	if command == nil {
		fail(w, r, http.StatusNotFound, "Command not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, command)
}
