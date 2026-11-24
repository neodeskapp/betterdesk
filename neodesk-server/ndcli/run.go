package ndcli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// kv is a key=value given with -q, -f or -F.
type kv struct{ key, value string }

// options are the parsed command line.
type options struct {
	url, token, tokenFile, config, output, caCert, data, file string
	json, insecure, allowHTTP, verbose, yes, help, version    bool
	timeout                                                   time.Duration
	queries, fields, rawFields                                []kv
	// flags are the command's own flags as given: name to values.
	flags map[string][]string
	// fromStdin are the secret flags to read from standard input
	// (--password-stdin).
	fromStdin  []string
	positional []string
}

// globalValue are the flags every command has that take a value, with
// their short forms.
var globalValue = map[string]string{
	"url": "url", "token": "token", "token-file": "token-file", "config": "config", "ca-cert": "ca-cert",
	"timeout": "timeout", "output": "output", "o": "output", "query": "query", "q": "query",
	"field": "field", "f": "field", "raw-field": "raw-field", "F": "raw-field", "data": "data", "file": "file",
}

// globalBool are the flags every command has that are switches.
var globalBool = map[string]string{
	"json": "json", "insecure": "insecure", "allow-http": "allow-http", "verbose": "verbose", "v": "verbose",
	"yes": "yes", "y": "yes", "help": "help", "h": "help", "version": "version",
}

// GlobalFlags are the options every command takes, as they are written:
// --name, or -n for a short form.
func GlobalFlags() []string {
	var out []string
	for _, names := range []map[string]string{globalValue, globalBool} {
		for name := range names {
			if len(name) == 1 {
				out = append(out, "-"+name)
			} else {
				out = append(out, "--"+name)
			}
		}
	}
	return out
}

// usageError is a mistake in the command line: exit 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

// splitFlag splits "--name=value" or "-n" into name and value.
func splitFlag(arg string) (name, value string, hasValue bool) {
	name = strings.TrimLeft(arg, "-")
	name, value, hasValue = strings.Cut(name, "=")
	return name, value, hasValue
}

// commandWords finds the words that name the command (up to two) and
// returns them with the other arguments in order. Flags every command
// has may come anywhere; the command's own flags come after its words.
func commandWords(args []string) (words []string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name, _, has := splitFlag(arg)
			if _, ok := globalBool[name]; ok {
				rest = append(rest, arg)
				continue
			}
			if _, ok := globalValue[name]; ok {
				rest = append(rest, arg)
				if !has && i+1 < len(args) {
					i++
					rest = append(rest, args[i])
				}
				continue
			}
			if len(words) < 2 {
				// A command flag before the command's words: the words
				// can still follow, but only for a known group
				rest = append(rest, args[i:]...)
				break
			}
			rest = append(rest, arg)
			continue
		}
		if len(words) < 2 {
			words = append(words, arg)
			continue
		}
		rest = append(rest, arg)
	}
	return words, rest, nil
}

// parseOptions parses the arguments after the command's words, with the
// command's flags (nil for a command without any).
func parseOptions(args []string, cmd *Command) (*options, error) {
	o := &options{flags: map[string][]string{}, timeout: 60 * time.Second}
	specs := map[string]Flag{}
	if cmd != nil {
		for _, f := range cmd.Flags {
			specs[f.Name] = f
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.positional = append(o.positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			o.positional = append(o.positional, arg)
			continue
		}
		name, value, has := splitFlag(arg)
		next := func() (string, error) {
			if has {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", usagef("--%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		if long, ok := globalBool[name]; ok {
			on := true
			if has {
				switch strings.ToLower(value) {
				case "true", "1", "yes":
				case "false", "0", "no":
					on = false
				default:
					return nil, usagef("--%s takes true or false", name)
				}
			}
			switch long {
			case "json":
				o.json = on
			case "insecure":
				o.insecure = on
			case "allow-http":
				o.allowHTTP = on
			case "verbose":
				o.verbose = on
			case "yes":
				o.yes = on
			case "help":
				o.help = on
			case "version":
				o.version = on
			}
			continue
		}
		if long, ok := globalValue[name]; ok {
			v, err := next()
			if err != nil {
				return nil, err
			}
			switch long {
			case "url":
				o.url = v
			case "token":
				o.token = v
			case "token-file":
				o.tokenFile = v
			case "config":
				o.config = v
			case "ca-cert":
				o.caCert = v
			case "output":
				o.output = v
			case "data":
				o.data = v
			case "file":
				o.file = v
			case "timeout":
				d, err := time.ParseDuration(v)
				if err != nil || d <= 0 {
					return nil, usagef("--timeout takes a duration like 30s or 2m")
				}
				o.timeout = d
			case "query", "field", "raw-field":
				k, val, ok := strings.Cut(v, "=")
				if !ok || k == "" {
					return nil, usagef("--%s takes key=value", long)
				}
				switch long {
				case "query":
					o.queries = append(o.queries, kv{k, val})
				case "field":
					o.fields = append(o.fields, kv{k, val})
				default:
					o.rawFields = append(o.rawFields, kv{k, val})
				}
			}
			continue
		}
		if base, isStdin := strings.CutSuffix(name, "-stdin"); isStdin && specs[base].Secret && !has {
			o.fromStdin = append(o.fromStdin, base)
			continue
		}
		spec, ok := specs[name]
		if !ok {
			if cmd == nil {
				return nil, usagef("unknown flag --%s", name)
			}
			return nil, usagef("unknown flag --%s for %q; see: neodesk %s --help", name, cmd.Name, cmd.Name)
		}
		if spec.Type == "bool" && !has {
			o.flags[name] = append(o.flags[name], "true")
			continue
		}
		v, err := next()
		if err != nil {
			return nil, err
		}
		o.flags[name] = append(o.flags[name], v)
	}
	return o, nil
}

// Main runs the command line with args (without the program name) and
// returns the exit status.
func Main(args []string, env *Env) int {
	t, err := Load()
	if err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitFailed
	}
	return run(t, args, env)
}

func run(t *Table, args []string, env *Env) int {
	words, rest, _ := commandWords(args)
	fail := func(err error) int {
		var u *usageError
		if errors.As(err, &u) {
			fmt.Fprintln(env.Stderr, "neodesk:", u.msg)
			return ExitUsage
		}
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitFailed
	}
	if len(words) == 0 {
		o, err := parseOptions(rest, nil)
		if err != nil {
			return fail(err)
		}
		if o.version {
			fmt.Fprintln(env.Stdout, "neodesk", env.Version)
			return ExitOK
		}
		writeRootHelp(env.Stdout, t, env)
		if !o.help {
			return ExitUsage
		}
		return ExitOK
	}
	if meta, ok := metaCommands[words[0]]; ok {
		margs := rest
		if len(words) == 2 {
			margs = append([]string{words[1]}, rest...)
		}
		// local hands its arguments to the server binary's commands
		if words[0] != "local" && words[0] != "__complete" && (contains(margs, "--help") || contains(margs, "-h")) {
			writeMetaHelp(env.Stdout, words[0])
			return ExitOK
		}
		return meta.run(t, margs, env)
	}
	group := words[0]
	if !t.HasGroup(group) {
		fmt.Fprintf(env.Stderr, "neodesk: no command group %q.%s\nRun neodesk --help for the list.\n", group, suggest(group, groupNames(t)))
		return ExitUsage
	}
	if len(words) < 2 {
		o, err := parseOptions(rest, nil)
		if err != nil && !strings.Contains(err.Error(), "unknown flag") {
			return fail(err)
		}
		writeGroupHelp(env.Stdout, t, group)
		if o == nil || !o.help {
			return ExitUsage
		}
		return ExitOK
	}
	cmd := t.Find(group, words[1])
	if cmd == nil {
		var actions []string
		for _, c := range t.InGroup(group) {
			actions = append(actions, c.Action())
		}
		fmt.Fprintf(env.Stderr, "neodesk: %q has no command %q.%s\nRun neodesk %s --help for the list.\n", group, words[1], suggest(words[1], actions), group)
		return ExitUsage
	}
	o, err := parseOptions(rest, cmd)
	if err != nil {
		return fail(err)
	}
	if o.help {
		writeCommandHelp(env.Stdout, cmd)
		return ExitOK
	}
	return callCommand(cmd, o, env)
}

func groupNames(t *Table) []string {
	var out []string
	for _, g := range t.Groups {
		out = append(out, g.Name)
	}
	return out
}

// suggest names the closest of the choices to what was typed, as a
// sentence, or "" when none is close.
func suggest(typed string, choices []string) string {
	best, bestDist := "", 1<<30
	for _, c := range choices {
		d := distance(typed, c)
		if strings.HasPrefix(c, typed) || strings.Contains(c, typed) {
			d = min(d, 2)
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == "" || bestDist > 3 {
		return ""
	}
	return fmt.Sprintf(" Did you mean %q?", best)
}

// distance is the Levenshtein distance.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// meta is a command of the command line itself.
type meta struct {
	summary string
	run     func(t *Table, args []string, env *Env) int
}

var metaCommands map[string]meta

func init() {
	metaCommands = map[string]meta{
		"login":      {"Save the console's address and an API token for the next commands", runLogin},
		"logout":     {"Forget the saved token (--revoke also revokes it on the console)", runLogout},
		"whoami":     {"Show the account and the token the commands use", runWhoami},
		"config":     {"Show where the settings come from (never the token)", runConfig},
		"api":        {"Send any request to the console's API: neodesk api GET /api/stats", runAPI},
		"describe":   {"Print every command with its arguments and permissions, as JSON or Markdown", runDescribe},
		"mcp":        {"Serve the commands as tools to an AI agent over MCP (stdio); read only unless --allow-writes", runMCP},
		"completion": {"Print a shell completion script: bash, zsh, fish or powershell", runCompletion},
		"local":      {"Run a command of the server binary on this machine (backup, migrate, ...)", runLocal},
		"version":    {"Print this command line's version (the server's: neodesk server info)", runVersion},
		"help":       {"Show help for a group or command: neodesk help devices list", runHelp},
		"__complete": {"", runComplete},
	}
}

func metaNames() []string {
	var out []string
	for name, m := range metaCommands {
		if m.summary != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func runVersion(_ *Table, _ []string, env *Env) int {
	fmt.Fprintln(env.Stdout, "neodesk", env.Version)
	return ExitOK
}

func runHelp(t *Table, args []string, env *Env) int {
	var words []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	switch {
	case len(words) == 0:
		writeRootHelp(env.Stdout, t, env)
	case len(words) == 1:
		if m, ok := metaCommands[words[0]]; ok && m.summary != "" {
			writeMetaHelp(env.Stdout, words[0])
			return ExitOK
		}
		if !t.HasGroup(words[0]) {
			fmt.Fprintf(env.Stderr, "neodesk: no command group %q.%s\n", words[0], suggest(words[0], groupNames(t)))
			return ExitUsage
		}
		writeGroupHelp(env.Stdout, t, words[0])
	default:
		cmd := t.Find(words[0], words[1])
		if cmd == nil {
			fmt.Fprintf(env.Stderr, "neodesk: no command %q\n", words[0]+" "+words[1])
			return ExitUsage
		}
		writeCommandHelp(env.Stdout, cmd)
	}
	return ExitOK
}

func writeLines(w io.Writer, lines ...string) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}
