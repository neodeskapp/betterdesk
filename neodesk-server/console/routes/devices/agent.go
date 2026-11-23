package devices

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/bdrelay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The routes that reach into a running agent: its services, processes,
// event log, activity, files, clipboard, screen, keyboard, mouse and
// terminal. Each asks the agent over its signal socket (bdrelay) and
// answers {"success":true,"data":...} with what the agent sent. An agent
// that is not connected gets the snapshot the server keeps, when there is
// one for the request, else a collection queued for when it comes back,
// else 503 agent_offline. An agent that does not answer in time is 504
// agent_timeout, one that answers with an error 502 and its error, also
// when the agent's error reads agent_offline or agent_timeout.

// commandFor is the collection the server queues for a request while the
// agent is offline.
var commandFor = map[string]string{
	"services.list":  "collect.services",
	"processes.list": "collect.processes",
	"events.list":    "collect.events",
	"activity.get":   "collect.activity",
	"files.browse":   "files.browse",
	"files.read":     "files.read",
}

// snapshotFor is the snapshot of the server's telemetry that stands in
// for a request while the agent is offline.
var snapshotFor = map[string]string{
	"services.list":  "services",
	"processes.list": "processes",
	"events.list":    "events",
	"activity.get":   "activity",
	"files.browse":   "files",
}

// agentRequest is proxyAgentRequest: the device of the path must exist and
// be in the account's scope, then the agent is asked.
func (h *handlers) agentRequest(w http.ResponseWriter, r *http.Request, typ string, payload any, timeout time.Duration) {
	ctx := r.Context()
	id := r.PathValue("id")
	device := h.backend.GetDeviceByID(ctx, id, false)
	if device == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return
	}
	visible, err := h.inScope(w, r, device)
	var data any
	if err == nil {
		if !visible {
			return
		}
		data, err = h.d.Relay.RequestFromDevice(ctx, id, typ, payload, timeout)
	}
	if err == nil {
		answer(w, r, http.StatusOK, withData{Success: true, Data: data})
		return
	}
	if ctx.Err() != nil {
		return
	}
	msg := err.Error()
	var agentErr *bdrelay.AgentError
	if errors.As(err, &agentErr) {
		msg = agentErr.Message
	}
	if msg == "" {
		msg = "agent_error"
	}
	offline := errors.Is(err, bdrelay.ErrAgentOffline)
	if offline {
		if name, ok := snapshotFor[typ]; ok {
			telemetry := h.api.GetPeerTelemetry(ctx, id)
			if snapshot, _ := goapi.Dig(telemetry, "data", "snapshots", name).(*goapi.Object); snapshot != nil && goapi.Truthy(snapshot.Get("data")) {
				cached := goapi.NewObject("success", true, "cached", true)
				if snapshot.Has("collected_at") {
					cached.Set("collected_at", snapshot.Get("collected_at"))
				}
				cached.Set("data", snapshot.Get("data"))
				answer(w, r, http.StatusOK, cached)
				return
			}
		}
		if command, ok := commandFor[typ]; ok {
			args := payload
			if !goapi.Truthy(args) {
				args = goapi.NewObject()
			}
			queued := h.api.QueuePeerTelemetryCommand(ctx, id, command, args)
			if !queued.Has("success") || queued.Get("success") != false {
				answer(w, r, http.StatusAccepted, goapi.NewObject("success", true, "pending", true,
					"data", goapi.NewObject("pending", true, "command", command)))
				return
			}
		}
	}
	status := http.StatusBadGateway
	switch {
	case offline:
		status = http.StatusServiceUnavailable
	case errors.Is(err, bdrelay.ErrAgentTimeout):
		status = http.StatusGatewayTimeout
	}
	answer(w, r, status, failure{Error: msg})
}

const second = time.Second

// services answers GET /api/devices/{id}/services: the agent's services.
func (h *handlers) services(w http.ResponseWriter, r *http.Request) {
	h.agentRequest(w, r, "services.list", nil, 15*second)
}

// processes answers GET /api/devices/{id}/processes.
func (h *handlers) processes(w http.ResponseWriter, r *http.Request) {
	h.agentRequest(w, r, "processes.list", nil, 15*second)
}

// events answers GET /api/devices/{id}/events?limit=: the newest entries
// of the agent's event log, limit between 1 and 500 (100 when it is not a
// number).
func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	raw, err := text(jsreq.Query(r, "limit"))
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	limit := goapipath.ParseInt(raw)
	if math.IsNaN(limit) || limit == 0 {
		limit = 100
	}
	limit = math.Min(math.Max(limit, 1), 500)
	h.agentRequest(w, r, "events.list", goapi.NewObject("limit", limit), 15*second)
}

// activity answers GET /api/devices/{id}/activity.
func (h *handlers) activity(w http.ResponseWriter, r *http.Request) {
	h.agentRequest(w, r, "activity.get", nil, 15*second)
}

// filesBrowse answers POST /api/devices/{id}/files/browse with {path,
// show_hidden}.
func (h *handlers) filesBrowse(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	path := slice(body.Get("path").Text(""), 4096)
	h.agentRequest(w, r, "files.browse", goapi.NewObject("path", path, "show_hidden", body.Get("show_hidden").Flag(false)), 15*second)
}

// filesRead answers POST /api/devices/{id}/files/read with {path, offset,
// length}: offset from 0, length from 0 to 1 MiB (64 KiB by default).
func (h *handlers) filesRead(w http.ResponseWriter, r *http.Request) {
	body := plainBodyValue(r)
	path := slice(body("path").Text(""), 4096)
	offset := body("offset").IntBetween(0, 0, maxSafeInteger)
	length := body("length").IntBetween(65536, 0, 1024*1024)
	h.agentRequest(w, r, "files.read", goapi.NewObject("path", path, "offset", float64(offset), "length", float64(length)), 30*second)
}

const maxSafeInteger = 1<<53 - 1

// plainBodyValue reads members of plainBodyObject(req.body): a body that
// is not an object has none.
func plainBodyValue(r *http.Request) func(name string) middleware.Value {
	body := kit.Body(r)
	return func(name string) middleware.Value { return body.Get(name) }
}

// stringField is String(req.body?.name || "") with an empty fallback. A
// value String() throws on ended the old console's process in the async
// handlers; the request is then dropped without an answer.
func stringField(r *http.Request, name string) string {
	s, err := textOr(value(r, name), "")
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	return s
}

// audit writes the audit entry of a write on the agent; a failure does
// not stop the request.
func (h *handlers) audit(r *http.Request, action, details string) {
	_ = h.d.LogAction(r, action, details)
}

// filesWrite answers POST /api/devices/{id}/files/write with {path, data,
// mode}: mode overwrite, append or create (overwrite otherwise), data at
// most 22 Mi characters. The console takes JSON bodies of at most 2 MB,
// so that check is never reached; it stays as the old route had it.
func (h *handlers) filesWrite(w http.ResponseWriter, r *http.Request) {
	path := slice(stringField(r, "path"), 4096)
	data := stringField(r, "data")
	mode := "overwrite"
	switch m := value(r, "mode"); m {
	case "overwrite", "append", "create":
		mode = m.(string)
	}
	if path == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "path_required"})
		return
	}
	if length(data) > 22*1024*1024 {
		answer(w, r, http.StatusRequestEntityTooLarge, failure{Error: "payload_too_large"})
		return
	}
	h.audit(r, "files.write", fmt.Sprintf("Write %s on %s: %s", mode, r.PathValue("id"), path))
	h.agentRequest(w, r, "files.write", goapi.NewObject("path", path, "data", data, "mode", mode), 30*second)
}

// filesDelete answers POST /api/devices/{id}/files/delete with {path,
// recursive}.
func (h *handlers) filesDelete(w http.ResponseWriter, r *http.Request) {
	path := slice(stringField(r, "path"), 4096)
	recursive := value(r, "recursive") == true
	if path == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "path_required"})
		return
	}
	note := ""
	if recursive {
		note = " (recursive)"
	}
	h.audit(r, "files.delete", fmt.Sprintf("Delete%s on %s: %s", note, r.PathValue("id"), path))
	h.agentRequest(w, r, "files.delete", goapi.NewObject("path", path, "recursive", recursive), 15*second)
}

// filesRename answers POST /api/devices/{id}/files/rename with {from, to}.
func (h *handlers) filesRename(w http.ResponseWriter, r *http.Request) {
	from := slice(stringField(r, "from"), 4096)
	to := slice(stringField(r, "to"), 4096)
	if from == "" || to == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "paths_required"})
		return
	}
	h.audit(r, "files.rename", fmt.Sprintf("Rename on %s: %s -> %s", r.PathValue("id"), from, to))
	h.agentRequest(w, r, "files.rename", goapi.NewObject("from", from, "to", to), 10*second)
}

// filesMkdir answers POST /api/devices/{id}/files/mkdir with {path,
// recursive}; recursive unless it is false.
func (h *handlers) filesMkdir(w http.ResponseWriter, r *http.Request) {
	path := slice(stringField(r, "path"), 4096)
	recursive := value(r, "recursive") != false
	if path == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "path_required"})
		return
	}
	h.audit(r, "files.mkdir", fmt.Sprintf("Mkdir on %s: %s", r.PathValue("id"), path))
	h.agentRequest(w, r, "files.mkdir", goapi.NewObject("path", path, "recursive", recursive), 10*second)
}

// clipboard answers GET /api/devices/{id}/clipboard: the agent's text
// clipboard.
func (h *handlers) clipboard(w http.ResponseWriter, r *http.Request) {
	h.agentRequest(w, r, "clipboard.get", nil, 5*second)
}

// setClipboard answers POST /api/devices/{id}/clipboard with {text}: at
// most 1 Mi characters.
func (h *handlers) setClipboard(w http.ResponseWriter, r *http.Request) {
	text, _ := value(r, "text").(string)
	if length(text) > 1024*1024 {
		answer(w, r, http.StatusRequestEntityTooLarge, failure{Error: "text_too_large"})
		return
	}
	h.audit(r, "clipboard.set", fmt.Sprintf("Clipboard set on %s (%d chars)", r.PathValue("id"), length(text)))
	h.agentRequest(w, r, "clipboard.set", goapi.NewObject("text", text), 5*second)
}

// screenshot answers POST /api/devices/{id}/screenshot: a JPEG of the
// screen from the agent.
func (h *handlers) screenshot(w http.ResponseWriter, r *http.Request) {
	h.agentRequest(w, r, "screenshot.capture", nil, 20*second)
}

// mouse answers POST /api/devices/{id}/input/mouse: one mouse event.
// Coordinates, screen sizes and wheel steps are cut to whole numbers; the
// relative position goes as it came.
func (h *handlers) mouse(w http.ResponseWriter, r *http.Request) {
	action, err := textOr(value(r, "action"), "move")
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	payload := goapi.NewObject("action", action)
	if button, ok := value(r, "button").(string); ok {
		payload.Set("button", button)
	}
	for _, f := range []struct {
		name  string
		whole bool
	}{{"x", true}, {"y", true}, {"x_rel", false}, {"y_rel", false}, {"screen_w", true}, {"screen_h", true}, {"wheel_dx", true}, {"wheel_dy", true}} {
		n, ok := value(r, f.name).(float64)
		if !ok {
			continue
		}
		if f.whole {
			n = math.Trunc(n)
		}
		// JSON.stringify writes -0 as 0
		payload.Set(f.name, n+0)
	}
	h.agentRequest(w, r, "input.mouse", payload, 5*second)
}

// key answers POST /api/devices/{id}/input/key with {key, action}: key at
// most 32 characters, action "press" unless given.
func (h *handlers) key(w http.ResponseWriter, r *http.Request) {
	key, err := textOr(value(r, "key"), "")
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	key = slice(key, 32)
	if key == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "key_required"})
		return
	}
	action, err := textOr(value(r, "action"), "press")
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	h.agentRequest(w, r, "input.key", goapi.NewObject("key", key, "action", action), 5*second)
}

// typeText answers POST /api/devices/{id}/input/text with {text}: at most
// 4096 characters.
func (h *handlers) typeText(w http.ResponseWriter, r *http.Request) {
	s, err := textOr(value(r, "text"), "")
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	s = slice(s, 4096)
	if s == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "text_required"})
		return
	}
	h.agentRequest(w, r, "input.text", goapi.NewObject("text", s), 8*second)
}

// terminal answers POST /api/devices/{id}/terminal/execute with {command}:
// one command, no terminal session, at most 4096 characters. It is
// written to the audit log with its first 200 characters.
func (h *handlers) terminal(w http.ResponseWriter, r *http.Request) {
	command := slice(stringField(r, "command"), 4096)
	if jsTrim(command) == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "command_required"})
		return
	}
	h.audit(r, "terminal.execute", fmt.Sprintf("Terminal command on %s: %s", r.PathValue("id"), slice(command, 200)))
	h.agentRequest(w, r, "terminal.execute", goapi.NewObject("command", command), 30*second)
}
