// Command migrate is betterdesk-server migrate as a program of its own,
// for scripts that build it from source.
package main

import (
	"os"

	"github.com/unitronix/betterdesk-server/migrate"
)

func main() {
	os.Exit(migrate.Main(os.Args[1:], os.Stdout, os.Stderr))
}
