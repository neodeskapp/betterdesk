package ndcli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Description is the whole command line as data: what `neodesk describe`
// prints as JSON, and what the Markdown docs and the MCP tools are made
// from.
type Description struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Summary     string         `json:"summary"`
	Usage       string         `json:"usage"`
	Environment []NameHelp     `json:"environment"`
	GlobalFlags []NameHelp     `json:"global_flags"`
	ExitCodes   []ExitCode     `json:"exit_codes"`
	Meta        []NameHelp     `json:"meta_commands"`
	Groups      []GroupDesc    `json:"groups"`
	Excluded    []Exclusion    `json:"excluded"`
	Counts      map[string]int `json:"counts"`
}

// NameHelp is a name with what it does.
type NameHelp struct {
	Name string `json:"name"`
	Help string `json:"help"`
}

// ExitCode is an exit status and its meaning.
type ExitCode struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

// GroupDesc is a group with its commands.
type GroupDesc struct {
	Name     string        `json:"name"`
	Summary  string        `json:"summary"`
	Commands []CommandDesc `json:"commands"`
}

// CommandDesc is one command as data.
type CommandDesc struct {
	Name        string   `json:"name"`
	Action      string   `json:"action"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Usage       string   `json:"usage"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Args        []Arg    `json:"args"`
	Flags       []Flag   `json:"flags"`
	Permission  string   `json:"permission"`
	ReadOnly    bool     `json:"read_only"`
	Destructive bool     `json:"destructive"`
	Exec        bool     `json:"exec"`
	Secrets     bool     `json:"secrets"`
	AsksFirst   bool     `json:"asks_first"`
	Upload      string   `json:"upload,omitempty"`
	Examples    []string `json:"examples"`
}

var globalFlagHelp = []NameHelp{
	{"--url URL", "the console's address (or NEODESK_URL)"},
	{"--token-file FILE", "read the API token from a file (or NEODESK_TOKEN)"},
	{"--token TOKEN", "the API token; it shows in the process list, so prefer NEODESK_TOKEN or the config file"},
	{"--config FILE", "the config file (or NEODESK_CONFIG); default neodesk/config.json in the user's config folder"},
	{"--json", "print the server's answer as JSON"},
	{"-o, --output FILE", "save the answer to a file (downloads)"},
	{"-q key=value", "add a query parameter; repeat for more"},
	{"-f key=value", "add a text field to the request body"},
	{"-F key=json", "add a JSON value to the request body: a number, true or false, a list"},
	{"--data JSON", "the whole request body, or @file.json"},
	{"--file PATH", "the file to upload, for commands that take one"},
	{"-y, --yes", "do not ask before deleting or ending something"},
	{"--insecure", "do not check the console's certificate"},
	{"--ca-cert FILE", "trust this CA certificate for the console (or NEODESK_CA_CERT)"},
	{"--allow-http", "allow plain HTTP to another machine (or NEODESK_ALLOW_HTTP=1); the token is not encrypted then"},
	{"--timeout 60s", "how long to wait for an answer"},
	{"-v, --verbose", "print each request's method, address and status to standard error"},
	{"-h, --help", "help for the command"},
}

var environmentHelp = []NameHelp{
	{"NEODESK_URL", "the console's address"},
	{"NEODESK_TOKEN", "the API token"},
	{"NEODESK_CONFIG", "the config file"},
	{"NEODESK_CA_CERT", "a CA certificate to trust for the console"},
	{"NEODESK_ALLOW_HTTP", "1 allows plain HTTP to another machine"},
}

// Describe builds the description of the table.
func (t *Table) Describe(version string) Description {
	d := Description{
		Name:        "neodesk",
		Version:     version,
		Summary:     "The command line of NeoDesk: every action of the web console, on top of API tokens.",
		Usage:       "neodesk <group> <action> [arguments] [flags]",
		Environment: environmentHelp,
		GlobalFlags: globalFlagHelp,
		Excluded:    t.Excluded,
		Counts:      map[string]int{},
	}
	for _, e := range exitCodes {
		d.ExitCodes = append(d.ExitCodes, ExitCode{e.Code, e.Meaning})
	}
	for _, name := range metaNames() {
		d.Meta = append(d.Meta, NameHelp{name, metaCommands[name].summary})
	}
	for _, g := range t.Groups {
		gd := GroupDesc{Name: g.Name, Summary: g.Summary}
		for _, c := range t.InGroup(g.Name) {
			gd.Commands = append(gd.Commands, describeCommand(c))
		}
		d.Groups = append(d.Groups, gd)
		d.Counts["commands"] += len(gd.Commands)
	}
	d.Counts["groups"] = len(d.Groups)
	d.Counts["excluded"] = len(t.Excluded)
	return d
}

func describeCommand(c *Command) CommandDesc {
	cd := CommandDesc{
		Name: c.Name, Action: c.Action(), Summary: c.Summary, Description: c.Description,
		Usage: usageLine(c), Method: c.Method(), Path: c.Path(),
		Args: c.Args, Flags: c.Flags, Permission: c.Permission,
		ReadOnly: c.ReadOnly(), Destructive: c.Destructive, Exec: c.Exec, Secrets: c.Secrets, AsksFirst: c.AsksFirst(),
		Upload: c.Upload, Examples: c.Examples,
	}
	if cd.Args == nil {
		cd.Args = []Arg{}
	}
	if cd.Flags == nil {
		cd.Flags = []Flag{}
	}
	if cd.Examples == nil {
		cd.Examples = []string{}
	}
	if cd.Permission == "" {
		cd.Permission = "a signed-in account"
	}
	return cd
}

// runDescribe is `neodesk describe [--format json|markdown] [group]`.
func runDescribe(t *Table, args []string, env *Env) int {
	format, group := "json", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			writeMetaHelp(env.Stdout, "describe")
			return ExitOK
		case a == "--format" && i+1 < len(args):
			format = args[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case a == "--json":
			format = "json"
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(env.Stderr, "neodesk: describe takes --format json|markdown and a group, not %s\n", a)
			return ExitUsage
		default:
			group = a
		}
	}
	d := t.Describe(env.Version)
	if group != "" {
		if !t.HasGroup(group) {
			fmt.Fprintf(env.Stderr, "neodesk: no command group %q.%s\n", group, suggest(group, groupNames(t)))
			return ExitUsage
		}
		for _, g := range d.Groups {
			if g.Name == group {
				d.Groups = []GroupDesc{g}
			}
		}
	}
	switch format {
	case "json":
		b, _ := json.MarshalIndent(d, "", "  ")
		fmt.Fprintln(env.Stdout, string(b))
	case "markdown", "md":
		if group != "" {
			WriteGroupMarkdown(env.Stdout, d.Groups[0])
		} else {
			WriteMarkdown(env.Stdout, d)
		}
	default:
		fmt.Fprintf(env.Stderr, "neodesk: --format is json or markdown, not %q\n", format)
		return ExitUsage
	}
	return ExitOK
}

// md escapes text for a Markdown table cell.
func md(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

// WriteMarkdown writes the whole reference as one Markdown document.
func WriteMarkdown(w io.Writer, d Description) {
	WriteIndexMarkdown(w, d, "#")
	for _, g := range d.Groups {
		fmt.Fprintln(w)
		WriteGroupMarkdown(w, g)
	}
	fmt.Fprintln(w)
	WriteExcludedMarkdown(w, d.Excluded)
}

// WriteIndexMarkdown writes the overview: usage, sign-in, the groups,
// the flags every command has, the environment and the exit codes. link
// is "#" for one document (links to the group's heading) or "" for a
// page per group (<group>.md).
func WriteIndexMarkdown(w io.Writer, d Description, link string) {
	fmt.Fprintf(w, "# The neodesk command line\n\n%s\n\n", d.Summary)
	fmt.Fprintf(w, "```sh\n%s\nneodesk <group> --help\nneodesk login --url https://desk.example.com\n```\n\n", d.Usage)
	fmt.Fprintln(w, "The command line is the server binary: called `neodesk` (the Docker images have that name), or `neodesk-server cli`. "+
		"Sign in once with an API token. Make one in the console: account menu, API tokens. "+
		"A token can do only what its permissions and its account allow, and a read-only token only reads.")
	fmt.Fprint(w, tokenRulesMarkdown)
	fmt.Fprintf(w, "\n## Groups\n\n| Group | What it is for | Commands |\n|---|---|---|\n")
	for _, g := range d.Groups {
		target := g.Name + ".md"
		if link == "#" {
			target = "#" + g.Name
		}
		fmt.Fprintf(w, "| [%s](%s) | %s | %d |\n", g.Name, target, md(g.Summary), len(g.Commands))
	}
	fmt.Fprintf(w, "\n## Commands of the command line itself\n\n| Command | What it does |\n|---|---|\n")
	for _, m := range d.Meta {
		fmt.Fprintf(w, "| `neodesk %s` | %s |\n", m.Name, md(m.Help))
	}
	fmt.Fprintf(w, "\n## Flags for every command\n\n| Flag | What it does |\n|---|---|\n")
	for _, f := range d.GlobalFlags {
		fmt.Fprintf(w, "| `%s` | %s |\n", f.Name, md(f.Help))
	}
	fmt.Fprintf(w, "\n## Environment\n\n| Variable | What it is |\n|---|---|\n")
	for _, e := range d.Environment {
		fmt.Fprintf(w, "| `%s` | %s |\n", e.Name, md(e.Help))
	}
	fmt.Fprintf(w, "\n## Exit codes\n\n| Code | Meaning |\n|---|---|\n")
	for _, e := range d.ExitCodes {
		fmt.Fprintf(w, "| %d | %s |\n", e.Code, md(e.Meaning))
	}
}

// tokenRulesMarkdown is what the docs say about API tokens (the rules are
// in package console/admintoken and console/apitokens.go).
const tokenRulesMarkdown = `
## API tokens

Make a token in the console: account menu, API tokens. The console shows it once. A token:

- holds at most the permissions of the account that made it, now and later: when the account loses a permission or is disabled, the token loses it too
- can be read only, expires after at most a year, and can be limited to some addresses or networks
- never signs in, changes its own account or shows the console's API key
- passes checks by role, changes accounts and access, and keeps server.config only when it carries every permission of its account
- reaches the server's secrets (the full backup and restore, the server's files, the advanced settings files, where scheduled backups go) only when it carries every permission of its account and may write

The address list checks the address the console sees. Behind a reverse proxy, set TRUST_PROXY to the proxy's address rather than true, and keep the console's port closed to everyone else. Otherwise a client can name any address in X-Forwarded-For.

For an AI agent, ` + "`neodesk mcp`" + ` serves the commands as MCP tools: read only, without the commands that show secrets or run something, unless you allow them. Give the agent a token of its own with only the permissions it needs.
`

// WriteGroupMarkdown writes one group with every command.
func WriteGroupMarkdown(w io.Writer, g GroupDesc) {
	fmt.Fprintf(w, "## %s\n\n%s.\n", g.Name, strings.TrimSuffix(g.Summary, "."))
	for _, c := range g.Commands {
		fmt.Fprintf(w, "\n### neodesk %s\n\n%s\n", c.Name, c.Summary)
		if c.Description != "" {
			fmt.Fprintf(w, "\n%s\n", c.Description)
		}
		fmt.Fprintf(w, "\n```sh\n%s\n```\n", c.Usage)
		if len(c.Args) > 0 {
			fmt.Fprintf(w, "\n| Argument | What it is |\n|---|---|\n")
			for _, a := range c.Args {
				fmt.Fprintf(w, "| `<%s>` | %s |\n", a.Name, md(a.Help))
			}
		}
		if len(c.Flags) > 0 || c.Upload != "" {
			fmt.Fprintf(w, "\n| Flag | What it is |\n|---|---|\n")
			for _, f := range c.Flags {
				help := f.Help
				if f.Required {
					help += " (required)"
				}
				if f.Secret {
					help += "; --" + f.Name + "-stdin reads it from standard input"
				}
				fmt.Fprintf(w, "| `--%s` | %s |\n", strings.TrimSpace(f.Name+" "+flagPlaceholder(f)), md(help))
			}
			if c.Upload != "" {
				fmt.Fprintf(w, "| `--file PATH` | the file to upload |\n")
			}
		}
		access := "changes something: needs a token with read and write access"
		if c.ReadOnly {
			access = "only reads"
		}
		fmt.Fprintf(w, "\nNeeds %s; %s", c.Permission, access)
		if c.Destructive {
			fmt.Fprint(w, "; deletes or ends something for good, so it asks first unless --yes is given")
		}
		if c.Exec {
			fmt.Fprint(w, "; runs something on a device or the server")
		}
		if c.Secrets {
			fmt.Fprint(w, "; the answer holds secrets or private content")
		}
		fmt.Fprintf(w, ". Calls `%s %s`.\n", c.Method, c.Path)
		if len(c.Examples) > 0 {
			fmt.Fprintf(w, "\n```sh\n%s\n```\n", strings.Join(c.Examples, "\n"))
		}
	}
}

// WriteExcludedMarkdown lists the routes the command line leaves out.
func WriteExcludedMarkdown(w io.Writer, excluded []Exclusion) {
	byReason := map[string][]string{}
	for _, e := range excluded {
		byReason[e.Reason] = append(byReason[e.Reason], e.Route)
	}
	reasons := make([]string, 0, len(byReason))
	for r := range byReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	fmt.Fprintf(w, "## Routes the command line leaves out\n\nEvery route of the console has a command, or is here with the reason. "+
		"`neodesk api` still sends any request.\n")
	for _, r := range reasons {
		fmt.Fprintf(w, "\n%s\n\n", r)
		for _, route := range byReason[r] {
			fmt.Fprintf(w, "- `%s`\n", route)
		}
	}
}
