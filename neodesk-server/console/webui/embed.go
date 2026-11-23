package webui

import (
	"embed"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/embedfs"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// dist is the build of the web app the binary carries: cmd/embed-webui
// copies web-ui/build here before go build. Without that step it holds
// only the placeholder, and the binary has no build.
//
//go:embed all:dist
var dist embed.FS

// embeddedDir is the name the build in the binary goes by in a Host: file
// names are made from it as from a build directory on disk, and the
// detail of an error answer outside production shows it. It is where the
// old Docker image kept the build.
var embeddedDir = filepath.FromSlash("/web-ui/build")

// embedded reads the build in the binary once. Every file has the
// modification time it had when cmd/embed-webui copied it.
var embedded = sync.OnceValues(func() (*embedfs.FS, error) {
	return embedfs.New(dist, "dist", time.Unix(0, 0))
})

// HasEmbeddedBuild reports whether the binary carries a build of the web
// app.
func HasEmbeddedBuild() bool {
	build, err := embedded()
	return err == nil && !build.Empty()
}

// newEmbedded makes a Host for an embedded build. A build that cannot be
// read is no build, and an empty one is answered like a build directory
// that does not exist.
func newEmbedded(opts Options, build *embedfs.FS, err error) *Host {
	if err != nil {
		logging.Logf("panel", "web UI: the build in the binary cannot be read (%v); the console has no web app", err)
		return newHost(opts, embeddedDir, noFiles{})
	}
	if build.Empty() {
		return newHost(opts, embeddedDir, noFiles{})
	}
	return newHost(opts, embeddedDir, embeddedFiles{root: embeddedDir, build: build})
}

// embeddedFiles are the files of an embedded build under the names a
// Host makes for them: root, or root and a path below it.
type embeddedFiles struct {
	root  string
	build *embedfs.FS
}

// rel is the slash-separated name in the build of a Host's file name.
func (e embeddedFiles) rel(op, name string) (string, error) {
	if name == e.root {
		return ".", nil
	}
	rest, ok := strings.CutPrefix(name, e.root+string(filepath.Separator))
	if !ok || !fs.ValidPath(filepath.ToSlash(rest)) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return filepath.ToSlash(rest), nil
}

func (e embeddedFiles) Stat(name string) (fs.FileInfo, error) {
	rel, err := e.rel("stat", name)
	if err != nil {
		return nil, err
	}
	return e.build.Stat(rel)
}

func (e embeddedFiles) Open(name string) (io.ReadSeekCloser, error) {
	rel, err := e.rel("open", name)
	if err != nil {
		return nil, err
	}
	file, err := e.build.Open(rel)
	if err != nil {
		return nil, err
	}
	seekable, ok := file.(io.ReadSeekCloser)
	if !ok {
		_ = file.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return seekable, nil
}

func (e embeddedFiles) ReadFile(name string) ([]byte, error) {
	rel, err := e.rel("read", name)
	if err != nil {
		return nil, err
	}
	return e.build.ReadFile(rel)
}
