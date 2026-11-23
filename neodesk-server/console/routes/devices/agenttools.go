package devices

import (
	"net/http"

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
// them (L388).
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

// mayUse reports whether the request's account may use the tool. When it
// may not, the refusal has been sent, as RequirePermission sends it.
func (h *handlers) mayUse(w http.ResponseWriter, r *http.Request, tool agentTool) bool {
	if decision := kit.Access(r).RequirePermission(tool.permission, httpx.OriginalURL(r)); !decision.Passed() {
		h.d.Refuse(w, r, decision)
		return false
	}
	return true
}

// allowed is mayUse without an answer.
func (h *handlers) allowed(r *http.Request, tool agentTool) bool {
	return kit.HasPermission(r, tool.permission)
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
