package ndcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// mcpSession runs `neodesk mcp` with the requests given and returns the
// answers by id.
func mcpSession(t *testing.T, env map[string]string, flags []string, requests ...string) (map[string]map[string]any, string) {
	t.Helper()
	in := strings.Join(requests, "\n") + "\n"
	code, out, stderr := runCLI(t, env, in, append([]string{"mcp"}, flags...)...)
	if code != 0 {
		t.Fatalf("mcp: %d %s", code, stderr)
	}
	answers := map[string]map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var a map[string]any
		if err := json.Unmarshal(sc.Bytes(), &a); err != nil {
			t.Fatalf("not JSON on stdout: %q", sc.Text())
		}
		answers[fmt.Sprint(a["id"])] = a
	}
	return answers, out
}

func call(id int, tool string, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

func tools(a map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	result, _ := a["result"].(map[string]any)
	list, _ := result["tools"].([]any)
	for _, item := range list {
		tool, _ := item.(map[string]any)
		out[tool["name"].(string)] = tool
	}
	return out
}

func resultText(a map[string]any) (string, bool) {
	result, _ := a["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return "", false
	}
	first, _ := content[0].(map[string]any)
	isError, _ := result["isError"].(bool)
	return first["text"].(string), isError
}

const initialize = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`

// Read only by default: only the commands that read are tools, but not
// those whose answer holds secrets; no tool uploads, and each says what
// it needs.
func TestMCPReadsOnlyByDefault(t *testing.T) {
	tab, _ := Load()
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	answers, out := mcpSession(t, env, nil, initialize, `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":2,"method":"nope"}`)
	if len(answers) != 3 {
		t.Errorf("%d answers (a notification gets none): %s", len(answers), out)
	}
	if r, _ := answers["0"]["result"].(map[string]any); r["protocolVersion"] != "2025-06-18" {
		t.Errorf("initialize: %v", answers["0"])
	}
	if e, _ := answers["2"]["error"].(map[string]any); e["code"] != float64(-32601) {
		t.Errorf("unknown method: %v", answers["2"])
	}
	list := tools(answers["1"])
	reads := 0
	for _, c := range tab.Commands {
		tool, offered := list[toolName(c)]
		if c.ReadOnly() && c.Upload == "" && !c.Secrets {
			reads++
			if !offered {
				t.Errorf("%s is not offered", c.Name)
				continue
			}
			ann, _ := tool["annotations"].(map[string]any)
			if ann["readOnlyHint"] != true || !strings.Contains(tool["description"].(string), "Needs ") {
				t.Errorf("%s: %v", c.Name, tool)
			}
		} else if offered {
			t.Errorf("%s writes or shows secrets but is offered without --allow-writes or --allow-secrets", c.Name)
		}
	}
	if len(list) != reads {
		t.Errorf("%d tools, %d commands that read", len(list), reads)
	}
	if strings.Contains(out, testToken[17:]) {
		t.Error("the token is in the output")
	}
}

func TestMCPWritesNeedTheFlagAndConfirmation(t *testing.T) {
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		if r.URL.Path == "/api/devices/x1" && r.Method == "GET" {
			return 200, `{"success":true,"data":{"id":"x1","hostname":"front"}}`
		}
		if r.URL.Path == "/api/devices/missing" {
			return 404, `{"success":false,"error":"Device not found"}`
		}
		return 200, `{"success":true}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	answers, _ := mcpSession(t, env, []string{"--allow-writes"}, initialize,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		call(2, "devices_get", `{"id":"x1"}`),
		call(3, "devices_delete", `{"id":"x1"}`),
		call(4, "devices_delete", `{"id":"x1","confirm":true}`),
		call(5, "devices_get", `{"id":"missing"}`),
		call(6, "devices_update", `{"id":"x1","note":"Front desk","body":{"pinned":true}}`),
		call(7, "backups_restore", `{"confirm":true}`),
	)
	list := tools(answers["1"])
	del := list["devices_delete"]
	ann, _ := del["annotations"].(map[string]any)
	schema, _ := del["inputSchema"].(map[string]any)
	if ann["destructiveHint"] != true || ann["readOnlyHint"] != false || !strings.Contains(fmt.Sprint(schema["required"]), "confirm") {
		t.Errorf("devices_delete: %v", del)
	}
	if _, ok := list["backups_restore"]; ok {
		t.Error("an upload is offered: it would read local files")
	}
	if text, isErr := resultText(answers["2"]); isErr || !strings.Contains(text, `"hostname": "front"`) {
		t.Errorf("devices_get: %q %v", text, isErr)
	}
	if text, isErr := resultText(answers["3"]); !isErr || !strings.Contains(text, "confirm: true") {
		t.Errorf("delete without confirm: %q", text)
	}
	if text, isErr := resultText(answers["5"]); !isErr || !strings.Contains(text, "HTTP 404") {
		t.Errorf("not found: %q", text)
	}
	if text, isErr := resultText(answers["7"]); !isErr || !strings.Contains(text, "no tool") {
		t.Errorf("upload tool: %q", text)
	}
	// What reached the console: the get, one delete (the confirmed one), the get of the missing device, the update
	var seen []string
	for i, r := range f.reqs {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == "PATCH" && (!strings.Contains(f.body[i], `"note":"Front desk"`) || !strings.Contains(f.body[i], `"pinned":true`)) {
			t.Errorf("update body %s", f.body[i])
		}
	}
	if strings.Join(seen, ",") != "GET /api/devices/x1,DELETE /api/devices/x1,GET /api/devices/missing,PATCH /api/devices/x1" {
		t.Errorf("requests %v", seen)
	}
}

func TestMCPGroupsAndCompactMode(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	answers, _ := mcpSession(t, env, []string{"--groups", "users,audit"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for name := range tools(answers["1"]) {
		if !strings.HasPrefix(name, "users_") && !strings.HasPrefix(name, "audit_") {
			t.Errorf("--groups users,audit offers %s", name)
		}
	}
	answers, _ = mcpSession(t, env, []string{"--compact"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		call(2, "neodesk_commands", `{}`), call(3, "neodesk_commands", `{"group":"devices"}`),
		call(4, "neodesk_run", `{"command":"devices list","flags":{"status":"online"}}`),
		call(5, "neodesk_run", `{"command":"devices delete","args":["x1"],"confirm":true}`))
	if list := tools(answers["1"]); len(list) != 2 {
		t.Errorf("compact tools: %v", list)
	}
	if text, _ := resultText(answers["2"]); !strings.Contains(text, "devices:") {
		t.Errorf("groups: %q", text)
	}
	if text, _ := resultText(answers["3"]); !strings.Contains(text, "devices list:") || strings.Contains(text, "devices delete:") {
		t.Errorf("devices commands (read only): %q", text)
	}
	if text, isErr := resultText(answers["5"]); !isErr || !strings.Contains(text, "no command") {
		t.Errorf("a write in read-only compact mode: %q", text)
	}
	if len(f.reqs) != 1 || f.reqs[0].URL.Query().Get("status") != "online" {
		t.Errorf("requests: %d", len(f.reqs))
	}
}

func TestMCPNeedsAToken(t *testing.T) {
	if code, _, stderr := runCLI(t, map[string]string{"NEODESK_URL": "https://desk.example.com"}, "", "mcp"); code != ExitAuth || !strings.Contains(stderr, "no API token") {
		t.Errorf("mcp without a token: %d %s", code, stderr)
	}
	if code, _, _ := runCLI(t, map[string]string{"NEODESK_URL": "https://desk.example.com", "NEODESK_TOKEN": testToken}, "", "mcp", "--groups", "nope"); code != ExitUsage {
		t.Errorf("unknown group: %d", code)
	}
}

// Commands that run something on a device or the server need
// --allow-exec on top of --allow-writes, those whose answer holds secrets
// --allow-secrets; and everything that runs something or changes who may
// do what needs confirm: true like a delete.
func TestMCPExecAndSecretsNeedTheirOwnFlags(t *testing.T) {
	tab, _ := Load()
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	list := func(flags ...string) map[string]map[string]any {
		answers, _ := mcpSession(t, env, flags, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		return tools(answers["1"])
	}
	writes, exec, secrets := list("--allow-writes"), list("--allow-writes", "--allow-exec"), list("--allow-secrets")
	execs, secretCmds := 0, 0
	for _, c := range tab.Commands {
		name := toolName(c)
		if c.Exec {
			execs++
			if _, ok := writes[name]; ok {
				t.Errorf("%s runs something but is offered without --allow-exec", name)
			}
			tool, ok := exec[name]
			if !ok && !c.Secrets && c.Upload == "" {
				t.Errorf("%s is not offered with --allow-exec", name)
			}
			if ok {
				ann, _ := tool["annotations"].(map[string]any)
				schema, _ := tool["inputSchema"].(map[string]any)
				if ann["destructiveHint"] != true || !strings.Contains(fmt.Sprint(schema["required"]), "confirm") {
					t.Errorf("%s: not marked to ask first: %v", name, tool)
				}
			}
		}
		if c.Secrets {
			secretCmds++
			if _, ok := writes[name]; ok {
				t.Errorf("%s shows secrets but is offered without --allow-secrets", name)
			}
			if _, ok := secrets[name]; c.ReadOnly() && c.Upload == "" && !ok {
				t.Errorf("%s is not offered with --allow-secrets", name)
			}
		}
	}
	for _, want := range []string{"device-control run", "mesh exec", "mesh terminal", "server exec", "settings file-set", "fleet workflow-run"} {
		if c := tab.Find(strings.Fields(want)[0], strings.Fields(want)[1]); c == nil || !c.Exec {
			t.Errorf("%s is not marked exec", want)
		}
	}
	for _, want := range []string{"devices connect-password", "remote-targets credentials", "settings file", "server file", "backups download", "device-control clipboard"} {
		if c := tab.Find(strings.Fields(want)[0], strings.Fields(want)[1]); c == nil || !c.Secrets {
			t.Errorf("%s is not marked secrets", want)
		}
	}
	if execs < 20 || secretCmds < 15 {
		t.Errorf("%d exec and %d secret commands", execs, secretCmds)
	}
	if code, _, stderr := runCLI(t, env, "", "mcp", "--allow-exec"); code != ExitUsage || !strings.Contains(stderr, "--allow-writes") {
		t.Errorf("--allow-exec alone: %d %s", code, stderr)
	}
	// Changing who may do what asks first; an ordinary write does not
	answers, _ := mcpSession(t, env, []string{"--allow-writes", "--allow-exec"},
		call(1, "users_create", `{"username":"eve","role":"admin","password":"x"}`),
		call(2, "device-control_run", `{"id":"x1","command":"whoami"}`),
		call(3, "devices_rename", `{"id":"x1","name":"front"}`),
		call(4, "users_create", `{"username":"eve","role":"admin","password":"x","confirm":true}`))
	for _, id := range []string{"1", "2"} {
		if text, isErr := resultText(answers[id]); !isErr || !strings.Contains(text, "confirm: true") {
			t.Errorf("call %s without confirm: %q", id, text)
		}
	}
	var seen []string
	for _, r := range f.reqs {
		seen = append(seen, r.Method+" "+r.URL.Path)
	}
	if strings.Join(seen, ",") != "POST /api/devices/x1/rename,POST /api/users" {
		t.Errorf("requests %v", seen)
	}
}

// A client that can ask the person (the MCP elicitation capability) is
// asked before a tool that deletes, ends, runs or changes who may do what:
// the agent's own confirm: true no longer suffices, and a decline sends
// nothing. A client without it keeps the confirm argument (L431).
func TestMCPAsksThePersonWhenTheClientCan(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	const askingClient = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{}},"clientInfo":{"name":"test","version":"1"}}}`
	answers, out := mcpSession(t, env, []string{"--allow-writes"}, askingClient,
		call(1, "devices_delete", `{"id":"x1","confirm":true}`),
		`{"jsonrpc":"2.0","id":"elicit-1","result":{"action":"decline"}}`,
		call(2, "devices_delete", `{"id":"x2"}`),
		`{"jsonrpc":"2.0","id":"elicit-2","result":{"action":"accept","content":{"confirm":true}}}`,
		call(3, "devices_get", `{"id":"x3"}`),
	)
	ask, _ := answers["elicit-1"]
	params, _ := ask["params"].(map[string]any)
	if ask["method"] != "elicitation/create" || !strings.Contains(fmt.Sprint(params["message"]), "devices delete") {
		t.Fatalf("no question to the person before the delete:\n%s", out)
	}
	if text, isErr := resultText(answers["1"]); !isErr || !strings.Contains(text, "declined") {
		t.Errorf("a declined delete: %q %v", text, isErr)
	}
	if _, isErr := resultText(answers["2"]); isErr {
		t.Errorf("an accepted delete: %v", answers["2"])
	}
	if _, asked := answers["elicit-3"]; asked {
		t.Error("a read asked the person")
	}
	var seen []string
	for _, r := range f.reqs {
		seen = append(seen, r.Method+" "+r.URL.Path)
	}
	if strings.Join(seen, ",") != "DELETE /api/devices/x2,GET /api/devices/x3" {
		t.Errorf("requests %v", seen)
	}
}
