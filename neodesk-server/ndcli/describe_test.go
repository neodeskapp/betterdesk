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
