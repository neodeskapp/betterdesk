package ndcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// show writes an answer: the error with its exit status, the body to a
// file for --output, JSON for --json, or a table or list for people.
func show(env *Env, cmd *Command, o *options, res *response) int {
	if !res.ok() {
		e := res.failure()
		line := "neodesk: " + e.msg + " (HTTP " + strconv.Itoa(e.status)
		if e.code != "" {
			line += ", " + e.code
		}
		line += ")"
		fmt.Fprintln(env.Stderr, line)
		if o.json && res.isJSON() {
			writeJSON(env.Stdout, res.body)
		}
		if res.status >= 200 && res.status < 300 {
			return ExitFailed
		}
		return exitFor(res.status)
	}
	if o.output != "" {
		if err := os.WriteFile(o.output, res.body, 0o600); err != nil {
			fmt.Fprintln(env.Stderr, "neodesk:", err)
			return ExitFailed
		}
		fmt.Fprintf(env.Stderr, "Saved %d bytes to %s\n", len(res.body), o.output)
		return ExitOK
	}
	if !res.isJSON() {
		if !utf8.Valid(res.body) && env.Terminal != nil && env.Terminal(env.Stdout) {
			fmt.Fprintf(env.Stderr, "neodesk: the answer is a file (%s, %d bytes); save it with --output FILE\n", res.contentType, len(res.body))
			return ExitUsage
		}
		env.Stdout.Write(res.body)
		if len(res.body) > 0 && res.body[len(res.body)-1] != '\n' && utf8.Valid(res.body) {
			fmt.Fprintln(env.Stdout)
		}
		return ExitOK
	}
	if o.json {
		writeJSON(env.Stdout, res.body)
		return ExitOK
	}
	var body any
	dec := json.NewDecoder(bytes.NewReader(res.body))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		env.Stdout.Write(res.body)
		return ExitOK
	}
	var rows, columns = "", []string(nil)
	if cmd != nil {
		rows, columns = cmd.Rows, cmd.Columns
	}
	render(env.Stdout, body, rows, columns)
	return ExitOK
}

// writeJSON prints JSON with two spaces of indentation.
func writeJSON(w io.Writer, raw []byte) {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		w.Write(raw)
		return
	}
	out.WriteByte('\n')
	w.Write(out.Bytes())
}

// unwrap takes the data out of {"success": true, "data": ...}.
func unwrap(body any) any {
	obj, ok := body.(map[string]any)
	if !ok {
		return body
	}
	if data, has := obj["data"]; has {
		if _, isSuccess := obj["success"]; isSuccess || len(obj) == 1 {
			return data
		}
	}
	return body
}

// at follows a dotted path into a value.
func at(v any, path string) (any, bool) {
	for _, part := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return v, true
}

// render writes a value for people: a list of objects as a table, an
// object as name: value lines, anything else as it is.
func render(w io.Writer, body any, rowsPath string, columns []string) {
	if obj, ok := body.(map[string]any); ok && len(obj) == 1 {
		if s, ok := obj["success"].(bool); ok && s {
			fmt.Fprintln(w, "Done.")
			return
		}
	}
	if rowsPath != "" {
		if v, ok := at(body, rowsPath); ok {
			if list, ok := v.([]any); ok {
				table(w, list, columns)
				return
			}
		}
	}
	v := unwrap(body)
	switch x := v.(type) {
	case []any:
		table(w, x, columns)
		return
	case map[string]any:
		if key, list := onlyList(x); key != "" {
			for _, k := range sortedKeys(x) {
				if k != key {
					fmt.Fprintf(w, "%s: %s\n", k, cell(x[k], 200))
				}
			}
			table(w, list, columns)
			return
		}
		lines(w, x, "")
		return
	case nil:
		fmt.Fprintln(w, "Done.")
		return
	}
	fmt.Fprintln(w, cell(v, 0))
}

// onlyList finds the one list of objects in an object whose other values
// are plain.
func onlyList(obj map[string]any) (string, []any) {
	key := ""
	var found []any
	for k, v := range obj {
		switch x := v.(type) {
		case []any:
			if len(x) > 0 {
				if _, isObj := x[0].(map[string]any); !isObj {
					continue
				}
			}
			if key != "" {
				return "", nil
			}
			key, found = k, x
		case map[string]any:
			return "", nil
		}
	}
	return key, found
}

// lines writes an object as "name: value", nested objects with dotted
// names.
func lines(w io.Writer, obj map[string]any, prefix string) {
	for _, k := range sortedKeys(obj) {
		v := obj[k]
		if inner, ok := v.(map[string]any); ok && len(inner) > 0 {
			lines(w, inner, prefix+k+".")
			continue
		}
		fmt.Fprintf(w, "%s%s: %s\n", prefix, k, cell(v, 0))
	}
}

func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// preferred are the fields a table shows first when the command names
// none.
var preferred = []string{"id", "name", "username", "hostname", "title", "role", "status", "state", "online", "type", "action", "created_at", "last_online", "expires_at"}

// table writes rows of objects with aligned columns.
func table(w io.Writer, rows []any, columns []string) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No results.")
		return
	}
	if _, ok := rows[0].(map[string]any); !ok {
		for _, r := range rows {
			fmt.Fprintln(w, cell(r, 0))
		}
		return
	}
	if len(columns) == 0 {
		columns = pickColumns(rows)
	}
	cells := make([][]string, 0, len(rows)+1)
	head := make([]string, len(columns))
	for i, c := range columns {
		head[i] = strings.ToUpper(c)
	}
	cells = append(cells, head)
	for _, r := range rows {
		obj, _ := r.(map[string]any)
		line := make([]string, len(columns))
		for i, c := range columns {
			v, _ := at(obj, c)
			line[i] = cell(v, 48)
		}
		cells = append(cells, line)
	}
	widths := make([]int, len(columns))
	for _, line := range cells {
		for i, s := range line {
			widths[i] = max(widths[i], utf8.RuneCountInString(s))
		}
	}
	for _, line := range cells {
		var b strings.Builder
		for i, s := range line {
			b.WriteString(s)
			if i < len(line)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(s)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	if len(rows) > 1 {
		fmt.Fprintf(w, "(%d rows)\n", len(rows))
	}
}

// pickColumns chooses up to six plain fields of the rows: the usual ones
// first, then the others of the first row in name order.
func pickColumns(rows []any) []string {
	plain := map[string]bool{}
	for _, r := range rows[:min(len(rows), 5)] {
		obj, _ := r.(map[string]any)
		for k, v := range obj {
			switch v.(type) {
			case map[string]any, []any:
			default:
				plain[k] = true
			}
		}
	}
	var out []string
	for _, p := range preferred {
		if plain[p] && len(out) < 6 {
			out = append(out, p)
			delete(plain, p)
		}
	}
	rest := make([]string, 0, len(plain))
	for k := range plain {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		if len(out) >= 6 {
			break
		}
		out = append(out, k)
	}
	return out
}

// cell is a value as text, cut to limit runes (0: no limit).
func cell(v any, limit int) string {
	var s string
	switch x := v.(type) {
	case nil:
		s = "-"
	case string:
		s = x
	case json.Number:
		s = x.String()
	case bool:
		s = strconv.FormatBool(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			switch item.(type) {
			case map[string]any, []any:
				parts = nil
			default:
				parts = append(parts, cell(item, 0))
				continue
			}
			break
		}
		if parts == nil && len(x) > 0 {
			s = fmt.Sprintf("(%d items, see --json)", len(x))
		} else {
			s = strings.Join(parts, ", ")
		}
	case map[string]any:
		s = fmt.Sprintf("(%d fields, see --json)", len(x))
	default:
		s = fmt.Sprint(x)
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	if limit > 0 && utf8.RuneCountInString(s) > limit {
		r := []rune(s)
		s = string(r[:limit-1]) + "…"
	}
	return s
}
