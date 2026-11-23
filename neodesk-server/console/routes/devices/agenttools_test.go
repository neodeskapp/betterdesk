package devices_test

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// agentTool is what one of the NeoDesk agent's console tools needs, in the
// words of the Permissions page and of Panel access: the permission, and
// the client feature that turns the tool off ("" for none).
type agentTool struct{ permission, feature string }

// wantAgentTools is every route that reaches a NeoDesk agent, with what
// it needs. Reading lists needs View devices; the terminal and the files
// have permissions of their own, as on MeshCentral agents; the screen,
// the input and the clipboard are remote control and need Connect to
// devices. Each client feature switch covers every route of its feature.
var wantAgentTools = map[string]agentTool{
	"GET /api/devices/{id}/services":          {"device.view", ""},
	"GET /api/devices/{id}/processes":         {"device.view", ""},
	"GET /api/devices/{id}/events":            {"device.view", ""},
	"GET /api/devices/{id}/activity":          {"device.view", ""},
	"POST /api/devices/{id}/files/browse":     {"agent.files", "file_transfer"},
	"POST /api/devices/{id}/files/read":       {"agent.files", "file_transfer"},
	"POST /api/devices/{id}/files/write":      {"agent.files", "file_transfer"},
	"POST /api/devices/{id}/files/delete":     {"agent.files", "file_transfer"},
	"POST /api/devices/{id}/files/rename":     {"agent.files", "file_transfer"},
	"POST /api/devices/{id}/files/mkdir":      {"agent.files", "file_transfer"},
	"GET /api/devices/{id}/clipboard":         {"device.connect", "clipboard"},
	"POST /api/devices/{id}/clipboard":        {"device.connect", "clipboard"},
	"POST /api/devices/{id}/screenshot":       {"device.connect", "remote_control"},
	"POST /api/devices/{id}/input/mouse":      {"device.connect", "remote_control"},
	"POST /api/devices/{id}/input/key":        {"device.connect", "remote_control"},
	"POST /api/devices/{id}/input/text":       {"device.connect", "remote_control"},
	"POST /api/devices/{id}/terminal/execute": {"agent.terminal", "terminal"},
	// A refresh queues collect.hardware
	"POST /api/devices/{id}/telemetry/refresh": {"device.edit", ""},
}

// wantTelemetryCommands is what each command the console queues for a
// device needs: a collection is a refresh, the files and the control of
// services and processes are the agent's tools.
var wantTelemetryCommands = map[string]agentTool{
	"collect.hardware":  {"device.edit", ""},
	"collect.metrics":   {"device.edit", ""},
	"collect.services":  {"device.edit", ""},
	"collect.processes": {"device.edit", ""},
	"collect.events":    {"device.edit", ""},
	"collect.activity":  {"device.edit", ""},
	"files.browse":      {"agent.files", "file_transfer"},
	"files.read":        {"agent.files", "file_transfer"},
	"service.control":   {"device.connect", "remote_control"},
	"process.terminate": {"device.connect", "remote_control"},
}

// agentBody fills every field an agent route reads, so each gets as far
// as the agent.
const agentBody = `{"path":"/tmp/x","data":"x","from":"/tmp/a","to":"/tmp/b","command":"whoami","text":"hi","key":"a","action":"press","x":1,"y":1}`

// TestEveryAgentRouteNamesItsPermission walks the console's route table:
// every route that reaches a NeoDesk agent (the agent is asked, or a
// command is queued for it) must be one of wantAgentTools, and no account
// gets there without the permission and the client feature that table
// names. global_admin, which holds them all, loses one permission or one
// feature at a time (L388, L389).
func TestEveryAgentRouteNamesItsPermission(t *testing.T) {
	roles := routetest.Overrides{"global_admin": {}}
	w, _ := world(t, storetest.Migrated(t), roles, nil)
	d := w.Server.Deps()
	ctx := context.Background()
	const caller = "global_admin"
	callerID := routetest.RoleIDs[caller]

	browser := w.As(caller)
	serve := func(method, path, body string) routetest.Answer {
		t.Helper()
		return browser.Do(method, path, body)
	}
	// reached says whether the request got to the agent: it was asked (the
	// agent is offline here) or a command was queued for it
	reached := func(got routetest.Answer) bool {
		return got.Status == http.StatusServiceUnavailable && strings.Contains(got.Body, "agent_offline") ||
			got.Status == http.StatusAccepted
	}
	var routes []string
	for _, rt := range w.Server.Router().Routes() {
		if strings.HasPrefix(rt.Path, "/api/devices/{id}/") && rt.Method != "" && !strings.HasSuffix(rt.Path, "/telemetry/command") {
			routes = append(routes, rt.Method+" "+rt.Path)
		}
	}
	sort.Strings(routes)
	walk := func(what string, lacks func(agentTool) bool) {
		t.Helper()
		for _, route := range routes {
			method, path, _ := strings.Cut(route, " ")
			body := ""
			if method != http.MethodGet {
				body = agentBody
			}
			got := serve(method, strings.Replace(path, "{id}", "A", 1), body)
			if !reached(got) {
				continue
			}
			tool, listed := wantAgentTools[route]
			if !listed {
				t.Errorf("%s reaches the agent but is no agent tool: %d %s", route, got.Status, got.Body)
				continue
			}
			if lacks(tool) {
				t.Errorf("%s reaches the agent %s: %d %s", route, what, got.Status, got.Body)
			}
		}
		for command, tool := range wantTelemetryCommands {
			got := serve(http.MethodPost, "/api/devices/A/telemetry/command", `{"command":"`+command+`"}`)
			if got.Status == http.StatusAccepted && lacks(tool) {
				t.Errorf("telemetry command %s is queued %s: %d %s", command, what, got.Status, got.Body)
			}
		}
	}

	// Everything held: every tool reaches the agent, so the walk is no empty one
	for route := range wantAgentTools {
		method, path, _ := strings.Cut(route, " ")
		body := ""
		if method != http.MethodGet {
			body = agentBody
		}
		if got := serve(method, strings.Replace(path, "{id}", "A", 1), body); !reached(got) {
			t.Errorf("%s with every permission: %d %s, want the agent asked", route, got.Status, got.Body)
		}
	}
	walk("with every permission", func(agentTool) bool { return false })

	for _, permission := range []string{"device.view", "device.edit", "device.connect", "agent.terminal", "agent.files"} {
		roles[caller][permission] = false
		d.Access.ClearRoleCache()
		walk("without "+permission, func(tool agentTool) bool { return tool.permission == permission })
		delete(roles[caller], permission)
		d.Access.ClearRoleCache()
	}

	user := strconv.FormatInt(callerID, 10)
	for _, feature := range []string{"terminal", "file_transfer", "remote_control", "clipboard"} {
		if _, err := d.Access.SavePolicy(ctx, access.SubjectUser, user, []any{}, []any{map[string]any{"feature_key": feature, "enabled": false}}); err != nil {
			t.Fatal(err)
		}
		walk("with "+feature+" turned off", func(tool agentTool) bool { return tool.feature == feature })
		if _, err := d.Access.SavePolicy(ctx, access.SubjectUser, user, []any{}, []any{}); err != nil {
			t.Fatal(err)
		}
	}
}
