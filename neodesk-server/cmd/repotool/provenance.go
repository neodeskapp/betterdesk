package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// checkProvenance is "check-provenance": a guard for the clean-room
// provenance process. It checks the policy the repository keeps (the
// full register is on the website, neode.sk/docs/development/provenance):
// the third-party notices define the clean-room workflow, the release gate
// and the SBOM requirement. It cannot prove independent authorship and does
// not replace a legal review.
func checkProvenance(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: repotool check-provenance")
		return 2
	}
	notices := filepath.Join(root, "THIRD_PARTY_NOTICES.md")

	var failures []string
	if _, err := os.Stat(notices); err != nil {
		failures = append(failures, "Missing provenance policy file: "+notices)
	} else {
		text, err := readText(notices)
		if err != nil {
			fmt.Fprintf(stderr, "Clean-room provenance check failed: %s\n", readError(notices, err))
			return 1
		}
		failures = provenanceFindings(text)
	}
	if len(failures) > 0 {
		fmt.Fprintf(stderr, "Clean-room provenance check failed:\n- %s\n", strings.Join(failures, "\n- "))
		return 1
	}
	fmt.Fprintln(stdout, "Clean-room provenance policy check passed.")
	return 0
}

func provenanceFindings(notices string) []string {
	var failures []string
	if !strings.Contains(notices, "Clean-room workflow") {
		failures = append(failures, "The third-party notices must define the clean-room workflow.")
	}
	if !strings.Contains(notices, "Release gate") {
		failures = append(failures, "The third-party notices must define a release gate.")
	}
	if !strings.Contains(notices, "SBOM") {
		failures = append(failures, "Third-party notices must describe the SBOM release requirement.")
	}
	return failures
}
