package ndcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// describe prints every command with its route, arguments, flags and
// permission, as JSON and as Markdown, from the same table.
func TestDescribeHoldsEveryCommand(t *testing.T) {
	tab, _ := Load()
	code, out, stderr := runCLI(t, nil, "", "describe")
	if code != 0 {
		t.Fatalf("describe: %d %s", code, stderr)
	}
	var d Description
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("describe is no JSON: %v", err)
	}
	n := 0
	for _, g := range d.Groups {
		for _, c := range g.Commands {
			n++
			cmd := tab.Find(g.Name, c.Action)
			if cmd == nil || c.Method+" "+c.Path != cmd.Route || c.Permission == "" || !strings.HasPrefix(c.Usage, "neodesk "+cmd.Name) {
				t.Errorf("%s: %+v", c.Name, c)
			}
		}
	}
	if n != len(tab.Commands) || d.Counts["commands"] != n || len(d.Excluded) != len(tab.Excluded) || len(d.ExitCodes) != len(exitCodes) {
		t.Errorf("describe has %d commands of %d, %d exclusions of %d", n, len(tab.Commands), len(d.Excluded), len(tab.Excluded))
	}

	code, md, _ := runCLI(t, nil, "", "describe", "--format", "markdown")
	if code != 0 {
		t.Fatalf("describe markdown: %d", code)
	}
	for _, c := range tab.Commands {
		if !strings.Contains(md, "### neodesk "+c.Name+"\n") {
			t.Errorf("the Markdown lacks %s", c.Name)
		}
	}
	for _, e := range tab.Excluded {
		if !strings.Contains(md, "- `"+e.Route+"`") {
			t.Errorf("the Markdown lacks the excluded %s", e.Route)
		}
	}
	if strings.Contains(md, "—") || strings.Contains(md, " · ") {
		t.Error("the Markdown has an em dash or a dot triad")
	}

	code, one, _ := runCLI(t, nil, "", "describe", "users", "--format=markdown")
	if code != 0 || !strings.HasPrefix(one, "## users") || strings.Contains(one, "## devices") {
		t.Errorf("describe users: %d %.80q", code, one)
	}
	if code, _, _ := runCLI(t, nil, "", "describe", "nope"); code != ExitUsage {
		t.Errorf("describe nope: %d", code)
	}
	if code, _, _ := runCLI(t, nil, "", "describe", "--format", "xml"); code != ExitUsage {
		t.Errorf("describe xml: %d", code)
	}
}

// cells splits a Markdown table row into its cells: a | that a backslash
// escapes belongs to the cell.
func cells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	var out []string
	var cur strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case row[i] == '|':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(row[i])
		}
	}
	return append(out, cur.String())
}

// Every row of the reference's tables has the cells of its header: a |
// in a cell, as in the choices of devices list --status online|offline,
// is escaped, also inside the code of the flag's column (L445).
func TestTheReferenceTablesKeepTheirCells(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	WriteMarkdown(&b, tab.Describe("test"))
	width, broken := 0, 0
	for i, line := range strings.Split(b.String(), "\n") {
		if !strings.HasPrefix(line, "|") {
			width = 0
			continue
		}
		n := len(cells(line))
		if width == 0 {
			width = n
			continue
		}
		if n != width {
			broken++
			if broken <= 5 {
				t.Errorf("line %d has %d cells, its table %d: %s", i+1, n, width, line)
			}
		}
	}
	if broken > 5 {
		t.Errorf("%d rows in all have the wrong number of cells", broken)
	}
	if !strings.Contains(b.String(), "| `--status online\\|offline` |") {
		t.Error("the choices of devices list --status are not in the reference as one cell")
	}
}

// Every flag says what it is: the reference and --help have no empty
// cells (L445).
func TestEveryFlagSaysWhatItIs(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var empty []string
	for _, c := range tab.Commands {
		for _, f := range c.Flags {
			if strings.TrimSpace(f.Help) == "" {
				empty = append(empty, c.Name+" --"+f.Name)
			}
		}
	}
	if len(empty) > 0 {
		t.Errorf("%d flags have no help text:\n  %s", len(empty), strings.Join(empty, "\n  "))
	}
}
