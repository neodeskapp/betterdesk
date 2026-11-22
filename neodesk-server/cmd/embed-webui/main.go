// Command embed-webui prepares the files the server binary embeds
// (DECISIONS A25). Run it in betterdesk-server after "npm run build" in
// web-ui and before go build:
//
//	go run ./cmd/embed-webui              # from ../web-ui/build
//	go run ./cmd/embed-webui -from DIR    # from another build
//
// It copies the web app's build into console/webui/dist, replacing what
// dist holds (git ignores it, except the placeholder that lets go build
// work without this step), and records the modification time of every
// file. Then it records the times of the console's own files in
// console/static/files, which are embedded where they are. The binary
// then answers with the same Last-Modified and ETag as the files on disk.
// A binary built without this step has no web app (WEB_UI_DIR then names
// a build on disk), and its console files have the time 1970-01-01.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/embedfs"
)

func main() {
	from := flag.String("from", filepath.Join("..", "web-ui", "build"), "the build of the web app")
	to := flag.String("to", filepath.Join("console", "webui", "dist"), "the folder the binary embeds the web app from")
	static := flag.String("static", filepath.Join("console", "static", "files"), "the console's own files")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/embed-webui [-from DIR] [-to DIR] [-static DIR]")
		os.Exit(2)
	}
	if err := run(*from, *to, *static); err != nil {
		fmt.Fprintf(os.Stderr, "embed-webui: %v\n", err)
		os.Exit(1)
	}
}

func run(from, to, static string) error {
	if _, err := os.Stat(filepath.Join(from, "index.html")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s has no index.html: build the web app first (npm run build in web-ui)", from)
		}
		return err
	}
	for _, dir := range []string{filepath.Dir(to), static} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("%s does not exist: run this in betterdesk-server", dir)
		}
	}
	files, size, err := embedfs.Copy(from, to)
	if err != nil {
		return err
	}
	fmt.Printf("embed-webui: %d files (%.1f MB) from %s in %s\n", files, float64(size)/(1<<20), from, to)
	files, size, err = embedfs.Stamp(static)
	if err != nil {
		return err
	}
	fmt.Printf("embed-webui: the times of %d files (%.1f MB) in %s\n", files, float64(size)/(1<<20), static)
	return nil
}
