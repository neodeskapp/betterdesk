package static

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/embedfs"
)

// The tree in the binary is the committed folder: the same files with the
// same bytes, and nothing else.
func TestFilesAreTheCommittedFolder(t *testing.T) {
	tree, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	var inBinary, onDisk []string
	err = fs.WalkDir(tree, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		inBinary = append(inBinary, name)
		embedded, err := fs.ReadFile(tree, name)
		if err != nil {
			return err
		}
		committed, err := os.ReadFile(filepath.Join("files", filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if !bytes.Equal(embedded, committed) {
			t.Errorf("%s in the binary differs from the committed file", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir("files", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("files", name)
		if rel != embedfs.StampFile {
			onDisk = append(onDisk, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(inBinary, onDisk) {
		t.Errorf("files in the binary %v, committed %v", inBinary, onDisk)
	}
	for _, want := range []string{"lang/en.json", "public/favicon.svg", "protos/rendezvous.proto", "themes/default.json", "config/theme-palettes.json", "config/supporters.json"} {
		if !slices.Contains(inBinary, want) {
			t.Errorf("%s is not in the binary", want)
		}
	}
	if _, err := fs.Stat(tree, embedfs.StampFile); err == nil {
		t.Error("the stamp is shown as a file")
	}
}

// The protocol files the browser loads are the server's own, byte for
// byte: protos/ of the server is the source.
func TestProtosAreTheServers(t *testing.T) {
	served, err := filepath.Glob(filepath.Join("files", "protos", "*.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if len(served) != 2 {
		t.Fatalf("served protocol files: %v", served)
	}
	for _, file := range served {
		source := filepath.Join("..", "..", "protos", filepath.Base(file))
		want, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from %s: copy the server's file over it", file, source)
		}
	}
}
