package updater

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
)

// defaultCLICopy is where the Linux installer keeps the command line: a
// root-owned copy of the server binary that every user may run.
const defaultCLICopy = "/usr/local/bin/neodesk"

// cliCopyOutdated reports whether a Linux host install's command line is
// an older binary than the running server: an update from the console
// replaces only the server binary, and the root-owned copy needs
// `sudo neodesk-setup --update` once. A link (older layouts), no copy,
// another system and Docker answer false.
func (s *Service) cliCopyOutdated() bool {
	if s.ImageMode() || s.opts.Docker || s.opts.GOOS != "linux" {
		return false
	}
	path := s.opts.CLICopy
	if path == "" {
		path = defaultCLICopy
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		return false
	}
	mine, err := fileSum(paths.Binary)
	if err != nil {
		return false
	}
	theirs, err := fileSum(path)
	if err != nil {
		return false
	}
	return !bytes.Equal(mine, theirs)
}

func fileSum(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
