// Package static holds the console's own files, which ship with every
// release and are built into the binary (DECISIONS A25): files/ has
//
//	lang/       the language files (<code>.json)
//	public/     what the console serves at its root: favicon.svg, img/
//	protos/     the protocol files, served at /protos
//	themes/     the built-in theme presets
//	config/     theme-palettes.json and supporters.json
//
// The files are embedded where they are, so the source tree and the
// binary hold the same bytes. cmd/embed-webui records their modification
// times before go build (embedfs.Stamp), and the console then answers for
// them with the same Last-Modified and ETag as for the files on disk.
// CONSOLE_STATIC_DIR reads a tree of the same shape from disk instead,
// for development (package config).
//
// What the console writes (data, uploads, installed fonts, the .env) is
// never here: it stays on disk in CONSOLE_DIR and DATA_DIR.
package static

import (
	"embed"
	"io/fs"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/embedfs"
)

//go:embed all:files
var files embed.FS

// Files is the tree in the binary, with the times cmd/embed-webui
// recorded; a file it recorded no time for has 1970-01-01.
var Files = sync.OnceValues(func() (fs.FS, error) {
	tree, err := embedfs.New(files, "files", time.Unix(0, 0))
	if err != nil {
		return nil, err
	}
	return tree, nil
})
