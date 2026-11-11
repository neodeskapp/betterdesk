// Package advancedconfig is the configuration file editor of the settings
// page: a fixed list of the server's and the console's configuration
// files that an operator with server.config can read and overwrite
// (services/advancedConfigService.js of the old console).
//
// A caller names a file by its id, never by a path. The paths are worked
// out on every call from the options and two environment variables, as
// the old console did:
//
//	console-env              <console>/.env
//	console-env-local        <console>/.env.local                      may be created
//	console-session-secret   <data>/.session_secret                    may be created
//	go-blocklist             <keys>/blocklist.txt                      may be created
//	go-audit-log             AUDIT_LOG_FILE when absolute, else the    may be created
//	                         first of /var/log/betterdesk/audit.jsonl
//	                         and <keys>/audit.jsonl that exists, else
//	                         /var/log/betterdesk/audit.jsonl
//	systemd-console          /etc/systemd/system/betterdesk-console.service   Linux only
//	systemd-server           /etc/systemd/system/betterdesk-server.service    Linux only
//	build-env                BETTERDESK_BUILD_ENV_FILE, else           may be created, Linux only
//	                         /etc/betterdesk/build.env
//	docker-supervisord       <console>/../docker/supervisord.conf
//	docker-compose           <console>/../docker-compose.yml
//
// Files are read and written up to 512 KiB. A write of a file that exists
// first copies it to "<path>.bak.<time>", and RestoreBackup copies such a
// backup back. These files hold secrets: nothing here logs their content.
package advancedconfig

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// MaxBytes is the largest file the editor reads or writes.
const MaxBytes = 512 * 1024

// The refusals of the editor. Their texts are the messages the old
// console threw; the settings routes turn them into translated answers.
var (
	ErrUnknownFile    = errors.New("unknown_file")
	ErrNotFound       = errors.New("not_found")
	ErrNotAFile       = errors.New("not_a_file")
	ErrTooLarge       = errors.New("file_too_large")
	ErrBinary         = errors.New("binary_file")
	ErrInvalidContent = errors.New("invalid_content")
	ErrNotWritable    = errors.New("not_writable")
	ErrInvalidBackup  = errors.New("invalid_backup")
)

// Options are the settings of a Service.
type Options struct {
	// ConsoleDir is the old console's folder (CONSOLE_DIR; the folder of
	// web-nodejs): its .env and .env.local, and through its parent the
	// repository's docker files.
	ConsoleDir string
	// DataDir is the console's data folder (DATA_DIR): .session_secret.
	DataDir string
	// KeysPath is the server's keys folder (KEYS_PATH, RUSTDESK_DIR,
	// RUSTDESK_PATH): blocklist.txt and audit.jsonl.
	KeysPath string
	// Getenv reads AUDIT_LOG_FILE and BETTERDESK_BUILD_ENV_FILE on every
	// call. Nil reads nothing.
	Getenv func(string) string
	// Platform is the operating system as Node names it
	// (process.platform): "linux" lists the systemd units and the build
	// settings. Empty means the running system.
	Platform string
	// Now is the clock of the backup names. Nil means time.Now.
	Now func() time.Time
}

// Service reads and writes the files of the catalog. It is safe for
// concurrent use; one write or restore runs at a time.
type Service struct {
	opts Options
	// writable is fs.access(path, W_OK); tests replace it, because a test
	// that runs as root may write everything.
	writable func(path string) bool
	mu       sync.Mutex
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	if opts.Platform == "" {
		opts.Platform = runtime.GOOS
		if opts.Platform == "windows" {
			opts.Platform = "win32"
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, writable: writable}
}

// definition is an entry of the catalog.
type definition struct {
	id              string
	path            func(s *Service) string
	category        string
	requiresRestart string
	canCreate       bool
	linuxOnly       bool
}

// catalog is the fixed list of files, in the order the editor lists them.
var catalog = []definition{
	{id: "console-env", category: "console", requiresRestart: "console",
		path: func(s *Service) string { return filepath.Join(s.opts.ConsoleDir, ".env") }},
	{id: "console-env-local", category: "console", requiresRestart: "console", canCreate: true,
		path: func(s *Service) string { return filepath.Join(s.opts.ConsoleDir, ".env.local") }},
	{id: "console-session-secret", category: "console", requiresRestart: "console", canCreate: true,
		path: func(s *Service) string { return filepath.Join(s.opts.DataDir, ".session_secret") }},
	{id: "go-blocklist", category: "goserver", requiresRestart: "goserver", canCreate: true,
		path: func(s *Service) string { return filepath.Join(s.opts.KeysPath, "blocklist.txt") }},
	{id: "go-audit-log", category: "goserver", requiresRestart: "goserver", canCreate: true,
		path: (*Service).auditLogPath},
	{id: "systemd-console", category: "system", requiresRestart: "systemd", linuxOnly: true,
		path: func(*Service) string { return "/etc/systemd/system/betterdesk-console.service" }},
	{id: "systemd-server", category: "system", requiresRestart: "systemd", linuxOnly: true,
		path: func(*Service) string { return "/etc/systemd/system/betterdesk-server.service" }},
	{id: "build-env", category: "system", requiresRestart: "none", canCreate: true, linuxOnly: true,
		path: func(s *Service) string {
			if p := s.opts.Getenv("BETTERDESK_BUILD_ENV_FILE"); p != "" {
				return p
			}
			return "/etc/betterdesk/build.env"
		}},
	{id: "docker-supervisord", category: "console", requiresRestart: "console",
		path: func(s *Service) string { return filepath.Join(s.opts.ConsoleDir, "..", "docker", "supervisord.conf") }},
	{id: "docker-compose", category: "console", requiresRestart: "console",
		path: func(s *Service) string { return filepath.Join(s.opts.ConsoleDir, "..", "docker-compose.yml") }},
}

const defaultAuditLog = "/var/log/betterdesk/audit.jsonl"

// auditLogPath is AUDIT_LOG_FILE when it is absolute, else the first of
// the usual places that exists, else the first usual place.
func (s *Service) auditLogPath() string {
	if p := s.opts.Getenv("AUDIT_LOG_FILE"); p != "" && isAbsolute(p) {
		return p
	}
	for _, p := range []string{defaultAuditLog, filepath.Join(s.opts.KeysPath, "audit.jsonl")} {
		if exists(p) {
			return p
		}
	}
	return defaultAuditLog
}

// isAbsolute is Node's path.isAbsolute.
func isAbsolute(p string) bool {
	if runtime.GOOS == "windows" {
		return filepath.IsAbs(p) || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, "/")
}

// exists is fs.existsSync.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// lookup finds the catalog entry of an id that is allowed on this
// platform: getDefinition and isPlatformAllowed.
func (s *Service) lookup(id any) (definition, bool) {
	text, ok := id.(string)
	if !ok || text == "" {
		return definition{}, false
	}
	for _, def := range catalog {
		if def.id == text {
			return def, s.allowed(def)
		}
	}
	return definition{}, false
}

func (s *Service) allowed(def definition) bool {
	return !def.linuxOnly || s.opts.Platform == "linux"
}

// File is an entry of the list: the catalog's facts and what the file
// system says about the file. Mtime is null for a file that is not there.
type File struct {
	ID              string  `json:"id"`
	Path            string  `json:"path"`
	Category        string  `json:"category"`
	RequiresRestart string  `json:"requiresRestart"`
	CanCreate       bool    `json:"canCreate"`
	Exists          bool    `json:"exists"`
	Size            int64   `json:"size"`
	Mtime           *string `json:"mtime"`
	Readable        bool    `json:"readable"`
	Writable        bool    `json:"writable"`
	// Error is the message of a failed look at the file, other than "not
	// there".
	Error string `json:"error,omitempty"`
}

// fileState is statEntry: whether the file is there, and whether it can
// be read and written.
type fileState struct {
	exists, readable, writable bool
	size                       int64
	mtime                      *string
	err                        string
}

func (s *Service) state(def definition, path string) fileState {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fileState{writable: def.canCreate}
		}
		return fileState{err: nodeError(err, "stat", path).Error()}
	}
	mtime := mtimeOf(info)
	return fileState{
		exists: true, readable: true, writable: s.canWrite(path, info),
		size: info.Size(), mtime: &mtime,
	}
}

// canWrite reports whether the file can be written, or, for something
// that is not a regular file, whether its folder can.
func (s *Service) canWrite(path string, info fs.FileInfo) bool {
	if s.writable(path) {
		return true
	}
	if info.Mode().IsRegular() {
		return false
	}
	return s.writable(filepath.Dir(path))
}

// ListFiles lists the catalog's files for this platform.
func (s *Service) ListFiles() []File {
	files := []File{}
	for _, def := range catalog {
		if !s.allowed(def) {
			continue
		}
		path := def.path(s)
		st := s.state(def, path)
		files = append(files, File{
			ID: def.id, Path: path, Category: def.category, RequiresRestart: def.requiresRestart,
			CanCreate: def.canCreate, Exists: st.exists, Size: st.size, Mtime: st.mtime,
			Readable: st.readable, Writable: st.writable, Error: st.err,
		})
	}
	return files
}

// Content is a file as the editor shows it.
type Content struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	Mtime           string `json:"mtime"`
	RequiresRestart string `json:"requiresRestart"`
	Content         Text   `json:"content"`
}

// ReadFile reads a file of the catalog.
//
// Errors: ErrUnknownFile, ErrNotFound, ErrNotAFile (a folder),
// ErrTooLarge (over MaxBytes), ErrBinary (a NUL byte), and the file
// system's own errors.
func (s *Service) ReadFile(id string) (*Content, error) {
	def, ok := s.lookup(id)
	if !ok {
		return nil, ErrUnknownFile
	}
	path := resolve(def.path(s))
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, nodeError(err, "stat", path)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotAFile
	}
	if info.Size() > MaxBytes {
		return nil, ErrTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nodeError(err, "open", path)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, ErrBinary
	}
	return &Content{
		ID: def.id, Path: path, Size: info.Size(), Mtime: mtimeOf(info),
		RequiresRestart: def.requiresRestart, Content: Text(DecodeUTF8(data)),
	}, nil
}

// Written is what a write did. BackupPath is null when there was nothing
// to back up or the backup failed.
type Written struct {
	ID              string  `json:"id"`
	Path            string  `json:"path"`
	Size            int64   `json:"size"`
	Mtime           string  `json:"mtime"`
	BackupPath      *string `json:"backupPath"`
	RequiresRestart string  `json:"requiresRestart"`
	Created         bool    `json:"created"`
}

// WriteFile replaces a file of the catalog with content, the value the
// request sent: only text is accepted. A file that exists is backed up
// first; a file that does not is created when the catalog allows it,
// with its folder, readable for its owner only.
//
// Errors: ErrUnknownFile, ErrInvalidContent, ErrTooLarge (content or the
// file there now over MaxBytes), ErrNotAFile, ErrNotFound (missing and
// not to be created), ErrNotWritable, and the file system's own errors,
// worded as Node words them.
func (s *Service) WriteFile(id string, content any) (*Written, error) {
	def, ok := s.lookup(id)
	if !ok {
		return nil, ErrUnknownFile
	}
	text, isText := content.(string)
	if !isText {
		return nil, ErrInvalidContent
	}
	data := []byte(text)
	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	path := resolve(def.path(s))
	existed := false
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return nil, ErrNotAFile
		}
		existed = true
		if info.Size() > MaxBytes {
			return nil, ErrTooLarge
		}
	case errors.Is(err, fs.ErrNotExist):
		if !def.canCreate {
			return nil, ErrNotFound
		}
	default:
		return nil, nodeError(err, "stat", path)
	}
	if !s.state(def, def.path(s)).writable {
		return nil, ErrNotWritable
	}

	var backup *string
	if existed {
		backup = s.writeBackup(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nil, nodeError(err, "mkdir", filepath.Dir(path))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, nodeError(err, "open", path)
	}
	info, err = os.Stat(path)
	if err != nil {
		return nil, nodeError(err, "stat", path)
	}
	return &Written{
		ID: def.id, Path: path, Size: info.Size(), Mtime: mtimeOf(info),
		BackupPath: backup, RequiresRestart: def.requiresRestart, Created: !existed,
	}, nil
}

// writeBackup copies a file to "<path>.bak.<time>", the time as
// toISOString writes it with ":" and "." made "-". A failed copy gives no
// backup and no error.
func (s *Service) writeBackup(path string) *string {
	if !exists(path) {
		return nil
	}
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(httpx.ISOTime(s.opts.Now()))
	backup := path + ".bak." + stamp
	if err := copyFile(path, backup); err != nil {
		return nil
	}
	return &backup
}

// RestoreBackup undoes a write: with created it removes the file, else it
// copies the backup back. The backup must be a "<path>.bak." file of the
// same catalog entry, given as a clean absolute path, and must exist.
//
// Errors: ErrUnknownFile, ErrInvalidBackup, and the file system's own.
func (s *Service) RestoreBackup(id, backupPath any, created bool) error {
	def, ok := s.lookup(id)
	if !ok {
		return ErrUnknownFile
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := resolve(def.path(s))
	if created {
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nodeError(err, "unlink", target)
		}
		return nil
	}
	backup, isText := backupPath.(string)
	if !isText || !strings.HasPrefix(backup, target+".bak.") {
		return ErrInvalidBackup
	}
	if resolve(backup) != backup || !exists(backup) {
		return ErrInvalidBackup
	}
	if err := copyFile(backup, target); err != nil {
		return nodeCopyError(err, backup, target)
	}
	return nil
}

// resolve is Node's path.resolve for one path: absolute and clean.
func resolve(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// copyFile is fs.copyFile as libuv does it: the target is created or
// truncated and gets the mode of the source, nothing happens when both
// are one file, and a copy that fails once the target is open removes the
// target (a folder as the source fails only when it is read).
func copyFile(from, to string) (err error) {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(to)
		}
	}()
	if dstInfo, statErr := dst.Stat(); statErr != nil {
		return statErr
	} else if os.SameFile(info, dstInfo) {
		return nil
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}
	if err := dst.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

// mtimeOf is stats.mtime.toISOString(): Node rounds the time of the last
// change to the nearest millisecond.
func mtimeOf(info fs.FileInfo) string {
	return httpx.ISOTime(info.ModTime().Round(time.Millisecond))
}
