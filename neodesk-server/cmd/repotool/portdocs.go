package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// checkPortDocs is "check-port-docs": it fails when a script or Go file
// presents 21120 as a live port. That number was in an early design and
// never shipped; a file may mention it where it says so ("does not exist",
// "never shipped", "historical"). The port table it once compared with the
// code is on the website now (neode.sk/docs/operations/ports), outside
// the repository, so the check no longer reads a document.
func checkPortDocs(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: repotool check-port-docs")
		return 2
	}
	var failures []string
	for _, dir := range []string{"neodesk-server", "scripts"} {
		failures = append(failures, stalePortFindings(root, filepath.Join(root, dir))...)
	}
	if len(failures) > 0 {
		fmt.Fprintln(stderr, "Port check failed:")
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %s\n", f)
		}
		return 1
	}
	fmt.Fprintln(stdout, "Port check OK: no file presents port 21120 as live")
	return 0
}

var (
	reStalePort  = regexp.MustCompile(`21120`)
	reHistorical = regexp.MustCompile(`(?i)does not exist|never shipped|HISTORICAL|histor`)
	reScanned    = regexp.MustCompile(`\.(md|js|sh|ps1|go)$`)
)

// portDocsSelf are this check's own files, which name the stale port.
var portDocsSelf = []string{
	filepath.Join("neodesk-server", "cmd", "repotool", "portdocs.go"),
	filepath.Join("neodesk-server", "cmd", "repotool", "portdocs_test.go"),
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
