package ndcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// metaOptions parses the flags of a command of the command line itself,
// which takes the flags every command has.
func metaOptions(env *Env, args []string, name string) (*options, bool, int) {
	o, err := parseOptions(args, nil)
	if err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return nil, false, ExitUsage
	}
	if o.help {
		writeMetaHelp(env.Stdout, name)
		return nil, false, ExitOK
	}
	return o, true, 0
}

// currentToken asks the console about the token in use.
func currentToken(c *conn) (map[string]any, *response, error) {
	res, err := c.do(context.Background(), request{method: "GET", path: "/api/panel/api-tokens/current"})
	if err != nil {
		return nil, nil, err
	}
	if !res.ok() {
		return nil, res, nil
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil || body.Data == nil {
		return nil, res, errors.New("the console sent an answer the command line does not know; is it a NeoDesk console with API tokens (3.6.0-rc.4 or later)?")
	}
	return body.Data, res, nil
}

// runLogin checks a token against the console and saves it with the
// address.
func runLogin(_ *Table, args []string, env *Env) int {
	o, ok, code := metaOptions(env, args, "login")
	if !ok {
		return code
	}
	if len(o.positional) > 0 {
		fmt.Fprintln(env.Stderr, "neodesk: login takes no token as an argument (it would stay in your shell history). Pipe it in or paste it when asked.")
		return ExitUsage
	}
	if o.url == "" && env.Getenv("NEODESK_URL") == "" {
		fmt.Fprintln(env.Stderr, "neodesk: login needs --url, the console's address, like https://desk.example.com")
		return ExitUsage
	}
	token := o.token
	if token == "" && o.tokenFile != "" {
		data, err := os.ReadFile(o.tokenFile)
		if err != nil {
			fmt.Fprintln(env.Stderr, "neodesk:", err)
			return ExitConfig
		}
		token = string(data)
	}
	if token == "" {
		if env.Terminal != nil && env.Terminal(env.Stdin) && env.ReadSecret != nil {
			fmt.Fprint(env.Stderr, "Paste the API token (it is not shown): ")
			t, err := env.ReadSecret()
			fmt.Fprintln(env.Stderr)
			if err != nil {
				fmt.Fprintln(env.Stderr, "neodesk:", err)
				return ExitUsage
			}
			token = t
		} else {
			line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
			token = line
		}
	}
	token = strings.TrimSpace(token)
	if token == "" {
		fmt.Fprintln(env.Stderr, "neodesk: no token given")
		return ExitUsage
	}
	o.token = token
	c, err := connect(o, env, true)
	if err != nil {
		return reportConnError(env, err)
	}
	info, res, err := currentToken(c)
	if err != nil {
		return reportConnError(env, err)
	}
	if info == nil {
		return show(env, nil, &options{}, res)
	}
	path, err := env.configPath(o.config)
	if err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitConfig
	}
	cfg := &Config{URL: c.url, Token: token, CACert: o.caCert, AllowHTTP: o.allowHTTP}
	if o.insecure {
		fmt.Fprintln(env.Stderr, "neodesk: --insecure is not saved: pass it to each command, or save the console's certificate and use --ca-cert FILE")
	}
	if err := saveConfig(path, cfg); err != nil {
		fmt.Fprintln(env.Stderr, "neodesk: save the config:", err)
		return ExitConfig
	}
	tok, _ := info["api_token"].(map[string]any)
	acct, _ := info["account"].(map[string]any)
	fmt.Fprintf(env.Stdout, "Signed in to %s as %s (%s) with the token %q (%s).\nSaved in %s.\n",
		c.url, cell(acct["username"], 0), cell(acct["role"], 0), cell(tok["name"], 0), cell(tok["hint"], 0), path)
	return ExitOK
}

// runLogout forgets the saved token, and with --revoke revokes it first.
func runLogout(_ *Table, args []string, env *Env) int {
	revoke := false
	var rest []string
	for _, a := range args {
		if a == "--revoke" {
			revoke = true
			continue
		}
		rest = append(rest, a)
	}
	o, ok, code := metaOptions(env, rest, "logout")
	if !ok {
		return code
	}
	path, err := env.configPath(o.config)
	if err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitConfig
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitConfig
	}
	if cfg.Token == "" {
		fmt.Fprintln(env.Stdout, "No token is saved.")
		return ExitOK
	}
	if revoke {
		c, err := connect(&options{config: o.config, timeout: o.timeout, insecure: o.insecure, caCert: o.caCert, allowHTTP: o.allowHTTP}, env, true)
		if err != nil {
			return reportConnError(env, err)
		}
		info, res, err := currentToken(c)
		if err != nil {
			return reportConnError(env, err)
		}
		if info == nil {
			return show(env, nil, &options{}, res)
		}
		tok, _ := info["api_token"].(map[string]any)
		id, _ := tok["id"].(string)
		res, err = c.do(context.Background(), request{method: "DELETE", path: "/api/panel/api-tokens/" + url.PathEscape(id)})
		if err != nil {
			return reportConnError(env, err)
		}
		if !res.ok() {
			return show(env, nil, &options{}, res)
		}
		fmt.Fprintf(env.Stdout, "Revoked the token %q on %s.\n", cell(tok["name"], 0), c.url)
	}
	cfg.Token = ""
	if err := saveConfig(path, cfg); err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitConfig
	}
	fmt.Fprintf(env.Stdout, "Forgot the token saved in %s.\n", path)
	return ExitOK
}

// runWhoami shows the account and token in use.
func runWhoami(_ *Table, args []string, env *Env) int {
	o, ok, code := metaOptions(env, args, "whoami")
	if !ok {
		return code
	}
	c, err := connect(o, env, true)
	if err != nil {
		return reportConnError(env, err)
	}
	info, res, err := currentToken(c)
	if err != nil {
		return reportConnError(env, err)
	}
	if info == nil {
		return show(env, nil, o, res)
	}
	if o.json {
		writeJSON(env.Stdout, res.body)
		return ExitOK
	}
	tok, _ := info["api_token"].(map[string]any)
	acct, _ := info["account"].(map[string]any)
	perms, _ := info["permissions"].([]any)
	access := "read and write"
	if ro, _ := tok["read_only"].(bool); ro {
		access = "read only"
	}
	full := "no: role checks, changes to accounts, server.config and the server's secrets are refused"
	if f, _ := info["full"].(bool); f {
		full = "yes"
	}
	fmt.Fprintf(env.Stdout, "Console:     %s\nAccount:     %s (%s)\nToken:       %s (%s), from %s\nAccess:      %s\nExpires:     %s\nPermissions: %s\nAll of the account's permissions: %s\n",
		c.url, cell(acct["username"], 0), cell(acct["role"], 0), cell(tok["name"], 0), cell(tok["hint"], 0), c.tokenSource, access, cell(tok["expires_at"], 0), joinAny(perms), full)
	if list, _ := tok["allow_list"].([]any); len(list) > 0 {
		fmt.Fprintf(env.Stdout, "Only from:   %s\n", joinAny(list))
	}
	return ExitOK
}

func joinAny(list []any) string {
	parts := make([]string, 0, len(list))
	for _, v := range list {
		parts = append(parts, fmt.Sprint(v))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// runConfig shows where the settings come from, never the token.
func runConfig(_ *Table, args []string, env *Env) int {
	if len(args) > 0 && (args[0] == "show" || args[0] == "path") {
		args = args[1:]
	}
	o, ok, code := metaOptions(env, args, "config")
	if !ok {
		return code
	}
	path, err := env.configPath(o.config)
	if err != nil {
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitConfig
	}
	cfg, loadErr := loadConfig(path)
	if cfg == nil {
		cfg = &Config{}
	}
	out := map[string]any{"config_file": path}
	if loadErr != nil {
		out["config_error"] = loadErr.Error()
	}
	urlSource := "config file"
	switch {
	case o.url != "":
		out["url"], urlSource = o.url, "--url"
	case env.Getenv("NEODESK_URL") != "":
		out["url"], urlSource = env.Getenv("NEODESK_URL"), "NEODESK_URL"
	default:
		out["url"] = cfg.URL
	}
	out["url_from"] = urlSource
	hint, source := "", ""
	switch {
	case o.token != "":
		hint, source = tokenHint(o.token), "--token"
	case o.tokenFile != "":
		hint, source = "(in "+o.tokenFile+")", "--token-file"
	case env.Getenv("NEODESK_TOKEN") != "":
		hint, source = tokenHint(env.Getenv("NEODESK_TOKEN")), "NEODESK_TOKEN"
	case cfg.Token != "":
		hint, source = tokenHint(cfg.Token), "config file, for "+cfg.URL
	}
	out["token"], out["token_from"] = hint, source
	if o.json {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(env.Stdout, string(b))
		return ExitOK
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(env.Stdout, "%s: %s\n", k, cell(out[k], 0))
	}
	if loadErr != nil {
		return ExitConfig
	}
	return ExitOK
}

// tokenHint shows a token as ndt_<id>_…, never its secret.
func tokenHint(token string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(token), "ndt_")
	if !ok || len(rest) < 12 {
		return "(not a NeoDesk token)"
	}
	return "ndt_" + rest[:12] + "_…"
}

// runAPI sends any request.
func runAPI(_ *Table, args []string, env *Env) int {
	o, ok, code := metaOptions(env, args, "api")
	if !ok {
		return code
	}
	if len(o.positional) != 2 {
		writeMetaHelp(env.Stderr, "api")
		return ExitUsage
	}
	method := strings.ToUpper(o.positional[0])
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		fmt.Fprintf(env.Stderr, "neodesk: %q is no method; use GET, POST, PUT, PATCH or DELETE\n", o.positional[0])
		return ExitUsage
	}
	path := o.positional[1]
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") {
		fmt.Fprintln(env.Stderr, "neodesk: give the path on the console, like /api/stats")
		return ExitUsage
	}
	cmd := &Command{Name: "api call", Route: method + " " + path}
	req, err := buildRequest(&Command{Name: "api call", Route: method + " /"}, o2(o), env.Stdin)
	if err != nil {
		return reportConnError(env, err)
	}
	req.method = method
	if p, q, found := strings.Cut(path, "?"); found {
		path = p
		extra, err := url.ParseQuery(q)
		if err != nil {
			fmt.Fprintln(env.Stderr, "neodesk: the query of the path is not valid")
			return ExitUsage
		}
		for k, vs := range extra {
			for _, v := range vs {
				req.query.Add(k, v)
			}
		}
	}
	req.path = path
	c, err := connect(o, env, true)
	if err != nil {
		return reportConnError(env, err)
	}
	res, err := c.do(context.Background(), req)
	if err != nil {
		return reportConnError(env, err)
	}
	return show(env, cmd, o, res)
}

// o2 is o without positional arguments, for buildRequest.
func o2(o *options) *options {
	c := *o
	c.positional = nil
	return &c
}

// runLocal runs a command of the server binary.
func runLocal(_ *Table, args []string, env *Env) int {
	names := make([]string, 0, len(env.Local))
	for n := range env.Local {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(env.Stdout, "Commands of the server binary. They run on this machine, read the server's own files, and need no token.\n\nUsage:\n  neodesk local <command> [arguments]\n\nCommands:")
		for _, n := range names {
			fmt.Fprintf(env.Stdout, "  %s\n", env.Local[n].Usage)
		}
		if len(args) == 0 {
			return ExitUsage
		}
		return ExitOK
	}
	cmd, ok := env.Local[args[0]]
	if !ok {
		fmt.Fprintf(env.Stderr, "neodesk: no local command %q.%s Run neodesk local for the list.\n", args[0], suggest(args[0], names))
		return ExitUsage
	}
	return cmd.Run(args[1:], env.Stdin, env.Stdout, env.Stderr)
}
