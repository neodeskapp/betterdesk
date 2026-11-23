// Command clidocs writes the reference of the neodesk command line as
// Markdown, from the same command table the command line runs on
// (ndcli/commands.json):
//
//	go run ./cmd/clidocs -out ../docs/cli
//
// It writes index.md (usage, sign-in, the groups, the flags every command
// has, the environment, the exit codes), one <group>.md per group with
// every command, excluded.md with the routes the command line leaves out
// and why, and commands.json, what `neodesk describe` prints. The website
// takes the pages from there.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/neodeskapp/neodesk/neodesk-server/ndcli"
)

func main() {
	out := flag.String("out", "cli-docs", "the folder to write the pages to")
	version := flag.String("version", "", "the version to name in commands.json")
	flag.Parse()
	if err := write(*out, *version); err != nil {
		fmt.Fprintln(os.Stderr, "clidocs:", err)
		os.Exit(1)
	}
}

func write(dir, version string) error {
	t, err := ndcli.Load()
	if err != nil {
		return err
	}
	d := t.Describe(version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := map[string]*bytes.Buffer{}
	page := func(name string) *bytes.Buffer {
		b := &bytes.Buffer{}
		files[name] = b
		return b
	}
	ndcli.WriteIndexMarkdown(page("index.md"), d, "")
	fmt.Fprintf(files["index.md"], "\nThe routes the command line leaves out are in [excluded.md](excluded.md).\n")
	for _, g := range d.Groups {
		ndcli.WriteGroupMarkdown(page(g.Name+".md"), g)
	}
	ndcli.WriteExcludedMarkdown(page("excluded.md"), d.Excluded)
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	page("commands.json").Write(append(raw, '\n'))
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %d files to %s: %d commands in %d groups, %d routes left out\n",
		len(files), dir, d.Counts["commands"], d.Counts["groups"], d.Counts["excluded"])
	return nil
}
