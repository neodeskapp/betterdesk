package ndcli

import (
	"fmt"
	"io"
	"strings"
)

// writeRootHelp is `neodesk --help`.
func writeRootHelp(w io.Writer, t *Table, env *Env) {
	writeLines(w,
		"neodesk controls a NeoDesk server from the command line: devices, users,",
		"settings, backups and everything else the web console does.",
		"",
		"Usage:",
		"  neodesk <group> <action> [arguments] [flags]",
		"  neodesk <group> --help          the actions of a group",
		"  neodesk <group> <action> --help the arguments and flags of an action",
		"",
		"On Linux hosts the installer's menu and options are neodesk-setup. sudo neodesk",
		"without arguments and the installer's options, such as --update, reach it too.",
		"Where the NeoDesk desktop client is installed too, its options, such as --join",
		"and --service, and a join or support link on its own go to it",
		"(/usr/bin/neodesk).",
		"",
		"Sign in once with an API token (make one in the console: account menu, API tokens):",
		"  neodesk login --url https://desk.example.com",
		"",
		"Groups:")
	width := 0
	for _, g := range t.Groups {
		width = max(width, len(g.Name))
	}
	for _, g := range t.Groups {
		fmt.Fprintf(w, "  %-*s  %s\n", width, g.Name, g.Summary)
	}
	writeLines(w, "", "Commands of the command line itself:")
	for _, name := range metaNames() {
		fmt.Fprintf(w, "  %-*s  %s\n", width, name, metaCommands[name].summary)
	}
	writeLines(w, "",
		"Flags for every command:",
		"  --url URL          the console's address (or NEODESK_URL)",
		"  --token-file FILE  read the API token from a file (or NEODESK_TOKEN; --token works too but shows in the process list)",
		"  --json             print the server's answer as JSON",
		"  -o, --output FILE  save the answer to a file (downloads)",
		"  -q key=value       add a query parameter (repeat for more)",
		"  -f key=value       add a text field to the request body",
		"  -F key=json        add a JSON value to the request body (numbers, true/false, lists)",
		"  --data JSON        the whole request body, or @file.json",
		"  -y, --yes          do not ask before deleting or ending something",
		"  --insecure         do not check the console's certificate (self-signed test setups)",
		"  --ca-cert FILE     trust this CA certificate for the console (or NEODESK_CA_CERT)",
		"  --allow-http       allow plain HTTP to another machine (the token is not encrypted)",
		"  --timeout 60s      how long to wait for an answer",
		"  -v, --verbose      print each request's method, address and status to stderr",
		"",
		"Exit codes:")
	for _, e := range exitCodes {
		fmt.Fprintf(w, "  %-3d %s\n", e.Code, e.Meaning)
	}
	writeLines(w, "",
		"Examples:",
		"  neodesk devices list",
		"  neodesk users create --username anna --role operator --password-stdin < pw.txt",
		"  neodesk audit list --limit 20",
		"  neodesk devices get 123456789 --json",
		"",
		"Every command needs the permission it names in its help, in the account and in the token.",
		"",
		"Help: "+helpURL)
}

// helpURL is where people ask for help: the NeoDesk Discord.
const helpURL = "https://discord.gg/nwERSawufr"

// writeGroupHelp is `neodesk <group> --help`.
func writeGroupHelp(w io.Writer, t *Table, group string) {
	cmds := t.InGroup(group)
	fmt.Fprintf(w, "%s\n\nUsage:\n  neodesk %s <action> [arguments] [flags]\n\nActions:\n", t.GroupSummary(group), group)
	width := 0
	for _, c := range cmds {
		width = max(width, len(c.Action()))
	}
	for _, c := range cmds {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.Action(), c.Summary)
	}
	fmt.Fprintf(w, "\nRun neodesk %s <action> --help for its arguments and flags.\n", group)
}

// usageLine is "neodesk <group> <action> <arg> ... [flags]".
func usageLine(c *Command) string {
	parts := []string{"neodesk", c.Name}
	for _, a := range c.Args {
		parts = append(parts, "<"+a.Name+">")
	}
	for _, f := range c.Flags {
		switch {
		case f.Required && f.Secret:
			parts = append(parts, "(--"+f.Name+"-stdin | --"+f.Name+" "+flagPlaceholder(f)+")")
		case f.Required:
			parts = append(parts, "--"+f.Name+" "+flagPlaceholder(f))
		}
	}
	if c.Upload != "" {
		parts = append(parts, "--file PATH")
	}
	parts = append(parts, "[flags]")
	return strings.Join(parts, " ")
}

func flagPlaceholder(f Flag) string {
	switch f.Type {
	case "int":
		return "N"
	case "bool":
		return ""
	case "list":
		return "A,B"
	case "json":
		return "JSON"
	}
	if len(f.Enum) > 0 {
		return strings.Join(f.Enum, "|")
	}
	return strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
}

// flagHelp is a flag's help with whether it is required.
func flagHelp(f Flag) string {
	switch {
	case f.Required && f.Secret:
		return strings.TrimSpace(f.Help + " (this or --" + f.Name + "-stdin is required)")
	case f.Required:
		return strings.TrimSpace(f.Help + " (required)")
	}
	return f.Help
}

// writeCommandHelp is `neodesk <group> <action> --help`.
func writeCommandHelp(w io.Writer, c *Command) {
	fmt.Fprintln(w, c.Summary)
	if c.Description != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, wrap(c.Description, 76))
	}
	fmt.Fprintf(w, "\nUsage:\n  %s\n", usageLine(c))
	if len(c.Args) > 0 {
		fmt.Fprintln(w, "\nArguments:")
		for _, a := range c.Args {
			fmt.Fprintf(w, "  <%s>  %s\n", a.Name, a.Help)
		}
	}
	if len(c.Flags) > 0 || c.Upload != "" {
		fmt.Fprintln(w, "\nFlags:")
		// The help column starts after the longest label up to 28
		// characters; a longer label (a long list of choices) gets its help
		// on the next line
		width := 0
		for _, f := range c.Flags {
			if n := len(f.Name) + len(flagPlaceholder(f)) + 1; n <= 28 {
				width = max(width, n)
			}
			if f.Secret {
				width = max(width, len(f.Name)+len("-stdin"))
			}
		}
		for _, f := range c.Flags {
			if f.Secret {
				fmt.Fprintf(w, "  --%-*s  %s\n", width, f.Name+"-stdin", "read the "+strings.ReplaceAll(f.Name, "-", " ")+" from standard input (its first line), so it stays out of the command line")
			}
			label := strings.TrimSpace(f.Name + " " + flagPlaceholder(f))
			if len(label) > width {
				fmt.Fprintf(w, "  --%s\n%*s%s\n", label, width+6, "", flagHelp(f))
				continue
			}
			fmt.Fprintf(w, "  --%-*s  %s\n", width, label, flagHelp(f))
		}
		if c.Upload != "" {
			fmt.Fprintf(w, "  --%-*s  %s\n", width, "file PATH", "the file to upload")
		}
	}
	fmt.Fprintln(w)
	if c.Permission != "" {
		fmt.Fprintf(w, "Needs: %s.\n", c.Permission)
	} else {
		fmt.Fprintln(w, "Needs: an account that may use this part of the console (see neodesk account access).")
	}
	if c.ReadOnly() {
		fmt.Fprintln(w, "Only reads: works with a read-only token.")
	} else {
		fmt.Fprintln(w, "Changes something: needs a token with read and write access.")
	}
	if c.Destructive {
		fmt.Fprintln(w, "Deletes or ends something for good: asks first, or pass --yes.")
	}
	if c.Exec {
		fmt.Fprintln(w, "Runs something on a device or the server (neodesk mcp offers it only with --allow-exec).")
	}
	if c.Secrets {
		fmt.Fprintln(w, "The answer holds secrets or private content: keep it safe (neodesk mcp offers it only with --allow-secrets).")
	}
	fmt.Fprintf(w, "Calls: %s\n", c.Route)
	if len(c.Examples) > 0 {
		fmt.Fprintln(w, "\nExamples:")
		for _, e := range c.Examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	fmt.Fprintln(w, "\nTables cut long values (…); --json prints the server's whole answer. -q, -f and -F add other query or body fields.")
}

// writeMetaHelp is the help of the command line's own commands.
func writeMetaHelp(w io.Writer, name string) {
	switch name {
	case "login":
		writeLines(w, "Save the console's address and an API token, checked against the console.",
			"", "Usage:", "  neodesk login --url URL [--insecure] [--ca-cert FILE] [--allow-http]",
			"", "The token is read from standard input: pipe it in, or paste it when asked (it is not shown).",
			"Make it in the console: account menu, API tokens.",
			"The address and token go into the config file (mode 600) and are used only together.",
			"Plain http:// works only for this machine (localhost) unless you add --allow-http.",
			"--insecure is not saved: give it to each command, or trust the console's certificate with --ca-cert FILE.",
			"", "Examples:", "  neodesk login --url https://desk.example.com",
			"  neodesk login --url https://desk.example.com < token.txt")
	case "logout":
		writeLines(w, "Forget the saved token.", "", "Usage:", "  neodesk logout [--revoke]",
			"", "--revoke also revokes the token on the console first.")
	case "whoami":
		writeLines(w, "Show the account and the API token the commands use, what the token may do, and when it expires.",
			"", "Usage:", "  neodesk whoami [--json]")
	case "config":
		writeLines(w, "Show the config file's place, the console's address and where the token comes from. The token itself is never printed.",
			"", "Usage:", "  neodesk config [--json]")
	case "api":
		writeLines(w, "Send any request to the console's API, for what no command covers yet.",
			"", "Usage:", "  neodesk api <METHOD> <PATH> [-q key=value] [-f key=value] [-F key=json] [--data JSON]",
			"", "Examples:", "  neodesk api GET /api/stats", "  neodesk api PATCH /api/devices/123456789 -f note=\"Front desk\"")
	case "describe":
		writeLines(w, "Print every command with its route, arguments, flags, permission and examples.",
			"", "Usage:", "  neodesk describe [--format json|markdown] [group]",
			"", "JSON is the default. The Markdown form is the command reference of the docs.")
	case "mcp":
		writeLines(w, "Serve the commands as MCP tools over standard input and output, for AI agents.",
			"", "Usage:", "  neodesk mcp [--allow-writes [--allow-exec]] [--allow-secrets] [--groups devices,users] [--compact]",
			"", "It uses the saved address and token (or --url and NEODESK_URL with NEODESK_TOKEN).",
			"Without --allow-writes only commands that read are offered.",
			"--allow-exec adds the commands that run something on a device or the server.",
			"--allow-secrets adds the commands whose answer holds secrets or private content.",
			"--compact offers two tools, one to look commands up and one to run them.",
			"Tools that delete or end something, run something or change who may do what need confirm: true.",
			"That only makes the agent ask: give the agent a token with only the permissions it needs.",
			"The token's own permissions and read-only setting still apply on the server.")
	case "completion":
		writeLines(w, "Print a completion script for your shell.", "", "Usage:", "  neodesk completion bash|zsh|fish|powershell",
			"", "Examples:", "  source <(neodesk completion bash)", "  neodesk completion fish > ~/.config/fish/completions/neodesk.fish",
			"  neodesk completion powershell | Out-String | Invoke-Expression")
	case "local":
		writeLines(w, "Run a command of the server binary on this machine. These read the server's own files and need no token.",
			"", "Usage:", "  neodesk local <command> [arguments]", "", "Run neodesk local for the list.")
	case "version":
		writeLines(w, "Print this command line's version. The server's version: neodesk server info.", "", "Usage:", "  neodesk version")
	case "help":
		writeLines(w, "Show help.", "", "Usage:", "  neodesk help [group] [action]")
	}
}

// wrap breaks text into lines of at most width characters.
func wrap(text string, width int) string {
	var out strings.Builder
	for _, para := range strings.Split(text, "\n") {
		line := 0
		for i, word := range strings.Fields(para) {
			if i > 0 && line+1+len(word) > width {
				out.WriteByte('\n')
				line = 0
			} else if i > 0 {
				out.WriteByte(' ')
				line++
			}
			out.WriteString(word)
			line += len(word)
		}
		out.WriteByte('\n')
	}
	return strings.TrimRight(out.String(), "\n")
}
