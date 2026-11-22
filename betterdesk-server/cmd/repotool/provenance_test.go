package main

import (
	"path/filepath"
	"testing"
)

func provenanceTree() map[string]string {
	return map[string]string{
		"docs/important/support-agent-provenance.md": "## Clean-room workflow and Release gate\n",
		"docs/features/WEB_REMOTE_CLIENT_PLAN.md":    "plan\n",
		".github/go-server-context.md":               "context\n",
		"THIRD_PARTY_NOTICES.md":                     "SBOM required\n",
	}
}

func TestCheckProvenancePasses(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, provenanceTree())
	runCommand(checkProvenance, root).want(t, 0, "Clean-room provenance policy check passed.\n", "")
}

func TestCheckProvenanceMissingFiles(t *testing.T) {
	root := t.TempDir()
	runCommand(checkProvenance, root).want(t, 1, "", "Clean-room provenance check failed:\n"+
		"- Missing provenance policy file: "+filepath.Join(root, "docs", "important", "support-agent-provenance.md")+"\n"+
		"- Missing provenance policy file: "+filepath.Join(root, "docs", "features", "WEB_REMOTE_CLIENT_PLAN.md")+"\n"+
		"- Missing provenance policy file: "+filepath.Join(root, ".github", "go-server-context.md")+"\n"+
		"- Missing provenance policy file: "+filepath.Join(root, "THIRD_PARTY_NOTICES.md")+"\n")

	// One missing file is reported before any content is read
	root = t.TempDir()
	tree := provenanceTree()
	delete(tree, "THIRD_PARTY_NOTICES.md")
	tree["docs/important/support-agent-provenance.md"] = "nothing\n"
	writeTree(t, root, tree)
	runCommand(checkProvenance, root).want(t, 1, "", "Clean-room provenance check failed:\n"+
		"- Missing provenance policy file: "+filepath.Join(root, "THIRD_PARTY_NOTICES.md")+"\n")
}

func TestCheckProvenanceFindings(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, provenanceTree())
	writeTree(t, root, map[string]string{
		"docs/important/support-agent-provenance.md": "nothing\n",
		"THIRD_PARTY_NOTICES.md":                     "no bill\n",
		// Matched without regard to case
		"docs/features/WEB_REMOTE_CLIENT_PLAN.md": "COPY `message.proto` and `rendezvous.proto` from `hbb_common/protos`\n" +
			"use exact same .proto files from hbb_common\n",
		".github/go-server-context.md": "The .proto files we use have **No Copyright Headers**\n",
	})
	runCommand(checkProvenance, root).want(t, 1, "", "Clean-room provenance check failed:\n"+
		"- The provenance register must define the clean-room workflow.\n"+
		"- The provenance register must define a release gate.\n"+
		"- Third-party notices must describe the SBOM release requirement.\n"+
		"- The web remote plan still instructs contributors to copy external protocol schemas.\n"+
		"- The web remote plan still requires external protocol schema copies.\n"+
		"- The Go server context still makes an unverified provenance claim.\n")
}

func TestCheckProvenanceClaimStaysOnOneLine(t *testing.T) {
	// The claim's parts are on one line: a carriage return ends it, as for
	// "." in JavaScript
	root := t.TempDir()
	writeTree(t, root, provenanceTree())
	writeTree(t, root, map[string]string{".github/go-server-context.md": ".proto files x\rhave **no copyright headers**\n"})
	runCommand(checkProvenance, root).want(t, 0, "Clean-room provenance policy check passed.\n", "")

	writeTree(t, root, map[string]string{".github/go-server-context.md": ".proto files x have **no copyright headers**\n"})
	if r := runCommand(checkProvenance, root); r.code != 1 {
		t.Errorf("claim on one line: exit %d, want 1", r.code)
	}
}

func TestCheckProvenanceOnTheRepository(t *testing.T) {
	runCommand(checkProvenance, repoRoot(t)).want(t, 0, "Clean-room provenance policy check passed.\n", "")
}
