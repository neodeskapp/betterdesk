package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// checkProvenance is "check-provenance": a guard for the clean-room
// provenance process. It checks only the repository's policy documents:
// they exist, the register defines the clean-room workflow and the
// release gate, the notices describe the SBOM requirement, and no document
// tells contributors to copy outside protocol schemas or claims they carry
// no copyright headers. It cannot prove independent authorship and does
// not replace a legal review.
func checkProvenance(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: repotool check-provenance")
		return 2
	}
	register := filepath.Join(root, "docs", "important", "support-agent-provenance.md")
	webRemotePlan := filepath.Join(root, "docs", "features", "WEB_REMOTE_CLIENT_PLAN.md")
	serverContext := filepath.Join(root, ".github", "go-server-context.md")
	notices := filepath.Join(root, "THIRD_PARTY_NOTICES.md")

	var failures []string
	for _, file := range []string{register, webRemotePlan, serverContext, notices} {
		if _, err := os.Stat(file); err != nil {
			failures = append(failures, "Missing provenance policy file: "+file)
		}
	}
	if len(failures) == 0 {
		texts := map[string]string{}
		for _, file := range []string{register, webRemotePlan, serverContext, notices} {
			text, err := readText(file)
			if err != nil {
				fmt.Fprintf(stderr, "Clean-room provenance check failed: %s\n", readError(file, err))
				return 1
			}
			texts[file] = text
		}
		failures = provenanceFindings(texts[register], texts[webRemotePlan], texts[serverContext], texts[notices])
	}
	if len(failures) > 0 {
		fmt.Fprintf(stderr, "Clean-room provenance check failed:\n- %s\n", strings.Join(failures, "\n- "))
		return 1
	}
	fmt.Fprintln(stdout, "Clean-room provenance policy check passed.")
	return 0
}

var (
	reCopySchemas    = regexp.MustCompile("(?i)Copy `message\\.proto` and `rendezvous\\.proto` from `hbb_common/protos`")
	reSameProtoFiles = regexp.MustCompile(`(?i)Use exact same \.proto files from hbb_common`)
	reNoCopyright    = regexp.MustCompile(`(?i)\.proto files ` + jsDot + `*have \*\*no copyright headers\*\*`)
)

func provenanceFindings(register, webRemote, serverContext, notices string) []string {
	var failures []string
	if !strings.Contains(register, "Clean-room workflow") {
		failures = append(failures, "The provenance register must define the clean-room workflow.")
	}
	if !strings.Contains(register, "Release gate") {
		failures = append(failures, "The provenance register must define a release gate.")
	}
	if !strings.Contains(notices, "SBOM") {
		failures = append(failures, "Third-party notices must describe the SBOM release requirement.")
	}
	if reCopySchemas.MatchString(webRemote) {
		failures = append(failures, "The web remote plan still instructs contributors to copy external protocol schemas.")
	}
	if reSameProtoFiles.MatchString(webRemote) {
		failures = append(failures, "The web remote plan still requires external protocol schema copies.")
	}
	if reNoCopyright.MatchString(serverContext) {
		failures = append(failures, "The Go server context still makes an unverified provenance claim.")
	}
	return failures
}
