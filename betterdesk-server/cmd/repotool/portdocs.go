package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// checkPortDocs is "check-port-docs": docs/CONNECTION_PORTS.md names the
// ports the code declares (signal, relay and API in config/constants.go,
// the console's API_PORT default in console/config/config.go), and its
// signal row has the signal port. It also fails when a document, script
// or Go file presents 21120 as a live port: that number was in an early
// design and never shipped. A file may mention it where it says so
// ("does not exist", "never shipped", "historical").
func checkPortDocs(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: repotool check-port-docs")
		return 2
	}
	var failures []string
	read := func(file string) string {
		text, err := readText(file)
		if err != nil {
			failures = append(failures, fmt.Sprintf("cannot read %s: %s", relPath(root, file), readError(file, err)))
			return ""
		}
		return text
	}

	goSource := read(filepath.Join(root, "betterdesk-server", "config", "constants.go"))
	consoleSource := read(filepath.Join(root, "betterdesk-server", "console", "config", "config.go"))

	ports := []codePort{
		{label: "signal", name: "DefaultSignalPort"},
		{label: "relay", name: "DefaultRelayPort"},
		{label: "api", name: "DefaultAPIPort"},
		{label: "clientApi"},
	}
	for i := range ports[:3] {
		ports[i].value, ports[i].found = goPort(goSource, ports[i].name)
		if !ports[i].found {
			failures = append(failures, "constants.go: "+ports[i].name+" not found")
		}
	}
	ports[3].value, ports[3].found = consolePort(consoleSource, "API_PORT")
	if !ports[3].found {
		failures = append(failures, "console/config/config.go: the API_PORT default not found")
	}

	if doc := read(filepath.Join(root, "docs", "CONNECTION_PORTS.md")); doc != "" {
		// Every port the code declares appears in the document
		for _, p := range ports {
			if !p.found {
				continue
			}
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(p.String()) + `\b`).MatchString(doc) {
				failures = append(failures, fmt.Sprintf("docs/CONNECTION_PORTS.md does not mention %s port %s declared in code", p.label, p))
			}
		}
		// and its signal row has the signal port
		if m := reSignalRow.FindStringSubmatch(doc); m == nil {
			failures = append(failures, "docs/CONNECTION_PORTS.md: signal row (TCP + UDP) not found")
		} else if n, _ := strconv.ParseFloat(m[1], 64); !ports[0].found || n != ports[0].value {
			failures = append(failures, fmt.Sprintf("docs/CONNECTION_PORTS.md signal row says %s, code says %s", m[1], ports[0]))
		}
	}

	for _, dir := range []string{"docs", "betterdesk-server", "scripts"} {
		failures = append(failures, stalePortFindings(root, filepath.Join(root, dir))...)
	}

	if len(failures) > 0 {
		fmt.Fprintln(stderr, "Port documentation check failed:")
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %s\n", f)
		}
		return 1
	}
	fmt.Fprintf(stdout, "Port docs OK (signal %s, relay %s, api %s, client-api proxy %s)\n",
		ports[0], ports[1], ports[2], ports[3])
	return 0
}

var (
	reSignalRow  = regexp.MustCompile(`(?m)^\|` + jsSpace + `*\*\*(\d{4,5})\*\*` + jsSpace + `*\|` + jsSpace + `*TCP \+ UDP`)
	reStalePort  = regexp.MustCompile(`21120`)
	reHistorical = regexp.MustCompile(`(?i)does not exist|never shipped|HISTORICAL|histor`)
	reScanned    = regexp.MustCompile(`\.(md|js|sh|ps1|go)$`)
)

// portDocsSelf are this check's own files, which name the stale port.
var portDocsSelf = []string{
	filepath.Join("betterdesk-server", "cmd", "repotool", "portdocs.go"),
	filepath.Join("betterdesk-server", "cmd", "repotool", "portdocs_test.go"),
}

// codePort is a port the code declares: the constant's name, and its
// value when the constant was found.
type codePort struct {
	label string
	name  string
	value float64
	found bool
}

// String is the port as the messages show it: null when not found.
func (p codePort) String() string {
	if !p.found {
		return "null"
	}
	return jsNumber(p.value)
}

// goPort is the number assigned to name in Go source ("Name = 21116").
func goPort(source, name string) (float64, bool) {
	return firstNumber(regexp.MustCompile(regexp.QuoteMeta(name)+jsSpace+`*=`+jsSpace+`*(\d+)`), source)
}

// consolePort is the default of a variable in console/config/config.go:
// `cfg.APIPort = parseIntOr(getenv("API_PORT"), 21121)`.
func consolePort(source, variable string) (float64, bool) {
	return firstNumber(regexp.MustCompile(`getenv\("`+regexp.QuoteMeta(variable)+`"\), (\d{4,5})\)`), source)
}

func firstNumber(re *regexp.Regexp, source string) (float64, bool) {
	m := re.FindStringSubmatch(source)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	return n, true
}

// stalePortFindings walks dir for files that mention the stale port
// without saying it does not exist. Folders it cannot read are left out.
func stalePortFindings(root, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var failures []string
	for _, e := range entries {
		if e.Name() == "node_modules" || e.Name() == ".git" || e.Name() == "private" {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			failures = append(failures, stalePortFindings(root, full)...)
			continue
		}
		if !e.Type().IsRegular() || !reScanned.MatchString(e.Name()) {
			continue
		}
		if isPortDocsSelf(root, full) {
			continue
		}
		// Vendored toolchains hold many unrelated files
		if sep := string(filepath.Separator); strings.Contains(full, sep+"data"+sep+"go-toolchain"+sep) {
			continue
		}
		text, err := os.ReadFile(full)
		if err != nil || !reStalePort.Match(text) {
			continue
		}
		if !reHistorical.Match(text) {
			failures = append(failures, relPath(root, full)+" mentions port 21120, which does not exist in the code")
		}
	}
	return failures
}

func isPortDocsSelf(root, full string) bool {
	for _, self := range portDocsSelf {
		if full == filepath.Join(root, self) {
			return true
		}
	}
	return false
}

// relPath is path relative to root, as path.relative words it.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
