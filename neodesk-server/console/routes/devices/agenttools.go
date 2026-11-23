package devices

import (
	"net/http"
	"slices"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// agentTool is what an account needs for one of the NeoDesk agent's
// console tools, in the words of the Permissions page and of Panel access:
// the permission, and the client feature that turns the tool off ("" for
// none). The lists of services, processes, events and activity need View
// devices; the terminal and the files have permissions of their own, as
// on MeshCentral agents; the screen, the input and the clipboard are
// remote control and need Connect to devices. Edit devices opens none of
// them (L388), and each client feature switch covers every route of its
// feature (L389).
type agentTool struct {
	permission string
	feature    string
}

var (
	viewTool     = agentTool{auth.PermDeviceView, ""}
	refreshTool  = agentTool{auth.PermDeviceEdit, ""}
	filesTool    = agentTool{auth.PermAgentFiles, "file_transfer"}
	terminalTool = agentTool{auth.PermAgentTerminal, "terminal"}
	controlTool  = agentTool{auth.PermDeviceConnect, "remote_control"}
	clipTool     = agentTool{auth.PermDeviceConnect, "clipboard"}
)

// agentTools are the requests the console sends an agent, by type, with
// what each needs. agentRequest sends no type that is not here.
var agentTools = map[string]agentTool{
	"services.list":      viewTool,
	"processes.list":     viewTool,
	"events.list":        viewTool,
	"activity.get":       viewTool,
	"files.browse":       filesTool,
	"files.read":         filesTool,
	"files.write":        filesTool,
	"files.delete":       filesTool,
	"files.rename":       filesTool,
	"files.mkdir":        filesTool,
	"clipboard.get":      clipTool,
	"clipboard.set":      clipTool,
	"screenshot.capture": controlTool,
	"input.mouse":        controlTool,
	"input.key":          controlTool,
	"input.text":         controlTool,
	"terminal.execute":   terminalTool,
}

// telemetryTools are the commands the console queues for a device, with
// what each needs: a collection is a refresh, the files and the control of
// services and processes are the agent's tools. telemetryCommand queues no
// command that is not here.
var telemetryTools = map[string]agentTool{
	"collect.hardware":  refreshTool,
	"collect.metrics":   refreshTool,
	"collect.services":  refreshTool,
	"collect.processes": refreshTool,
	"collect.events":    refreshTool,
	"collect.activity":  refreshTool,
	"files.browse":      filesTool,
	"files.read":        filesTool,
	"service.control":   controlTool,
	"process.terminate": controlTool,
}

// featureRefusals are the answers of a tool whose client feature is off.
var featureRefusals = map[string]string{
	"terminal":       "The terminal is turned off for your account",
	"file_transfer":  "File transfer is turned off for your account",
	"remote_control": "Remote control is turned off for your account",
	"clipboard":      "The clipboard is turned off for your account",
}

// everyClientFeature is what an account may not use when its client
// features cannot be looked up.
var everyClientFeature = []string{"remote_control", "file_transfer", "clipboard", "audio", "tunnel", "terminal", "reboot", "privacy_mode"}

// featuresOff returns the client features the request's account may not
// use; every feature when the lookup fails.
func (h *handlers) featuresOff(r *http.Request) []string {
	off, err := h.d.Access.ClientFeaturesOff(r.Context(), kit.Access(r).UserID)
	if err != nil {
		h.log.Warn("[DEVICES] client features of the account could not be read:", err.Error())
		return everyClientFeature
	}
	return off
}

// mayUse reports whether the request's account may use the tool. When it
// may not, the refusal has been sent: the permission's, as RequirePermission
// sends it, or the client feature's.
func (h *handlers) mayUse(w http.ResponseWriter, r *http.Request, tool agentTool) bool {
	if decision := kit.Access(r).RequirePermission(tool.permission, httpx.OriginalURL(r)); !decision.Passed() {
		h.d.Refuse(w, r, decision)
		return false
	}
	if tool.feature != "" && slices.Contains(h.featuresOff(r), tool.feature) {
		answer(w, r, http.StatusForbidden, failure{Error: featureRefusals[tool.feature]})
		return false
	}
	return true
}

// allowed is mayUse without an answer.
func (h *handlers) allowed(r *http.Request, tool agentTool) bool {
	if !kit.HasPermission(r, tool.permission) {
		return false
	}
	return tool.feature == "" || !slices.Contains(h.featuresOff(r), tool.feature)
}

// needs is the route guard of an agent request type: the account must be
// allowed the tool (mayUse) before the route reads its body.
func (h *handlers) needs(typ string) kit.Middleware {
	tool, known := agentTools[typ]
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !known {
				answer(w, r, http.StatusForbidden, failure{Error: "agent_request_not_allowed"})
				return
			}
			if h.mayUse(w, r, tool) {
				next.ServeHTTP(w, r)
			}
		})
	}
}
