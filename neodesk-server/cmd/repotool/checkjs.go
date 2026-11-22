package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// jsAllowList is where the allow list of check-js lives, from the root.
const jsAllowList = ".github/javascript-allowlist.txt"

// checkJS is "check-js": the repository keeps no JavaScript of its own.
// It lists the tracked files (git ls-files) that end in .js, .mjs or .cjs
// and fails when one is not on the allow list. It also fails when an
// entry of the list has no comment above it that says why, or matches no
// tracked file, so the list stays short and explained.
func checkJS(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: repotool check-js")
		return 2
	}
	list, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(jsAllowList)))
	if err != nil {
		fmt.Fprintf(stderr, "JavaScript check failed: %v\n", err)
		return 1
	}
	files, err := trackedFiles(root)
	if err != nil {
		fmt.Fprintf(stderr, "JavaScript check failed: %v\n", err)
		return 1
	}
	allowed, failures := jsFindings(string(list), files)
	if len(failures) > 0 {
		fmt.Fprintln(stderr, "JavaScript check failed:")
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %s\n", f)
		}
		return 1
	}
	fmt.Fprintf(stdout, "JavaScript check OK (%d allowed files)\n", allowed)
	return 0
}

// trackedFiles is what git ls-files lists in root.
func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %v: %s", err, strings.TrimSpace(errOut.String()))
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// allowEntry is a pattern of the allow list and the line it is on.
type allowEntry struct {
	pattern string
	line    int
}

// parseAllowList reads the allow list: one path or glob per line, blank
// lines and lines starting with # in between. Every entry needs a comment
// line directly above it, or above the entries right before it.
func parseAllowList(text string) ([]allowEntry, []string) {
	var entries []allowEntry
	var failures []string
	commented := false
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			commented = false
		case strings.HasPrefix(line, "#"):
			commented = true
		default:
			if !commented {
				failures = append(failures, fmt.Sprintf("%s line %d: %s has no comment above it that says why it stays JavaScript", jsAllowList, i+1, line))
			}
			if _, err := path.Match(strings.ReplaceAll(line, "**", "*"), ""); err != nil {
				failures = append(failures, fmt.Sprintf("%s line %d: %s is not a valid glob", jsAllowList, i+1, line))
				continue
			}
			entries = append(entries, allowEntry{pattern: line, line: i + 1})
		}
	}
	return entries, failures
}

// jsFindings checks the tracked files against the allow list. It returns
// how many JavaScript files the list allows, and what is wrong.
func jsFindings(list string, files []string) (int, []string) {
	entries, failures := parseAllowList(list)
	used := make([]bool, len(entries))
	allowed := 0
	var outside []string
	for _, f := range files {
		if !isJavaScript(f) {
			continue
		}
		ok := false
		for i, e := range entries {
			if globMatch(e.pattern, f) {
				used[i], ok = true, true
			}
		}
		if ok {
			allowed++
		} else {
			outside = append(outside, f)
		}
	}
	for _, f := range outside {
		failures = append(failures, f+" is JavaScript and not on the allow list ("+jsAllowList+"); write it in TypeScript or Go")
	}
	for i, e := range entries {
		if !used[i] {
			failures = append(failures, fmt.Sprintf("%s line %d: %s matches no tracked JavaScript file; remove it", jsAllowList, e.line, e.pattern))
		}
	}
	return allowed, failures
}

// isJavaScript is a file ending in .js, .mjs or .cjs, in any case
// (UPPER.JS is JavaScript too).
func isJavaScript(file string) bool {
	lower := strings.ToLower(file)
	return strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".cjs")
}

// globMatch matches a slash-separated path against a pattern whose parts
// are path.Match patterns; a part "**" matches any number of folders.
func globMatch(pattern, name string) bool {
	return matchParts(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchParts(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchParts(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}
