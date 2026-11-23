package main

import (
	"os"
	"path/filepath"
	"testing"
)

func portTree() map[string]string {
	return map[string]string{
		"neodesk-server/config/constants.go":         "package config\n\nconst (\n\tDefaultSignalPort = 21116\n\tDefaultRelayPort  = 21117\n\tDefaultAPIPort\t= 21114\n)\n",
		"neodesk-server/cmd/repotool/portdocs.go":    "the stale port 21120\n",
		"neodesk-server/cmd/repotool/other_test.go":  "no port here\n",
		"neodesk-server/cmd/repotool/portdocs_x.txt": "21120\n",
		"scripts/a.sh": "echo 21116\n",
	}
}

func TestCheckPortDocsPasses(t *testing.T) {
	// The port table is on the website, so no docs/CONNECTION_PORTS.md is
	// needed and a docs folder is not read
	root := t.TempDir()
	tree := portTree()
	tree["docs/a.md"] = "uses 21120 here"
	writeTree(t, root, tree)
	runCommand(checkPortDocs, root).want(t, 0, "Port check OK: no file presents port 21120 as live\n", "")
}

func TestCheckPortDocsFindings(t *testing.T) {
	root := t.TempDir()
	tree := portTree()
	for name, text := range map[string]string{
		"scripts/b.md":                                 "port 21120 never shipped",
		"scripts/c.md":                                 "port 21120 is HiStOrIcAl",
		"scripts/d.txt":                                "21120",
		"scripts/node_modules/x.md":                    "21120",
		"scripts/private/x.md":                         "21120",
		"scripts/e.sh":                                 "21120",
		"scripts/f.ps1":                                "21120",
		"neodesk-server/g.go":                          "x := 21120",
		"neodesk-server/data/go-toolchain/h.go":        "21120",
		"neodesk-server/sub/data/go-toolchain/i.go":    "21120",
		"neodesk-server/j.js":                          "21120",
		"neodesk-server/k.md":                          "211201",
		"neodesk-server/cmd/repotool/portdocs_test.go": "21120",
	} {
		tree[name] = text
	}
	writeTree(t, root, tree)
	// A link is not followed
	if err := os.Symlink(filepath.Join("..", "scripts", "e.sh"), filepath.Join(root, "neodesk-server", "link.md")); err != nil {
		t.Logf("no symlink: %v", err)
	}
	runCommand(checkPortDocs, root).want(t, 1, "", "Port check failed:\n"+
		"  - "+filepath.Join("neodesk-server", "g.go")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("neodesk-server", "j.js")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("neodesk-server", "k.md")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("scripts", "e.sh")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("scripts", "f.ps1")+" mentions port 21120, which does not exist in the code\n")
}

func TestCheckPortDocsUsage(t *testing.T) {
	runCommand(checkPortDocs, t.TempDir(), "extra").want(t, 2, "", "usage: repotool check-port-docs\n")
}

func TestCheckPortDocsOnTheRepository(t *testing.T) {
	runCommand(checkPortDocs, repoRoot(t)).want(t, 0, "Port check OK: no file presents port 21120 as live\n", "")
}
