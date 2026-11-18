package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// A backup before an install is a folder DATA_DIR/backups/pre-update-<time>
// with manifest.json. The old console's backups hold console, script and
// server source files; a backup of this updater holds the binary that ran
// before the install, as server/<binary name>, and the full data backup
// of console/backup beside it.

// backupKind marks a manifest this updater wrote.
const backupKind = "betterdesk-server-binary"

// BackupManifest is manifest.json of a backup this updater wrote. The
// first five fields are those of the old console's manifests, so the list
// shows both alike.
type BackupManifest struct {
	Version       string   `json:"version"`
	SHA           string   `json:"sha"`
	Timestamp     string   `json:"timestamp"`
	FilesBackedUp int      `json:"filesBackedUp"`
	Files         []string `json:"files"`
	Kind          string   `json:"kind"`
	BinarySHA256  string   `json:"binarySha256"`
	// MinRollbackVersion is the oldest version the release installed
	// over this backup can go back to; a restore of an older one is
	// refused.
	MinRollbackVersion string `json:"minRollbackVersion,omitempty"`
	// DataBackup is the file name of the data backup in the folder, empty
	// when none was made.
	DataBackup string `json:"dataBackup,omitempty"`
}

// BackupEntry is one backup in the list (listBackups).
type BackupEntry struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Version       any    `json:"version"`
	SHA           string `json:"sha"`
	Timestamp     any    `json:"timestamp"`
	FilesBackedUp any    `json:"filesBackedUp"`
	FileCount     any    `json:"fileCount"`
	SizeBytes     int64  `json:"sizeBytes"`

	sortKey string
}

// reBackupName is the only form of a backup's name (isValidBackupName).
var reBackupName = regexp.MustCompile(`^pre-update-[0-9T-]+$`)

// ValidBackupName reports a name of the form pre-update-<digits, T and ->.
func ValidBackupName(name string) bool { return reBackupName.MatchString(name) }

func (s *Service) backupRoot() string { return filepath.Join(s.opts.DataDir, backupsDir) }

// ListBackups lists the backups, newest first. A manifest that is
// missing or no JSON counts as empty.
func (s *Service) ListBackups() []BackupEntry {
	root := s.backupRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return []BackupEntry{}
	}
	list := []BackupEntry{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "pre-update-") {
			continue
		}
		dir := filepath.Join(root, name)
		var m any = goapi.NewObject()
		if data, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
			if parsed, err := goapi.Parse(data); err == nil {
				m = parsed
			}
		}
		or := func(key string, fallback any) any {
			if v := goapi.Dig(m, key); goapi.Truthy(v) {
				return v
			}
			return fallback
		}
		sha := jsText(goapi.Dig(m, "sha"))
		if len(sha) > 7 {
			sha = sha[:7]
		}
		timestamp := or("timestamp", "")
		files := or("filesBackedUp", 0.0)
		list = append(list, BackupEntry{
			Name: name, Path: dir, Version: or("version", "unknown"), SHA: sha, Timestamp: timestamp,
			FilesBackedUp: files, FileCount: files, SizeBytes: directorySize(dir), sortKey: jsText(timestamp),
		})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].sortKey > list[j].sortKey })
	return list
}

// directorySize adds up the sizes of the regular files below dir.
func directorySize(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			total += directorySize(full)
		case e.Type().IsRegular():
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	return total
}

// BackupError is a refused backup operation; the routes answer 400 for
// the ones whose message says invalid, not found or outside.
type BackupError struct{ Message string }

func (e *BackupError) Error() string { return e.Message }

// backupDir checks a name and returns the backup's folder.
func (s *Service) backupDir(name string) (string, error) {
	if !ValidBackupName(name) {
		return "", &BackupError{Message: "Invalid backup name"}
	}
	dir := filepath.Join(s.backupRoot(), name)
	if filepath.Dir(dir) != s.backupRoot() {
		return "", &BackupError{Message: "Invalid backup name"}
	}
	info, err := os.Lstat(dir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", &BackupError{Message: "Backup not found"}
	}
	return dir, nil
}

// DeleteBackup removes one backup (deleteBackup).
func (s *Service) DeleteBackup(name string) (map[string]string, error) {
	dir, err := s.backupDir(name)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("delete backup %s: %w", name, err)
	}
	return map[string]string{"deleted": name}, nil
}

// Pruned is the answer of PruneBackups.
type Pruned struct {
	Kept    int      `json:"kept"`
	Deleted []string `json:"deleted"`
}

// PruneBackups keeps the keep newest backups and deletes the others; keep
// of 0 or less keeps them all and answers kept -1 (pruneBackups).
func (s *Service) PruneBackups(keep int) Pruned {
	if keep <= 0 {
		return Pruned{Kept: -1, Deleted: []string{}}
	}
	all := s.ListBackups()
	out := Pruned{Kept: keep, Deleted: []string{}}
	if len(all) <= keep {
		return out
	}
	for _, b := range all[keep:] {
		if _, err := s.DeleteBackup(b.Name); err != nil {
			s.log.Warn(fmt.Sprintf("[UPDATE] Failed to prune backup %s: %v", b.Name, err))
			continue
		}
		out.Deleted = append(out.Deleted, b.Name)
	}
	return out
}

// retention is the number of backups to keep after a new one: the
// setting backup_retention_count when it is a positive number, else
// BACKUP_RETENTION_COUNT, else all.
func (s *Service) retention(ctx context.Context) int {
	if s.opts.Setting != nil {
		if value, found, err := s.opts.Setting(ctx, "backup_retention_count"); err == nil && found && value != "" {
			if n, ok := parseLeadingInt(value); ok && n > 0 {
				return n
			}
		}
	}
	if n, ok := parseLeadingInt(s.getenv("BACKUP_RETENTION_COUNT")); ok && n > 0 {
		return n
	}
	return 0
}

// parseLeadingInt is parseInt(text, 10) for the values it can give a
// whole number for.
func parseLeadingInt(text string) (int, bool) {
	text = strings.TrimLeft(text, " \t\n\r\v\f")
	end := 0
	if end < len(text) && (text[end] == '-' || text[end] == '+') {
		end++
	}
	digits := end
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == digits {
		return 0, false
	}
	n, err := strconv.Atoi(text[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// createBackup writes the backup of an install: the running binary and,
// when a data backup is wired, the full data backup. A backup that fails
// half way is removed.
func (s *Service) createBackup(ctx context.Context, paths BinaryPaths, minRollback string) (_ string, _ int, err error) {
	root := s.backupRoot()
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", 0, fmt.Errorf("create backup folder: %w", err)
	}
	// A new folder, never one that exists: a failed backup removes it.
	name := "pre-update-" + s.opts.Now().UTC().Format("2006-01-02T15-04-05")
	dir := filepath.Join(root, name)
	for n := 1; ; n++ {
		err := os.Mkdir(dir, 0o750)
		if err == nil {
			break
		}
		if !os.IsExist(err) || n > 9 {
			return "", 0, fmt.Errorf("create backup folder: %w", err)
		}
		dir = filepath.Join(root, name+"-"+strconv.Itoa(n))
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	rel := "server/" + filepath.Base(paths.Binary)
	if err := os.MkdirAll(filepath.Join(dir, "server"), 0o750); err != nil {
		return "", 0, fmt.Errorf("create backup folder: %w", err)
	}
	target := filepath.Join(dir, "server", filepath.Base(paths.Binary))
	_ = os.Remove(target)
	if err := copyVerified(paths.Binary, target, ""); err != nil {
		return "", 0, fmt.Errorf("back up the running binary: %w", err)
	}
	sum, err := fileSHA256(target)
	if err != nil {
		return "", 0, fmt.Errorf("back up the running binary: %w", err)
	}
	manifest := BackupManifest{
		Version: s.opts.Version, SHA: s.opts.Commit, Timestamp: s.isoNow(), FilesBackedUp: 1,
		Files: []string{rel}, Kind: backupKind, BinarySHA256: sum, MinRollbackVersion: minRollback,
	}
	backedUp := 1
	if s.opts.DataBackup != nil {
		archive, fileName, err := s.opts.DataBackup.CreateFullBackup(ctx)
		if err != nil {
			return "", 0, fmt.Errorf("back up the data: %w", err)
		}
		if fileName = filepath.Base(fileName); !reDataBackupName.MatchString(fileName) {
			return "", 0, fmt.Errorf("back up the data: unexpected file name %q", fileName)
		}
		if err := os.WriteFile(filepath.Join(dir, fileName), archive, 0o600); err != nil {
			return "", 0, fmt.Errorf("back up the data: %w", err)
		}
		manifest.DataBackup = fileName
		backedUp++
	}
	text, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), text, 0o640); err != nil {
		return "", 0, fmt.Errorf("write backup manifest: %w", err)
	}
	if keep := s.retention(ctx); keep > 0 {
		if pruned := s.PruneBackups(keep); len(pruned.Deleted) > 0 {
			s.log.Info(fmt.Sprintf("[UPDATE] Pruned %d old backup(s) (retention=%d)", len(pruned.Deleted), keep))
		}
	}
	return dir, backedUp, nil
}

var reDataBackupName = regexp.MustCompile(`^betterdesk-backup-[0-9-]+\.tar\.gz$`)

// errNodeBackup is the refusal of a backup of the old console (DECISIONS
// A29).
var errNodeBackup = &BackupError{Message: "This backup holds files of the old Node console and cannot be restored by this version"}

// readBackupManifest reads and checks the manifest of a backup this
// updater wrote. It refuses a backup of the old console.
func readBackupManifest(dir string) (BackupManifest, error) {
	var m BackupManifest
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, &BackupError{Message: "Invalid backup — missing manifest"}
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, &BackupError{Message: "Invalid backup — the manifest is no JSON"}
	}
	if m.Kind != backupKind {
		return m, errNodeBackup
	}
	if len(m.Files) != 1 || !strings.HasPrefix(m.Files[0], "server/") || !reVersion.MatchString(m.Version) ||
		!reSHA256.MatchString(m.BinarySHA256) {
		return m, &BackupError{Message: "Invalid backup — the manifest is incomplete"}
	}
	return m, nil
}

// ErrBusy is an install, restore or repair while another runs.
var ErrBusy = errors.New("Another update is already in progress")
