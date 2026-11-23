// Command migrate is neodesk-server migrate as a program of its own,
// for scripts that build it from source.
package main

import (
	"os"

	"github.com/neodeskapp/neodesk/neodesk-server/migrate"
)

func main() {
	os.Exit(migrate.Main(os.Args[1:], os.Stdout, os.Stderr))
}
