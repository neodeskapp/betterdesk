package servermgmt

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// ReadRoots are the folders the file browser may list and read
// (getAllowedFileRoots), each once, in this order: the server's folder
// (serverPath), the console's folder
// (NEODESK_CONSOLE_PATH, default WorkDir), the account's home, LogDir,
// the temp folder, the TempDirs, then NEODESK_FILE_ALLOWED_ROOTS. All
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
	s.addListed(roots, "NEODESK_FILE_ALLOWED_ROOTS")
	return roots.list
}

// serverPath is the server's folder by the rule of the update and
// management routes (managedconfig.Config.Paths): NEODESK_PATH,
// RUSTDESK_DIR or RUSTDESK_PATH, else /opt/neodesk, on Windows
// C:\NeoDesk.
func (s *Service) serverPath() string {
	env := s.opts.Getenv
	dir := firstSet(env("NEODESK_PATH"), env("RUSTDESK_DIR"), env("RUSTDESK_PATH"))
	if dir == "" {
		dir = "/opt/neodesk"
		if s.windows() {
			dir = `C:\NeoDesk`
		}
	}
	return s.resolve(dir)
}

func (s *Service) consolePath() string {
	return s.resolve(firstSet(s.opts.Getenv("NEODESK_CONSOLE_PATH"), s.opts.WorkDir))
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
