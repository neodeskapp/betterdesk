package main

import (
	"path/filepath"
	"testing"
)

func provenanceTree() map[string]string {
	return map[string]string{
		"THIRD_PARTY_NOTICES.md": "### Clean-room workflow\n### Release gate\nSBOM required\n",
	}
}

func TestCheckProvenancePasses(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, provenanceTree())
	runCommand(checkProvenance, root).want(t, 0, "Clean-room provenance policy check passed.\n", "")
}

func TestCheckProvenanceNeedsNoDocsFolder(t *testing.T) {
	// The register and the web remote plan left the repository for the
	// website; the check reads the policy in the notices, and a docs folder
	// that is still there is not read
	root := t.TempDir()
	tree := provenanceTree()
	tree["docs/important/support-agent-provenance.md"] = "nothing\n"
	tree["docs/features/WEB_REMOTE_CLIENT_PLAN.md"] = "Use exact same .proto files from hbb_common\n"
	writeTree(t, root, tree)
	runCommand(checkProvenance, root).want(t, 0, "Clean-room provenance policy check passed.\n", "")
}

func TestCheckProvenanceMissingNotices(t *testing.T) {
	root := t.TempDir()
	runCommand(checkProvenance, root).want(t, 1, "", "Clean-room provenance check failed:\n"+
		"- Missing provenance policy file: "+filepath.Join(root, "THIRD_PARTY_NOTICES.md")+"\n")
}

func TestCheckProvenanceFindings(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"THIRD_PARTY_NOTICES.md": "no bill\n"})
	runCommand(checkProvenance, root).want(t, 1, "", "Clean-room provenance check failed:\n"+
		"- The third-party notices must define the clean-room workflow.\n"+
		"- The third-party notices must define a release gate.\n"+
		"- Third-party notices must describe the SBOM release requirement.\n")

	// Each part is checked on its own
	writeTree(t, root, map[string]string{"THIRD_PARTY_NOTICES.md": "### Clean-room workflow\nSBOM required\n"})
	runCommand(checkProvenance, root).want(t, 1, "", "Clean-room provenance check failed:\n"+
		"- The third-party notices must define a release gate.\n")
}

func TestCheckProvenanceOnTheRepository(t *testing.T) {
	runCommand(checkProvenance, repoRoot(t)).want(t, 0, "Clean-room provenance policy check passed.\n", "")
}
