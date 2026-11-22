package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// versionTree is a repository whose version files all name version, in
// the shapes the real files have.
func versionTree(v string) map[string]string {
	return map[string]string{
		"VERSION":                   v + "\n",
		"betterdesk.sh":             "#!/bin/bash\n#   BetterDesk Console Manager v" + v + "\nVERSION=\"" + v + "\"\necho \"BetterDesk Console Manager v$VERSION\"\n",
		"betterdesk.ps1":            "<#\n    BetterDesk Console Manager v" + v + " - All-in-One\n#>\n$script:VERSION = \"" + v + "\"\n",
		"betterdesk-docker.sh":      "#   BetterDesk Console Manager v" + v + " (Docker)\nVERSION=\"" + v + "\"\n",
		"Dockerfile":                "FROM alpine\nLABEL version=\"" + v + "\"\n",
		"Dockerfile.server":         "FROM alpine\nLABEL version=\"" + v + "\"\n",
		"install.sh":                "#   --version TAG   Docker image tag / release baseline (default: " + v + ")\nBETTERDESK_VERSION=\"${BETTERDESK_VERSION:-" + v + "}\"\n",
		"docker/entrypoint.sh":      "echo \"  Version: ${BETTERDESK_IMAGE_VERSION:-" + v + "}\"\n",
		"betterdesk-server/VERSION": v + "\n",
		"betterdesk-server/internal/productversion/VERSION": v + "\n",
		"CHANGELOG.md": "## [Unreleased]\n\n### Changed\n- _(none yet)_\n\n---\n\n## [" + v + "] — 2026-11-21\n\n- x\n\n" +
			"[" + v + "]: https://github.com/trybetterdesk/betterdesk/compare/v3.5.269...v" + v + "\n",
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name := range versionTree("") {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err == nil {
			out[name] = string(data)
		}
	}
	return out
}

// Half past midnight in Berlin is still the day before in UTC
var bumpDay = time.Date(2026, 11, 23, 0, 30, 0, 0, time.FixedZone("CET", 3600))

// runBump runs bump-version on root with a fixed clock and environment.
func runBump(root string, env map[string]string, args ...string) result {
	var out, errOut bytes.Buffer
	b := versionBump{root: root, stdout: &out, stderr: &errOut, now: func() time.Time { return bumpDay }, getenv: func(k string) string { return env[k] }}
	code, err := b.run(args)
	if err != nil {
		errOut.WriteString("Error: " + err.Error() + "\n")
		code = 1
	}
	return result{code, out.String(), errOut.String()}
}

func TestBumpVersionVerify(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, versionTree("3.5.270"))
	runBump(root, nil, "--verify").want(t, 0, "All Tier 1/2 version files match VERSION (3.5.270).\n", "")
	runCommand(bumpVersion, root, "--verify").want(t, 0, "All Tier 1/2 version files match VERSION (3.5.270).\n", "")

	writeTree(t, root, map[string]string{
		"Dockerfile":                "LABEL version=\"3.5.1\"\n",
		"betterdesk.ps1":            "$script:VERSION = \"3.5.2\"\n",
		"betterdesk-docker.sh":      "  VERSION=\"3.5.3\"\n",
		"betterdesk-server/VERSION": "  3.5.270 \n",
		// README.md and the quick-start compose files are not version files
		"README.md":                       "img.shields.io/badge/version-1.0.0-blue",
		"docker-compose.quick.single.yml": "image: x:${BETTERDESK_IMAGE_TAG:-latest}\n",
	})
	if err := os.Remove(filepath.Join(root, "install.sh")); err != nil {
		t.Fatal(err)
	}
	runBump(root, nil, "--verify").want(t, 1, "", "Version mismatch detected (canonical VERSION = 3.5.270):\n"+
		"  - betterdesk.ps1: expected 3.5.270, got 3.5.2\n"+
		"  - betterdesk-docker.sh: expected 3.5.270, got (not found)\n"+
		"  - Dockerfile: expected 3.5.270, got 3.5.1\n"+
		"  - install.sh: expected 3.5.270, got (missing file)\n")
}

func TestBumpVersionPatch(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, versionTree("3.5.270"))
	runBump(root, nil, "--patch").want(t, 0, "Version 3.5.270 → 3.5.271\nUpdated files:\n"+
		"  - VERSION\n  - betterdesk.sh\n  - betterdesk.ps1\n  - betterdesk-docker.sh\n  - Dockerfile\n  - Dockerfile.server\n"+
		"  - install.sh\n  - docker/entrypoint.sh\n  - betterdesk-server/VERSION\n  - betterdesk-server/internal/productversion/VERSION\n"+
		"  - CHANGELOG.md\n", "")
	got := readTree(t, root)
	want := versionTree("3.5.271")
	// The changelog's new section has today's date (UTC) and the compare link
	want["CHANGELOG.md"] = "## [Unreleased]\n\n### Changed\n- _(none yet)_\n\n---\n\n" +
		"## [3.5.271] — 2026-11-22\n\n### Changed\n- _(none yet)_\n\n---\n\n## [3.5.270] — 2026-11-21\n\n- x\n\n" +
		"[3.5.270]: https://github.com/trybetterdesk/betterdesk/compare/v3.5.269...v3.5.270\n" +
		"[3.5.271]: https://github.com/trybetterdesk/betterdesk/compare/v3.5.270...v3.5.271\n"
	for name, text := range want {
		if got[name] != text {
			t.Errorf("%s:\n%q\nwant:\n%q", name, got[name], text)
		}
	}
	runBump(root, nil, "--verify").want(t, 0, "All Tier 1/2 version files match VERSION (3.5.271).\n", "")
}

func TestBumpVersionMinorInAFork(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, versionTree("3.5.270"))
	r := runBump(root, map[string]string{"GITHUB_REPOSITORY": "someone/fork"}, "--minor")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "Version 3.5.270 → 3.6.0\n") {
		t.Fatalf("got %+v", r)
	}
	if got := readTree(t, root)["CHANGELOG.md"]; !strings.HasSuffix(got, "\n[3.6.0]: https://github.com/someone/fork/compare/v3.5.270...v3.6.0\n") {
		t.Errorf("changelog:\n%s", got)
	}
}

func TestBumpVersionManagerScripts(t *testing.T) {
	root := t.TempDir()
	tree := versionTree("3.5.270")
	// Every version line and banner of a manager script changes; only the
	// first label, default and image version of the others do
	tree["betterdesk.sh"] = "VERSION=\"3.5.270\"\nVERSION=\"3.5.270\"\n# BetterDesk Console Manager v3.5.270 x Console Manager v3.5.270\n"
	tree["Dockerfile"] = "LABEL version=\"3.5.270\"\nLABEL version=\"3.5.270\"\n"
	tree["install.sh"] = "#   --version TAG  Docker image tag / release baseline (default: 3.5.270)\nBETTERDESK_VERSION=\"${BETTERDESK_VERSION:-3.5.270}\"\nBETTERDESK_VERSION=\"${BETTERDESK_VERSION:-3.5.270}\"\n"
	tree["docker/entrypoint.sh"] = "a ${BETTERDESK_IMAGE_VERSION:-3.5.270} b ${BETTERDESK_IMAGE_VERSION:-3.5.270}\n"
	writeTree(t, root, tree)
	if r := runBump(root, nil, "--set", "4.0.0"); r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	got := readTree(t, root)
	for name, want := range map[string]string{
		"betterdesk.sh":        "VERSION=\"4.0.0\"\nVERSION=\"4.0.0\"\n# BetterDesk Console Manager v4.0.0 x Console Manager v3.5.270\n",
		"Dockerfile":           "LABEL version=\"4.0.0\"\nLABEL version=\"3.5.270\"\n",
		"install.sh":           "#   --version TAG  Docker image tag / release baseline (default: 4.0.0)\nBETTERDESK_VERSION=\"${BETTERDESK_VERSION:-4.0.0}\"\nBETTERDESK_VERSION=\"${BETTERDESK_VERSION:-3.5.270}\"\n",
		"docker/entrypoint.sh": "a ${BETTERDESK_IMAGE_VERSION:-4.0.0} b ${BETTERDESK_IMAGE_VERSION:-3.5.270}\n",
	} {
		if got[name] != want {
			t.Errorf("%s:\n%q\nwant:\n%q", name, got[name], want)
		}
	}
}

func TestBumpVersionSync(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, versionTree("3.5.270"))
	runBump(root, nil, "--sync").want(t, 0, "All Tier 1/2 files already match VERSION (3.5.270).\n", "")

	writeTree(t, root, map[string]string{"Dockerfile": "LABEL version=\"3.5.1\"\n", "betterdesk-server/VERSION": "  3.5.270 \n"})
	runBump(root, nil, "--sync", "--dry-run").want(t, 0, "[dry-run] Synced drifted files to VERSION (3.5.270):\n  - Dockerfile\n  - betterdesk-server/VERSION\n", "")
	if got := readTree(t, root)["Dockerfile"]; got != "LABEL version=\"3.5.1\"\n" {
		t.Errorf("a dry run wrote %q", got)
	}
	runBump(root, nil, "--sync").want(t, 0, "Synced drifted files to VERSION (3.5.270):\n  - Dockerfile\n  - betterdesk-server/VERSION\n", "")
	got := readTree(t, root)
	if got["Dockerfile"] != "LABEL version=\"3.5.270\"\n" || got["betterdesk-server/VERSION"] != "3.5.270\n" || got["CHANGELOG.md"] != versionTree("3.5.270")["CHANGELOG.md"] {
		t.Errorf("synced: %q %q", got["Dockerfile"], got["betterdesk-server/VERSION"])
	}
}

func TestBumpVersionArguments(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, versionTree("3.5.270"))
	runBump(root, nil, "--list-paths", "--verify").want(t, 0, "VERSION\nbetterdesk.sh\nbetterdesk.ps1\nbetterdesk-docker.sh\nDockerfile\nDockerfile.server\n"+
		"install.sh\ndocker/entrypoint.sh\nbetterdesk-server/VERSION\nbetterdesk-server/internal/productversion/VERSION\nCHANGELOG.md\n", "")
	runBump(root, nil, "--set", "4.0").want(t, 1, "", "Invalid --set version (expected X.Y.Z): 4.0\n")
	runBump(root, nil, "--set").want(t, 1, bumpUsage, "")
	runBump(root, nil).want(t, 1, bumpUsage, "")
	runBump(root, nil, "--fast").want(t, 1, bumpUsage, "")
	// --patch goes before --minor and --set
	if r := runBump(root, nil, "--dry-run", "--set", "9.9.9", "--minor", "--patch"); !strings.HasPrefix(r.stdout, "[dry-run] Version 3.5.270 → 3.5.271\n") {
		t.Errorf("got %+v", r)
	}
	runBump(root, nil, "--set", " 3.5.270 ").want(t, 0, "Version already 3.5.270; nothing to bump.\nVersion 3.5.270 → 3.5.270\nNo file changes required.\n", "")
	if got := readTree(t, root); got["VERSION"] != "3.5.270\n" || got["CHANGELOG.md"] != versionTree("3.5.270")["CHANGELOG.md"] {
		t.Error("a dry run or a bump to the same version wrote files")
	}
}

func TestBumpVersionChangelog(t *testing.T) {
	for name, c := range map[string]struct{ before, after string }{
		"no unreleased section": {
			"# Changelog\n\n## [3.5.270] — 2026-11-21\n- x\n\n\n  \n",
			"## [Unreleased]\n\n### Changed\n- _(none yet)_\n\n---\n\n## [3.5.271] — 2026-11-22\n\n### Changed\n- Version bump to 3.5.271.\n\n---\n\n# Changelog\n\n## [3.5.270] — 2026-11-21\n- x\n" +
				"[3.5.271]: https://github.com/trybetterdesk/betterdesk/compare/v3.5.270...v3.5.271\n",
		},
		"the section exists": {
			"## [Unreleased]\n\n## [3.5.271] — 2026-11-22\n",
			"## [Unreleased]\n\n## [3.5.271] — 2026-11-22\n",
		},
		"the link exists, two unreleased sections": {
			"## [Unreleased]\n\n- a\n\n## [Unreleased]\n\n[3.5.271]: https://example.test\n",
			"## [Unreleased]\n\n### Changed\n- _(none yet)_\n\n---\n\n## [3.5.271] — 2026-11-22\n\n- a\n\n## [Unreleased]\n\n[3.5.271]: https://example.test\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			tree := versionTree("3.5.270")
			tree["CHANGELOG.md"] = c.before
			writeTree(t, root, tree)
			r := runBump(root, nil, "--patch")
			if got := readTree(t, root)["CHANGELOG.md"]; got != c.after {
				t.Errorf("changelog:\n%q\nwant:\n%q", got, c.after)
			}
			if listed := strings.Contains(r.stdout, "  - CHANGELOG.md\n"); listed != (c.before != c.after) {
				t.Errorf("CHANGELOG.md listed %v:\n%s", listed, r.stdout)
			}
		})
	}

	// Without a changelog the files are bumped all the same
	root := t.TempDir()
	tree := versionTree("3.5.270")
	delete(tree, "CHANGELOG.md")
	writeTree(t, root, tree)
	if r := runBump(root, nil, "--patch"); r.code != 0 || strings.Contains(r.stdout, "CHANGELOG") || readTree(t, root)["VERSION"] != "3.5.271\n" {
		t.Errorf("got %+v", r)
	}
}

func TestBumpVersionNeedsAVersionFile(t *testing.T) {
	root := t.TempDir()
	runCommand(bumpVersion, root, "--verify").want(t, 1, "", "Error: VERSION file not found\n")
	writeTree(t, root, map[string]string{"VERSION": "v3\n"})
	runCommand(bumpVersion, root, "--patch").want(t, 1, "", "Error: Invalid VERSION file content: v3\n")
	runCommand(bumpVersion, root, "--list-paths").want(t, 0, strings.Join(append(rulePaths(), "CHANGELOG.md"), "\n")+"\n", "")
}

func rulePaths() []string {
	var out []string
	for _, r := range versionRules {
		out = append(out, r.path)
	}
	return out
}

func TestBumpVersionOnTheRepository(t *testing.T) {
	r := runCommand(bumpVersion, repoRoot(t), "--verify")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "All Tier 1/2 version files match VERSION (") {
		t.Errorf("got %+v", r)
	}
}
