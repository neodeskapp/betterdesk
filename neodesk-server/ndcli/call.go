package ndcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// buildRequest turns a command and its options into a request. stdin
// gives the values of --<secret>-stdin, one line each.
func buildRequest(cmd *Command, o *options, stdin io.Reader) (request, error) {
	req := request{method: cmd.Method(), query: url.Values{}}
	if req.method == "ALL" {
		req.method = "POST"
	}
	// Path arguments, in order
	params := pathParams(cmd.Path())
	if len(o.positional) < len(params) {
		var missing []string
		for _, a := range cmd.Args[len(o.positional):] {
			missing = append(missing, "<"+a.Name+">")
		}
		return req, usagef("%s needs %s; see: neodesk %s --help", cmd.Name, strings.Join(missing, " "), cmd.Name)
	}
	if len(o.positional) > len(params) {
		return req, usagef("%s takes %d argument(s), got %d: %s", cmd.Name, len(params), len(o.positional), strings.Join(o.positional, " "))
	}
	path := cmd.Path()
	for i, name := range params {
		value := o.positional[i]
		if value == "" {
			return req, usagef("<%s> is empty", name)
		}
		// A proxy that cleans paths would turn x/.. into another route
		for _, part := range strings.Split(value, "/") {
			if part == "." || part == ".." {
				return req, usagef("<%s> cannot be or hold %q", name, part)
			}
		}
		escaped := url.PathEscape(value)
		if strings.HasSuffix(paramAt(path, i), "...}") {
			escaped = strings.ReplaceAll(url.PathEscape(value), "%2F", "/")
		}
		path = strings.Replace(path, paramAt(path, i), escaped, 1)
	}
	req.path = path

	// Secrets from standard input, one line each, in the order given
	if len(o.fromStdin) > 0 {
		reader := bufio.NewReader(stdin)
		for _, name := range o.fromStdin {
			line, err := reader.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			if line == "" && err != nil {
				return req, usagef("--%s-stdin: nothing on standard input", name)
			}
			o.flags[name] = append(o.flags[name], line)
		}
	}
	// Flags into the query or the body
	body := map[string]any{}
	if o.data != "" {
		raw, err := readData(o.data)
		if err != nil {
			return req, err
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return req, usagef("--data must be a JSON object: %v", err)
		}
	}
	for _, f := range cmd.Flags {
		values, given := o.flags[f.Name]
		if !given {
			if f.Required {
				return req, usagef("%s needs --%s; see: neodesk %s --help", cmd.Name, f.Name, cmd.Name)
			}
			continue
		}
		value := values[len(values)-1]
		if len(f.Enum) > 0 && !contains(f.Enum, value) {
			return req, usagef("--%s takes one of: %s", f.Name, strings.Join(f.Enum, ", "))
		}
		if f.In == "query" {
			for _, v := range values {
				req.query.Add(f.Key(), v)
			}
			continue
		}
		typed, err := typedValue(f, values)
		if err != nil {
			return req, err
		}
		body[f.Key()] = typed
	}
	for _, q := range o.queries {
		req.query.Add(q.key, q.value)
	}
	for _, f := range o.fields {
		body[f.key] = f.value
	}
	for _, f := range o.rawFields {
		var v any
		if err := json.Unmarshal([]byte(f.value), &v); err != nil {
			return req, usagef("-F %s: %q is not JSON (use -f for text)", f.key, f.value)
		}
		body[f.key] = v
	}
	switch {
	case cmd.Upload != "":
		if o.file == "" {
			return req, usagef("%s needs --file PATH", cmd.Name)
		}
		fields := map[string]string{}
		for k, v := range body {
			switch x := v.(type) {
			case string:
				fields[k] = x
			default:
				b, _ := json.Marshal(x)
				fields[k] = string(b)
			}
		}
		req.upload = &upload{field: cmd.Upload, path: o.file, fields: fields}
	case len(body) > 0:
		if req.method == "GET" {
			return req, usagef("%s reads only: give its options as flags or -q key=value, not as body fields", cmd.Name)
		}
		req.body = body
	case o.file != "":
		return req, usagef("%s takes no file", cmd.Name)
	}
	return req, nil
}

// paramAt is the i-th parameter of a pattern, with its braces.
func paramAt(pattern string, i int) string {
	return paramRe.FindAllString(pattern, -1)[i]
}

func readData(data string) ([]byte, error) {
	switch {
	case data == "-":
		return nil, usagef("--data - is not supported; use --data @file.json")
	case strings.HasPrefix(data, "@"):
		b, err := os.ReadFile(data[1:])
		if err != nil {
			return nil, usagef("--data: %v", err)
		}
		return b, nil
	}
	return []byte(data), nil
}

func typedValue(f Flag, values []string) (any, error) {
	value := values[len(values)-1]
	switch f.Type {
	case "int":
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return nil, usagef("--%s takes a whole number, got %q", f.Name, value)
		}
		return n, nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		}
		return nil, usagef("--%s takes true or false, got %q", f.Name, value)
	case "list":
		out := []string{}
		for _, v := range values {
			for _, part := range strings.Split(v, ",") {
				if p := strings.TrimSpace(part); p != "" {
					out = append(out, p)
				}
			}
		}
		return out, nil
	case "json":
		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			return nil, usagef("--%s takes JSON, got %q", f.Name, value)
		}
		return v, nil
	}
	return value, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// callCommand runs a command of the table.
func callCommand(cmd *Command, o *options, env *Env) int {
	req, err := buildRequest(cmd, o, env.Stdin)
	if err != nil {
		var u *usageError
		if errors.As(err, &u) {
			fmt.Fprintln(env.Stderr, "neodesk:", u.msg)
			return ExitUsage
		}
		fmt.Fprintln(env.Stderr, "neodesk:", err)
		return ExitFailed
	}
	c, err := connect(o, env, true)
	if err != nil {
		return reportConnError(env, err)
	}
	if cmd.Destructive && !o.yes {
		if !confirm(env, cmd, o.positional) {
			return ExitUsage
		}
	}
	res, err := c.do(context.Background(), req)
	if err != nil {
		return reportConnError(env, err)
	}
	return show(env, cmd, o, res)
}

// confirm asks before a destructive command: on a terminal it asks for
// "yes", elsewhere it needs --yes.
func confirm(env *Env, cmd *Command, args []string) bool {
	what := cmd.Name
	if len(args) > 0 {
		what += " " + strings.Join(args, " ")
	}
	if env.Terminal == nil || !env.Terminal(env.Stdin) {
		fmt.Fprintf(env.Stderr, "neodesk: %s %s. Add --yes to go ahead.\n", what, destroyWords)
		return false
	}
	fmt.Fprintf(env.Stderr, "%s %s. Type yes to go ahead: ", what, destroyWords)
	line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
	if strings.TrimSpace(strings.ToLower(line)) != "yes" {
		fmt.Fprintln(env.Stderr, "Nothing was changed.")
		return false
	}
	return true
}

const destroyWords = "deletes or ends something for good"

func reportConnError(env *Env, err error) int {
	var ce *connError
	if errors.As(err, &ce) {
		fmt.Fprintln(env.Stderr, "neodesk:", ce.msg)
		return ce.code
	}
	var u *usageError
	if errors.As(err, &u) {
		fmt.Fprintln(env.Stderr, "neodesk:", u.msg)
		return ExitUsage
	}
	fmt.Fprintln(env.Stderr, "neodesk:", err)
	return ExitFailed
}
