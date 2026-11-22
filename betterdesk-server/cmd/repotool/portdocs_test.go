package main

import (
	"os"
	"path/filepath"
	"testing"
)

func portTree() map[string]string {
	return map[string]string{
		"betterdesk-server/config/constants.go":         "package config\n\nconst (\n\tDefaultSignalPort = 21116\n\tDefaultRelayPort  = 21117\n\tDefaultAPIPort\t= 21114\n)\n",
		"betterdesk-server/console/config/config.go":    "cfg.APIPort = parseIntOr(getenv(\"API_PORT\"), 21121)\n",
		"docs/CONNECTION_PORTS.md":                      "| Port | Proto |\n|---|---|\n| **21116** | TCP + UDP | signal |\n| 21117 | TCP | relay |\n| 21114 | api |\n| 21121 | client |\n",
		"betterdesk-server/cmd/repotool/portdocs.go":    "the stale port 21120\n",
		"betterdesk-server/cmd/repotool/other_test.go":  "no port here\n",
		"betterdesk-server/cmd/repotool/portdocs_x.txt": "21120\n",
	}
}

func TestCheckPortDocsPasses(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, portTree())
	runCommand(checkPortDocs, root).want(t, 0, "Port docs OK (signal 21116, relay 21117, api 21114, client-api proxy 21121)\n", "")
}

func TestCheckPortDocsFindings(t *testing.T) {
	root := t.TempDir()
	tree := portTree()
	tree["betterdesk-server/config/constants.go"] = "package config\nconst DefaultSignalPort=21118\nconst DefaultAPIPort =  00021114\n"
	// The signal row may have a tab before its bar; 211170 and 21121x are no mention
	tree["docs/CONNECTION_PORTS.md"] = "| Port |\n|  **21116**\t| TCP + UDP |\n 211170 21121x\n"
	for name, text := range map[string]string{
		"docs/a.md":              "uses 21120 here",
		"docs/b.md":              "port 21120 never shipped",
		"docs/c.md":              "port 21120 is HiStOrIcAl",
		"docs/d.txt":             "21120",
		"docs/node_modules/x.md": "21120",
		"docs/private/x.md":      "21120",
		"scripts/e.sh":           "21120",
		"scripts/f.ps1":          "21120",
		"betterdesk-server/g.go": "x := 21120",
		"betterdesk-server/data/go-toolchain/h.go":        "21120",
		"betterdesk-server/sub/data/go-toolchain/i.go":    "21120",
		"betterdesk-server/j.js":                          "21120",
		"betterdesk-server/k.md":                          "211201",
		"betterdesk-server/cmd/repotool/portdocs_test.go": "21120",
	} {
		tree[name] = text
	}
	writeTree(t, root, tree)
	// A link is not followed
	if err := os.Symlink(filepath.Join("..", "docs", "a.md"), filepath.Join(root, "betterdesk-server", "link.md")); err != nil {
		t.Logf("no symlink: %v", err)
	}
	runCommand(checkPortDocs, root).want(t, 1, "", "Port documentation check failed:\n"+
		"  - constants.go: DefaultRelayPort not found\n"+
		"  - docs/CONNECTION_PORTS.md does not mention signal port 21118 declared in code\n"+
		"  - docs/CONNECTION_PORTS.md does not mention api port 21114 declared in code\n"+
		"  - docs/CONNECTION_PORTS.md does not mention clientApi port 21121 declared in code\n"+
		"  - docs/CONNECTION_PORTS.md signal row says 21116, code says 21118\n"+
		"  - "+filepath.Join("docs", "a.md")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("betterdesk-server", "g.go")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("betterdesk-server", "j.js")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("betterdesk-server", "k.md")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("scripts", "e.sh")+" mentions port 21120, which does not exist in the code\n"+
		"  - "+filepath.Join("scripts", "f.ps1")+" mentions port 21120, which does not exist in the code\n")
}

func TestCheckPortDocsUnreadableFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"docs/CONNECTION_PORTS.md": "| **21116** | TCP + UDP |\n"})
	constants := filepath.Join(root, "betterdesk-server", "config", "constants.go")
	config := filepath.Join(root, "betterdesk-server", "console", "config", "config.go")
	runCommand(checkPortDocs, root).want(t, 1, "", "Port documentation check failed:\n"+
		"  - cannot read "+filepath.Join("betterdesk-server", "config", "constants.go")+": ENOENT: no such file or directory, open '"+constants+"'\n"+
		"  - cannot read "+filepath.Join("betterdesk-server", "console", "config", "config.go")+": ENOENT: no such file or directory, open '"+config+"'\n"+
		"  - constants.go: DefaultSignalPort not found\n"+
		"  - constants.go: DefaultRelayPort not found\n"+
		"  - constants.go: DefaultAPIPort not found\n"+
		"  - console/config/config.go: the API_PORT default not found\n"+
		"  - docs/CONNECTION_PORTS.md signal row says 21116, code says null\n")

	root = t.TempDir()
	tree := portTree()
	delete(tree, "docs/CONNECTION_PORTS.md")
	writeTree(t, root, tree)
	doc := filepath.Join(root, "docs", "CONNECTION_PORTS.md")
	runCommand(checkPortDocs, root).want(t, 1, "", "Port documentation check failed:\n"+
		"  - cannot read "+filepath.Join("docs", "CONNECTION_PORTS.md")+": ENOENT: no such file or directory, open '"+doc+"'\n")

	if err := os.MkdirAll(doc, 0o755); err != nil {
		t.Fatal(err)
	}
	runCommand(checkPortDocs, root).want(t, 1, "", "Port documentation check failed:\n"+
		"  - cannot read "+filepath.Join("docs", "CONNECTION_PORTS.md")+": EISDIR: illegal operation on a directory, read\n")
}

func TestCheckPortDocsDocument(t *testing.T) {
	root := t.TempDir()
	tree := portTree()
	// An empty document is not checked
	tree["docs/CONNECTION_PORTS.md"] = ""
	writeTree(t, root, tree)
	runCommand(checkPortDocs, root).want(t, 0, "Port docs OK (signal 21116, relay 21117, api 21114, client-api proxy 21121)\n", "")

	writeTree(t, root, map[string]string{"docs/CONNECTION_PORTS.md": "21116 21117 21114 21121\n|**21116**|TCP|\n"})
	runCommand(checkPortDocs, root).want(t, 1, "", "Port documentation check failed:\n"+
		"  - docs/CONNECTION_PORTS.md: signal row (TCP + UDP) not found\n")

	// Tabs and no-break spaces are spaces in the signal row
	nbsp := string(rune(0xA0))
	for _, space := range []string{"\t", nbsp} {
		row := "|" + space + "**21116**" + space + "|" + space + "TCP + UDP |\n21117 21114 21121\n"
		writeTree(t, root, map[string]string{"docs/CONNECTION_PORTS.md": row})
		runCommand(checkPortDocs, root).want(t, 0, "Port docs OK (signal 21116, relay 21117, api 21114, client-api proxy 21121)\n", "")
	}
}
