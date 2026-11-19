package servermgmt

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// ReadRoots are the folders the file browser may list and read
// (getAllowedFileRoots), each once, in this order: the server's folder
// (serverPath), the console's folder
// (BETTERDESK_CONSOLE_PATH, default WorkDir), the account's home, LogDir,
// the temp folder, the TempDirs, then BETTERDESK_FILE_ALLOWED_ROOTS. All
// are made absolute except LogDir, the temp folder and the TempDirs, which
// are listed as they are.
func (s *Service) ReadRoots() []string {
	roots := newRootSet()
	roots.add(s.serverPath())
	roots.add(s.consolePath())
	roots.add(s.resolve(s.home()))
	roots.add(s.opts.LogDir)
	roots.add(s.tempDir())
	for _, dir := range s.opts.TempDirs {
		roots.add(dir)
	}
	s.addListed(roots, "BETTERDESK_FILE_ALLOWED_ROOTS")
	return roots.list
}

// WriteRoots are the folders the file browser may change
// (getMutableFileRoots): the server's and the console's folder, then
// BETTERDESK_MUTABLE_ROOTS.
func (s *Service) WriteRoots() []string {
	roots := newRootSet()
	roots.add(s.serverPath())
	roots.add(s.consolePath())
	s.addListed(roots, "BETTERDESK_MUTABLE_ROOTS")
	return roots.list
}

// serverPath is the server's folder by the rule of the update and
// management routes (managedconfig.Config.Paths): BETTERDESK_PATH,
// RUSTDESK_DIR or RUSTDESK_PATH, else /opt/betterdesk, on Windows
// C:\BetterDesk.
func (s *Service) serverPath() string {
	env := s.opts.Getenv
	dir := firstSet(env("BETTERDESK_PATH"), env("RUSTDESK_DIR"), env("RUSTDESK_PATH"))
	if dir == "" {
		dir = "/opt/betterdesk"
		if s.windows() {
			dir = `C:\BetterDesk`
		}
	}
	return s.resolve(dir)
}

func (s *Service) consolePath() string {
	return s.resolve(firstSet(s.opts.Getenv("BETTERDESK_CONSOLE_PATH"), s.opts.WorkDir))
}

// addListed adds the folders of a comma-separated variable: each entry
// without white space around it, empty ones skipped, made absolute.
func (s *Service) addListed(roots *rootSet, key string) {
	value := s.opts.Getenv(key)
	if value == "" {
		return
	}
	for _, entry := range strings.Split(value, ",") {
		if trimmed := jsbind.Trim(entry); trimmed != "" {
			roots.add(s.resolve(trimmed))
		}
	}
}

// rootSet keeps folders once each, in the order they were added, as a
// JavaScript Set of strings did.
type rootSet struct {
	list []string
	seen map[string]bool
}

func newRootSet() *rootSet { return &rootSet{seen: map[string]bool{}} }

func (r *rootSet) add(dir string) {
	if r.seen[dir] {
		return
	}
	r.seen[dir] = true
	r.list = append(r.list, dir)
}
