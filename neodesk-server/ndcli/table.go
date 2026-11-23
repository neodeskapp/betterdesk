// Package ndcli is the command line of NeoDesk: `neodesk <group> <action>`
// runs one call of the console's API over HTTP(S) with an API token
// (package console/admintoken), and `neodesk local ...` runs the server
// binary's own commands on the server machine.
//
// The commands are data. commands.json maps each command to one console
// route: its arguments (the route's path parameters), its flags (query or
// body fields), the permission it needs, what to show of the answer, and
// whether it destroys something. The same table drives --help, shell
// completion, `neodesk describe` and its Markdown docs, and the MCP server
// (`neodesk mcp`). Its "excluded" list names every console route the
// command line leaves out, with the reason; the coverage tests fail when a
// route has neither a command nor a reason.
package ndcli

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed commands.json
var tableJSON []byte

// Table is the command table.
type Table struct {
	Groups   []Group     `json:"groups"`
	Commands []*Command  `json:"commands"`
	Excluded []Exclusion `json:"excluded"`
}

// Group is a group of commands with what it is about.
type Group struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// Command is one command: `neodesk <group> <action>`.
type Command struct {
	// Name is "<group> <action>".
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Description says more, in a few sentences; optional.
	Description string `json:"description,omitempty"`
	// Route is the console route: "GET /api/devices/{id}".
	Route string `json:"route"`
	// Args are the path parameters in their order, given as positional
	// arguments.
	Args  []Arg  `json:"args,omitempty"`
	Flags []Flag `json:"flags,omitempty"`
	// Permission is what the account and the token need, in words: a
	// permission name, "an admin role", or "" when a signed-in account is
	// enough (the panel section's level still applies).
	Permission string `json:"permission,omitempty"`
	// Rows is where the list to show as a table is in the answer
	// ("data.devices"); empty finds the first list of objects.
	Rows string `json:"rows,omitempty"`
	// Columns are the fields of a row to show; empty picks some.
	Columns []string `json:"columns,omitempty"`
	// Destructive marks a command that deletes or ends something for good;
	// it asks for confirmation unless --yes is given.
	Destructive bool `json:"destructive,omitempty"`
	// Upload is the form field a file goes in (--file); empty for JSON.
	Upload   string   `json:"upload,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Arg is a positional argument.
type Arg struct {
	Name string `json:"name"`
	Help string `json:"help"`
}

// Flag is a named option of a command.
type Flag struct {
	Name string `json:"name"`
	// In is "query" or "body".
	In string `json:"in"`
	// Type is string, int, bool, list (comma-separated texts) or json.
	Type     string   `json:"type"`
	Help     string   `json:"help,omitempty"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	// Field is the name the server takes, when it is not the flag's.
	Field string `json:"field,omitempty"`
	// Secret marks a value that should not be typed on the command line
	// (a password): --<name>-stdin reads it from standard input.
	Secret bool `json:"secret,omitempty"`
}

// Key is the flag's field name in the query or body.
func (f Flag) Key() string {
	if f.Field != "" {
		return f.Field
	}
	return f.Name
}

// Exclusion is a console route the command line leaves out, and why.
type Exclusion struct {
	Route  string `json:"route"`
	Reason string `json:"reason"`
}

// Group is the command's group.
func (c *Command) Group() string { g, _, _ := strings.Cut(c.Name, " "); return g }

// Action is the command's action.
func (c *Command) Action() string { _, a, _ := strings.Cut(c.Name, " "); return a }

// Method is the HTTP method of the command's route.
func (c *Command) Method() string { m, _, _ := strings.Cut(c.Route, " "); return m }

// Path is the path pattern of the command's route.
func (c *Command) Path() string { _, p, _ := strings.Cut(c.Route, " "); return p }

// ReadOnly reports whether the command only reads (GET).
func (c *Command) ReadOnly() bool { return c.Method() == "GET" }

var (
	tableOnce sync.Once
	loaded    *Table
	tableErr  error
)

// Load returns the command table built into the binary.
func Load() (*Table, error) {
	tableOnce.Do(func() {
		loaded, tableErr = Parse(tableJSON)
	})
	return loaded, tableErr
}

var (
	namePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]* [a-z][a-z0-9-]*$`)
	flagPattern  = regexp.MustCompile(`^[a-z][a-z0-9-_]*$`)
	routePattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|ALL) /\S*$`)
)

// reserved are the flag names every command has.
var reserved = map[string]bool{
	"url": true, "token": true, "token-file": true, "config": true, "json": true, "insecure": true,
	"ca-cert": true, "allow-http": true, "timeout": true, "verbose": true, "yes": true, "output": true,
	"help": true, "query": true, "field": true, "raw-field": true, "data": true, "file": true, "version": true,
}

// Parse reads and checks a command table.
func Parse(data []byte) (*Table, error) {
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("command table: %w", err)
	}
	groups := map[string]bool{}
	for _, g := range t.Groups {
		if groups[g.Name] {
			return nil, fmt.Errorf("command table: group %q is listed twice", g.Name)
		}
		groups[g.Name] = true
	}
	names := map[string]bool{}
	for _, c := range t.Commands {
		switch {
		case !namePattern.MatchString(c.Name):
			return nil, fmt.Errorf("command table: %q is no <group> <action> name", c.Name)
		case names[c.Name]:
			return nil, fmt.Errorf("command table: %q is listed twice", c.Name)
		case !groups[c.Group()]:
			return nil, fmt.Errorf("command table: %q has no group entry", c.Name)
		case !routePattern.MatchString(c.Route):
			return nil, fmt.Errorf("command table: %s: route %q", c.Name, c.Route)
		case strings.TrimSpace(c.Summary) == "":
			return nil, fmt.Errorf("command table: %s has no summary", c.Name)
		}
		names[c.Name] = true
		params := pathParams(c.Path())
		if len(params) != len(c.Args) {
			return nil, fmt.Errorf("command table: %s: %d args for %d path parameters", c.Name, len(c.Args), len(params))
		}
		seen := map[string]bool{}
		for _, f := range c.Flags {
			switch {
			case !flagPattern.MatchString(f.Name) || reserved[f.Name] || seen[f.Name]:
				return nil, fmt.Errorf("command table: %s: flag %q", c.Name, f.Name)
			case f.In != "query" && f.In != "body":
				return nil, fmt.Errorf("command table: %s: flag %q is in %q", c.Name, f.Name, f.In)
			}
			switch f.Type {
			case "string", "int", "bool", "list", "json":
			default:
				return nil, fmt.Errorf("command table: %s: flag %q has type %q", c.Name, f.Name, f.Type)
			}
			seen[f.Name] = true
		}
	}
	return &t, nil
}

var paramRe = regexp.MustCompile(`\{([^}]+)\}`)

// pathParams lists the names of a pattern's parameters.
func pathParams(pattern string) []string {
	var out []string
	for _, m := range paramRe.FindAllStringSubmatch(pattern, -1) {
		out = append(out, strings.TrimSuffix(m[1], "..."))
	}
	return out
}

// Find returns the command with the group and action, nil when there is
// none.
func (t *Table) Find(group, action string) *Command {
	for _, c := range t.Commands {
		if c.Name == group+" "+action {
			return c
		}
	}
	return nil
}

// HasGroup reports whether the group exists.
func (t *Table) HasGroup(name string) bool {
	for _, g := range t.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// InGroup lists the group's commands by action.
func (t *Table) InGroup(group string) []*Command {
	var out []*Command
	for _, c := range t.Commands {
		if c.Group() == group {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Action() < out[b].Action() })
	return out
}

// GroupSummary is the group's summary.
func (t *Table) GroupSummary(name string) string {
	for _, g := range t.Groups {
		if g.Name == name {
			return g.Summary
		}
	}
	return ""
}

// RouteKey is a route's method and path with every parameter written as
// {}: "GET /api/devices/{}". Express patterns (":id(\\d+)", "*") and Go
// patterns ("{id}", "{rest...}") of one route give the same key.
func RouteKey(route string) string {
	method, path, _ := strings.Cut(strings.TrimSpace(route), " ")
	path = regexp.MustCompile(`:[A-Za-z0-9_]+(\([^)]*\))?\??`).ReplaceAllString(path, "{}")
	path = paramRe.ReplaceAllString(path, "{}")
	path = strings.ReplaceAll(path, "*", "{}")
	path = strings.TrimSuffix(strings.ToLower(path), "/")
	if path == "" {
		path = "/"
	}
	return method + " " + path
}
