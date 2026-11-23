package ndcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// The MCP server: `neodesk mcp` serves the commands of the table as tools
// over standard input and output (JSON-RPC 2.0, one message per line), so
// an AI agent can use the console with the token the command line uses.
//
// What an agent gets:
//   - without --allow-writes only the commands that read (GET);
//   - with it every command but the uploads; a tool that deletes or ends
//     something is marked destructive and runs only with confirm: true;
//   - --groups narrows the tools to some groups; --compact offers two
//     tools instead (neodesk_commands to look commands up, neodesk_run to
//     run one by name), for clients that take few tools;
//   - no tool reads or writes local files: uploads are left out and a
//     download comes back as text, or as its size when it is binary.
// The server checks every call against the token: its permissions, its
// account's, and whether it is read-only.

const mcpProtocol = "2025-06-18"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// mcpTool is a tool as tools/list sends it.
type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

type mcpServer struct {
	table   *Table
	conn    *conn
	writes  bool
	compact bool
	groups  map[string]bool
	tools   map[string]*Command
	version string
}

// toolName is the tool of a command: "devices list" is devices_list.
func toolName(c *Command) string { return strings.ReplaceAll(c.Name, " ", "_") }

// offered reports whether the server offers the command as a tool.
func (s *mcpServer) offered(c *Command) bool {
	if c.Upload != "" {
		return false
	}
	if !s.writes && !c.ReadOnly() {
		return false
	}
	return len(s.groups) == 0 || s.groups[c.Group()]
}

func runMCP(t *Table, args []string, env *Env) int {
	writes, compact, groups := false, false, ""
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--allow-writes":
			writes = true
		case a == "--compact":
			compact = true
		case a == "--groups" && i+1 < len(args):
			groups = args[i+1]
			i++
		case strings.HasPrefix(a, "--groups="):
			groups = strings.TrimPrefix(a, "--groups=")
		default:
			rest = append(rest, a)
		}
	}
	o, ok, code := metaOptions(env, rest, "mcp")
	if !ok {
		return code
	}
	c, err := connect(o, env, true)
	if err != nil {
		return reportConnError(env, err)
	}
	s := &mcpServer{table: t, conn: c, writes: writes, compact: compact, groups: map[string]bool{}, tools: map[string]*Command{}, version: env.Version}
	for _, g := range strings.Split(groups, ",") {
		if g = strings.TrimSpace(g); g != "" {
			if !t.HasGroup(g) {
				fmt.Fprintf(env.Stderr, "neodesk: no command group %q\n", g)
				return ExitUsage
			}
			s.groups[g] = true
		}
	}
	for _, cmd := range t.Commands {
		if s.offered(cmd) {
			s.tools[toolName(cmd)] = cmd
		}
	}
	mode := "read only"
	if writes {
		mode = "reads and writes"
	}
	fmt.Fprintf(env.Stderr, "neodesk mcp: %d tools (%s) for %s\n", len(s.tools), mode, c.url)
	return s.serve(env.Stdin, env.Stdout)
}

// serve answers requests until standard input ends.
func (s *mcpServer) serve(in io.Reader, out io.Writer) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil || req.JSONRPC != "2.0" {
			enc.Encode(mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{-32700, "parse error"}})
			continue
		}
		result, rpcErr := s.handle(req)
		if len(req.ID) == 0 {
			continue // a notification gets no answer
		}
		resp := mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if rpcErr == nil && result == nil {
			resp.Result = map[string]any{}
		}
		enc.Encode(resp)
	}
	return ExitOK
}

func (s *mcpServer) handle(req mcpRequest) (any, *mcpError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := mcpProtocol
		if p.ProtocolVersion != "" {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "neodesk", "title": "NeoDesk", "version": s.version},
			"instructions":    s.instructions(),
		}, nil
	case "notifications/initialized", "notifications/cancelled", "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.list()}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &mcpError{-32602, "invalid params"}
		}
		return s.call(p.Name, p.Arguments), nil
	}
	return nil, &mcpError{-32601, "method not found: " + req.Method}
}

func (s *mcpServer) instructions() string {
	b := "Tools of the NeoDesk console, called with an API token: each tool is one command of the neodesk command line. " +
		"The server allows only what the token's permissions and its account allow."
	if !s.writes {
		b += " Only tools that read are offered."
	} else {
		b += " Tools marked destructive delete or end something for good: ask the person first, then call them with confirm: true."
	}
	if s.compact {
		b += " Look commands up with neodesk_commands, then run one with neodesk_run."
	}
	return b
}

// list is tools/list.
func (s *mcpServer) list() []mcpTool {
	if s.compact {
		return []mcpTool{
			{
				Name: "neodesk_commands", Title: "Find NeoDesk commands",
				Description: "List the commands this server runs, with their arguments and flags. Give a group to see its commands in full, or nothing for the groups.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{
					"group": map[string]any{"type": "string", "description": "a group, like devices"},
				}},
				Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			},
			{
				Name: "neodesk_run", Title: "Run a NeoDesk command",
				Description: "Run one command, like \"devices list\". args are its arguments in order, flags its flags by name.",
				InputSchema: map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "the group and action, like devices get"},
					"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"flags":   map[string]any{"type": "object", "description": "flag name to value"},
					"confirm": map[string]any{"type": "boolean", "description": "true for a command that deletes or ends something, after the person agreed"},
				}},
				Annotations: map[string]any{"readOnlyHint": !s.writes, "destructiveHint": s.writes, "idempotentHint": false, "openWorldHint": false},
			},
		}
	}
	names := make([]string, 0, len(s.tools))
	for n := range s.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]mcpTool, 0, len(names))
	for _, n := range names {
		out = append(out, toolOf(s.tools[n]))
	}
	return out
}

// toolOf describes a command as a tool: its arguments and flags as the
// input schema, and what it does to the server as annotations.
func toolOf(c *Command) mcpTool {
	props := map[string]any{}
	required := []string{}
	for _, a := range c.Args {
		props[a.Name] = map[string]any{"type": "string", "description": a.Help}
		required = append(required, a.Name)
	}
	for _, f := range c.Flags {
		p := map[string]any{"description": f.Help}
		switch f.Type {
		case "int":
			p["type"] = "integer"
		case "bool":
			p["type"] = "boolean"
		case "list":
			p["type"] = "array"
			p["items"] = map[string]any{"type": "string"}
		case "json":
		default:
			p["type"] = "string"
		}
		if len(f.Enum) > 0 {
			p["enum"] = f.Enum
		}
		props[f.Name] = p
		if f.Required {
			required = append(required, f.Name)
		}
	}
	props["query"] = map[string]any{"type": "object", "description": "more query parameters, name to text", "additionalProperties": map[string]any{"type": "string"}}
	if !c.ReadOnly() {
		props["body"] = map[string]any{"type": "object", "description": "more fields of the request body"}
	}
	if c.Destructive {
		props["confirm"] = map[string]any{"type": "boolean", "description": "must be true: this deletes or ends something for good; ask the person first"}
		required = append(required, "confirm")
	}
	desc := c.Summary
	if c.Description != "" {
		desc += " " + c.Description
	}
	perm := c.Permission
	if perm == "" {
		perm = "a signed-in account"
	}
	desc += " Needs " + perm + "."
	return mcpTool{
		Name:        toolName(c),
		Title:       c.Summary,
		Description: desc,
		InputSchema: map[string]any{"type": "object", "properties": props, "required": required},
		Annotations: map[string]any{
			"title":           c.Summary,
			"readOnlyHint":    c.ReadOnly(),
			"destructiveHint": c.Destructive,
			"idempotentHint":  c.ReadOnly() || c.Method() == "PUT" || c.Method() == "DELETE",
			"openWorldHint":   false,
		},
	}
}

// toolResult is a tools/call answer.
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

// call is tools/call.
func (s *mcpServer) call(name string, args map[string]any) map[string]any {
	if s.compact {
		switch name {
		case "neodesk_commands":
			return s.commands(args)
		case "neodesk_run":
			command, _ := args["command"].(string)
			words := strings.Fields(command)
			if len(words) != 2 {
				return toolResult("command must be a group and an action, like \"devices list\"", true)
			}
			cmd := s.table.Find(words[0], words[1])
			if cmd == nil || !s.offered(cmd) {
				return toolResult(fmt.Sprintf("no command %q is offered here; look it up with neodesk_commands", command), true)
			}
			named := map[string]any{"confirm": args["confirm"]}
			list, _ := args["args"].([]any)
			for i, a := range cmd.Args {
				if i < len(list) {
					named[a.Name] = fmt.Sprint(list[i])
				}
			}
			if flags, ok := args["flags"].(map[string]any); ok {
				for k, v := range flags {
					named[k] = v
				}
			}
			return s.run(cmd, named)
		}
		return toolResult("no tool "+name, true)
	}
	cmd, ok := s.tools[name]
	if !ok {
		return toolResult("no tool "+name+" is offered here", true)
	}
	return s.run(cmd, args)
}

// commands is neodesk_commands of the compact mode.
func (s *mcpServer) commands(args map[string]any) map[string]any {
	group, _ := args["group"].(string)
	var b strings.Builder
	if group == "" {
		for _, g := range s.table.Groups {
			n := 0
			for _, c := range s.table.InGroup(g.Name) {
				if s.offered(c) {
					n++
				}
			}
			if n > 0 {
				fmt.Fprintf(&b, "%s: %s (%d commands)\n", g.Name, g.Summary, n)
			}
		}
		return toolResult(b.String(), false)
	}
	if !s.table.HasGroup(group) {
		return toolResult("no group "+group, true)
	}
	for _, c := range s.table.InGroup(group) {
		if !s.offered(c) {
			continue
		}
		t := toolOf(c)
		schema, _ := json.Marshal(t.InputSchema["properties"])
		fmt.Fprintf(&b, "%s: %s\n  usage: %s\n  destructive: %v\n  arguments and flags: %s\n", c.Name, t.Description, usageLine(c), c.Destructive, schema)
	}
	return toolResult(b.String(), false)
}

// run calls a command with a tool's arguments.
func (s *mcpServer) run(cmd *Command, args map[string]any) map[string]any {
	if cmd.Destructive {
		if confirmed, _ := args["confirm"].(bool); !confirmed {
			return toolResult("This deletes or ends something for good. Ask the person first, then call again with confirm: true.", true)
		}
	}
	o := &options{flags: map[string][]string{}}
	for _, a := range cmd.Args {
		v, ok := args[a.Name]
		if !ok {
			return toolResult("missing argument "+a.Name, true)
		}
		o.positional = append(o.positional, fmt.Sprint(v))
	}
	for _, f := range cmd.Flags {
		v, ok := args[f.Name]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case []any:
			parts := make([]string, 0, len(x))
			for _, item := range x {
				parts = append(parts, fmt.Sprint(item))
			}
			o.flags[f.Name] = []string{strings.Join(parts, ",")}
		case map[string]any:
			raw, _ := json.Marshal(x)
			o.flags[f.Name] = []string{string(raw)}
		case float64:
			o.flags[f.Name] = []string{strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")}
		default:
			if f.Type == "json" {
				raw, _ := json.Marshal(x)
				o.flags[f.Name] = []string{string(raw)}
			} else {
				o.flags[f.Name] = []string{fmt.Sprint(x)}
			}
		}
	}
	if q, ok := args["query"].(map[string]any); ok {
		for k, v := range q {
			o.queries = append(o.queries, kv{k, fmt.Sprint(v)})
		}
	}
	if body, ok := args["body"].(map[string]any); ok && !cmd.ReadOnly() {
		for k, v := range body {
			raw, _ := json.Marshal(v)
			o.rawFields = append(o.rawFields, kv{k, string(raw)})
		}
	}
	req, err := buildRequest(cmd, o, strings.NewReader(""))
	if err != nil {
		return toolResult(err.Error(), true)
	}
	res, err := s.conn.do(context.Background(), req)
	if err != nil {
		return toolResult(err.Error(), true)
	}
	if !res.ok() {
		e := res.failure()
		text := fmt.Sprintf("HTTP %d: %s", e.status, e.msg)
		if e.code != "" {
			text += " (" + e.code + ")"
		}
		return toolResult(text, true)
	}
	return toolResult(answerText(res), false)
}

// answerText is an answer as text for the agent: JSON indented, other
// text as it is, a binary answer as its size.
func answerText(res *response) string {
	const limit = 200 << 10
	body := res.body
	if res.isJSON() {
		var v any
		if json.Unmarshal(body, &v) == nil {
			if b, err := json.MarshalIndent(v, "", "  "); err == nil {
				body = b
			}
		}
	} else if !utf8.Valid(body) {
		return fmt.Sprintf("The answer is a file (%s, %d bytes). Download it with the neodesk command line and --output.", res.contentType, len(body))
	}
	if len(body) > limit {
		return string(body[:limit]) + fmt.Sprintf("\n… cut at %d of %d bytes; narrow the request with a filter or limit", limit, len(body))
	}
	return string(body)
}
