// Package scheduledbackup writes full backups on a schedule
// (services/scheduledBackupService.js): its settings, its state, the
// check of a destination folder, and the job that runs it.
//
// The archive itself comes from package backup, so a scheduled backup
// holds what a download holds. This package stores it, checks it twice
// (as made and as read back from the disk), writes its SHA-256 next to
// it and removes the oldest archives beyond the retention count.
//
// Settings live in the settings table as JSON: scheduled_backup_config
// and scheduled_backup_status. A backup goes to a folder; a network share
// is mounted there by the operating system.
package scheduledbackup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/unitronix/betterdesk-server/console/backup"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/logging"
)

// The setting keys and the limits of the schedule.
const (
	ConfigKey = "scheduled_backup_config"
	StatusKey = "scheduled_backup_status"

	DefaultIntervalHours = 24
	DefaultRetention     = 7
	minIntervalHours     = 1
	maxIntervalHours     = 24 * 30
	maxRetention         = 365
)

// Backups makes and checks archives (package backup).
type Backups interface {
	CreateFullBackup(ctx context.Context) ([]byte, string, error)
	ValidateFullBackup(buf []byte) backup.Validation
}

// Options are the settings of a Service.
type Options struct {
	Store   *store.DB
	Backups Backups
	// DataDir is DATA_DIR; the default destination is
	// DATA_DIR/scheduled-backups.
	DataDir string
	// AppDir is the console's own folder (web-nodejs), which may not be a
	// destination.
	AppDir string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// PID is the process id in the name of a file being written; 0 is
	// os.Getpid().
	PID int
	// Windows reads a destination as Windows does, where the root of a
	// network share (a UNC path) is a folder; the default is the system's.
	Windows *bool
	// Ticker gives the job its minute; nil is time.NewTicker.
	Ticker func(d time.Duration) (<-chan time.Time, func())
}

// Service is the scheduled backup.
type Service struct {
	o       Options
	running atomic.Bool
}

// New returns a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.Windows == nil {
		w := runtime.GOOS == "windows"
		o.Windows = &w
	}
	if o.Ticker == nil {
		o.Ticker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	return &Service{o: o}
}

// DefaultDestination is DATA_DIR/scheduled-backups.
func (s *Service) DefaultDestination() string {
	return filepath.Join(s.o.DataDir, "scheduled-backups")
}

func (s *Service) iso(t time.Time) string { return store.ISOTime(t) }

// ---- Values ----

// Values are those of package branding: nil, branding.Undefined, bool,
// float64, string, []any and *branding.Object.

type jsError string

func (e jsError) Error() string { return string(e) }

const errToPrimitive = jsError("Cannot convert object to primitive value")

func prop(v any, key string) any {
	o, ok := v.(*branding.Object)
	if !ok || o == nil {
		return branding.Undefined
	}
	value, has := o.Get(key)
	if !has {
		return branding.Undefined
	}
	return value
}

// jsString is String(v).
func jsString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return jsbind.NumberString(x), nil
	case string:
		return x, nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil || e == branding.Undefined {
				continue
			}
			s, err := jsString(e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), nil
	case *branding.Object:
		if x.Has("toString") {
			return "", errToPrimitive
		}
		return "[object Object]", nil
	}
	return "undefined", nil
}

// jsNumber is Number(v).
func jsNumber(v any) (float64, error) {
	switch x := v.(type) {
	case nil:
		return 0, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case float64:
		return x, nil
	case string:
		return jsbind.ParseNumber(x), nil
	case []any:
		s, err := jsString(x)
		if err != nil {
			return 0, err
		}
		return jsbind.ParseNumber(s), nil
	case *branding.Object:
		if x.Has("toString") {
			return 0, errToPrimitive
		}
		return math.NaN(), nil
	}
	return math.NaN(), nil
}

// jsRound is Math.round.
func jsRound(f float64) float64 { return math.Floor(f + 0.5) }

// parseStored is parseStored(value, fallback): the stored JSON when it
// is an object or a list, else the fallback.
func parseStored(text sql.NullString, fallback any) any {
	if !text.Valid || text.String == "" {
		return fallback
	}
	v, err := branding.ParseJSON([]byte(text.String))
	if err != nil {
		return fallback
	}
	switch v.(type) {
	case *branding.Object, []any:
		return v
	}
	return fallback
}

func (s *Service) getSetting(ctx context.Context, key string) (sql.NullString, error) {
	var v sql.NullString
	err := s.o.Store.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, nil
	}
	return v, err
}

func (s *Service) setSetting(ctx context.Context, key, value string) error {
	db := s.o.Store
	_, err := db.Exec(ctx, db.Pick(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, NOW())
		 ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`), key, value)
	return err
}

// spread is {...v}: the own keys of an object, the indexes of a list.
func spread(v any) *branding.Object {
	out := branding.NewObject()
	switch x := v.(type) {
	case *branding.Object:
		for _, k := range x.Keys() {
			value, _ := x.Get(k)
			out.Set(k, value)
		}
	case []any:
		for i, item := range x {
			out.Set(strconv.Itoa(i), item)
		}
	}
	return out
}

// ---- Schedule ----

// Config is a schedule as normalizeConfig made it.
type Config struct {
	Enabled         bool    `json:"enabled"`
	IntervalHours   float64 `json:"intervalHours"`
	RetentionCount  float64 `json:"retentionCount"`
	DestinationType string  `json:"destinationType"`
	DestinationPath string  `json:"destinationPath"`
}

// Normalize is normalizeConfig: the interval in whole hours from 1 to 720
// (24 when it is no number), the retention from 1 to 365 (7), "local" or
// "path", and the path only for "path".
func Normalize(v any) (Config, error) {
	interval, err := jsNumber(prop(v, "intervalHours"))
	if err != nil {
		return Config{}, err
	}
	retention, err := jsNumber(prop(v, "retentionCount"))
	if err != nil {
		return Config{}, err
	}
	c := Config{
		IntervalHours:   DefaultIntervalHours,
		RetentionCount:  DefaultRetention,
		DestinationType: "local",
	}
	if prop(v, "destinationType") == "path" {
		c.DestinationType = "path"
		if p, ok := prop(v, "destinationPath").(string); ok {
			c.DestinationPath = jsbind.Trim(p)
		}
	}
	enabled := prop(v, "enabled")
	c.Enabled = enabled == true || enabled == "true"
	if !math.IsNaN(interval) && !math.IsInf(interval, 0) {
		c.IntervalHours = math.Min(maxIntervalHours, math.Max(minIntervalHours, jsRound(interval)))
	}
	if !math.IsNaN(retention) && !math.IsInf(retention, 0) {
		c.RetentionCount = math.Min(maxRetention, math.Max(1, jsRound(retention)))
	}
	return c, nil
}

// Public is publicConfig: a local destination is named by its folder.
func (s *Service) Public(c Config) Config {
	if c.DestinationType == "local" {
		c.DestinationPath = s.DefaultDestination()
	}
	return c
}

// ValidateDestinationPath is validateDestinationPath: an absolute path
// that is neither the root of a file system nor the console's folder or
// one below it. It returns the path cleaned.
func (s *Service) ValidateDestinationPath(p string) (string, error) {
	input := jsbind.Trim(p)
	if input == "" {
		return "", errors.New("Backup destination path is required")
	}
	if !filepath.IsAbs(input) {
		return "", errors.New("Backup destination path must be absolute")
	}
	resolved := filepath.Clean(input)
	root := filepath.VolumeName(resolved) + string(filepath.Separator)
	uncRoot := *s.o.Windows && strings.HasPrefix(resolved, `\\`)
	if resolved == root && !uncRoot {
		return "", errors.New("The filesystem root cannot be used as a backup destination")
	}
	app := filepath.Clean(s.o.AppDir)
	if s.o.AppDir != "" && (resolved == app || strings.HasPrefix(resolved, app+string(filepath.Separator))) {
		return "", errors.New("The BetterDesk application directory cannot be used as a backup destination")
	}
	return resolved, nil
}

// ResolveDestination is where a schedule writes.
func (s *Service) ResolveDestination(c Config) (string, error) {
	if c.DestinationType == "local" {
		return s.DefaultDestination(), nil
	}
	return s.ValidateDestinationPath(c.DestinationPath)
}

// GetConfig reads the schedule.
func (s *Service) GetConfig(ctx context.Context) (Config, error) {
	stored, err := s.getSetting(ctx, ConfigKey)
	if err != nil {
		return Config{}, err
	}
	return Normalize(parseStored(stored, branding.NewObject()))
}

// SaveConfig stores a schedule and returns it as Public shows it. A path
// destination is checked first.
func (s *Service) SaveConfig(ctx context.Context, v any) (Config, error) {
	c, err := Normalize(v)
	if err != nil {
		return Config{}, err
	}
	if c.DestinationType == "path" {
		if _, err := s.ValidateDestinationPath(c.DestinationPath); err != nil {
			return Config{}, err
		}
	}
	raw := branding.Stringify(configObject(c))
	if err := s.setSetting(ctx, ConfigKey, raw); err != nil {
		return Config{}, err
	}
	return s.Public(c), nil
}

func configObject(c Config) *branding.Object {
	o := branding.NewObject()
	o.Set("enabled", c.Enabled)
	o.Set("intervalHours", c.IntervalHours)
	o.Set("retentionCount", c.RetentionCount)
	o.Set("destinationType", c.DestinationType)
	o.Set("destinationPath", c.DestinationPath)
	return o
}

// ConfigJSON is the JSON of a schedule.
func ConfigJSON(c Config) string { return branding.Stringify(configObject(c)) }

// defaultStatus is the state before the first run.
func defaultStatus() *branding.Object {
	o := branding.NewObject()
	for _, k := range []string{"lastAttemptAt", "lastSuccessAt", "lastFile", "lastSha256", "lastValidation", "lastError"} {
		o.Set(k, nil)
	}
	return o
}

// GetStatus reads the state of the last runs, as stored.
func (s *Service) GetStatus(ctx context.Context) (any, error) {
	stored, err := s.getSetting(ctx, StatusKey)
	if err != nil {
		return nil, err
	}
	return parseStored(stored, defaultStatus()), nil
}

func (s *Service) saveStatus(ctx context.Context, status *branding.Object) error {
	return s.setSetting(ctx, StatusKey, branding.Stringify(status))
}

// ---- Runs ----

// Result is what a run wrote.
type Result struct {
	Filename   string
	SHA256     string
	Validation *branding.Object
	Deleted    []string
}

// ErrRunning is the answer while a run is going on.
var ErrRunning = errors.New("A scheduled backup is already running")

// CreateScheduledBackup writes one archive now (createScheduledBackup).
// The attempt is noted first. The archive is checked as made, written to
// a temporary name and renamed, read back and checked again (a copy that
// fails this check is removed), and its SHA-256 is written next to it as
// "<hex>  <name>\n". Then the oldest archives beyond the retention count
// go. The state records the outcome either way; an error is returned
// after it is recorded. When the state cannot be read or noted at the
// start, the error is returned and nothing is written. Either way the
// service is free for the next run when this one returns.
func (s *Service) CreateScheduledBackup(ctx context.Context) (*Result, error) {
	if !s.running.CompareAndSwap(false, true) {
		return nil, ErrRunning
	}
	defer s.running.Store(false)
	startedAt := s.iso(s.o.Now())
	stored, err := s.GetStatus(ctx)
	if err != nil {
		return nil, err
	}
	status := spread(stored)
	status.Set("lastAttemptAt", startedAt)
	status.Set("lastError", nil)
	if err := s.saveStatus(ctx, status); err != nil {
		return nil, err
	}

	res, err := s.write(ctx, status)
	if err != nil {
		failed := spread(status)
		validation := branding.NewObject()
		validation.Set("valid", false)
		failed.Set("lastValidation", validation)
		failed.Set("lastError", err.Error())
		if saveErr := s.saveStatus(ctx, failed); saveErr != nil {
			return nil, saveErr
		}
		return nil, err
	}
	return res, nil
}

func (s *Service) write(ctx context.Context, status *branding.Object) (*Result, error) {
	schedule, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	destination, err := s.ResolveDestination(schedule)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destination, 0o777); err != nil {
		return nil, backup.NodeFSError(err, "mkdir", destination)
	}
	archive, filename, err := s.o.Backups.CreateFullBackup(ctx)
	if err != nil {
		return nil, err
	}
	first := s.o.Backups.ValidateFullBackup(archive)
	if !first.Valid {
		return nil, fmt.Errorf("Generated backup failed validation: %s", strings.Join(first.Errors, "; "))
	}
	finalPath := filepath.Join(destination, filename)
	tempPath := fmt.Sprintf("%s.%d.%d.tmp", finalPath, s.o.PID, s.o.Now().UnixMilli())
	// the temporary file holds what the archive holds: it goes on every
	// failure
	if err := os.WriteFile(tempPath, archive, 0o600); err != nil {
		removeFile(tempPath)
		return nil, backup.NodeFSError(err, "open", tempPath)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		removeFile(tempPath)
		return nil, backup.NodeFSError(err, "rename", tempPath)
	}
	persisted, err := os.ReadFile(finalPath)
	if err != nil {
		return nil, backup.NodeFSError(err, "open", finalPath)
	}
	second := s.o.Backups.ValidateFullBackup(persisted)
	if !second.Valid {
		_ = os.Remove(finalPath)
		return nil, fmt.Errorf("Stored backup failed validation: %s", strings.Join(second.Errors, "; "))
	}
	sum := sha256.Sum256(persisted)
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(finalPath+".sha256", []byte(digest+"  "+filename+"\n"), 0o600); err != nil {
		return nil, backup.NodeFSError(err, "open", finalPath+".sha256")
	}
	removeStaleTemporaryFiles(destination, s.o.Now().Add(-staleTemporary))
	deleted := prune(destination, int(schedule.RetentionCount))

	var components any = []any{}
	if c := prop(second.Manifest, "components"); branding.Truthy(c) {
		components = c
	}
	validation := branding.NewObject()
	validation.Set("valid", true)
	validation.Set("components", components)
	validation.Set("sizeBytes", float64(len(persisted)))
	done := spread(status)
	done.Set("lastSuccessAt", s.iso(s.o.Now()))
	done.Set("lastFile", finalPath)
	done.Set("lastSha256", digest)
	done.Set("lastValidation", validation)
	done.Set("lastPruned", float64(len(deleted)))
	done.Set("lastError", nil)
	if err := s.saveStatus(ctx, done); err != nil {
		return nil, err
	}
	return &Result{Filename: finalPath, SHA256: digest, Validation: validation, Deleted: deleted}, nil
}

var archiveName = regexp.MustCompile(`^betterdesk-backup-[^\n\r\x{2028}\x{2029}]*\.tar\.gz$`)

// prune removes the archives beyond keep, newest first by modification
// time, each with its .sha256. A file that cannot be removed stays and is
// not counted; when the folder cannot be listed nothing is removed.
func prune(dir string, keep int) []string {
	deleted := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return deleted
	}
	type archive struct {
		name string
		path string
		mod  time.Time
		dir  bool
	}
	var list []archive
	for _, e := range entries {
		if !archiveName.MatchString(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, err := os.Stat(full)
		if err != nil {
			return deleted
		}
		list = append(list, archive{e.Name(), full, info.ModTime(), info.IsDir()})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })
	if keep >= len(list) {
		return deleted
	}
	for _, a := range list[keep:] {
		if a.dir || !removeFile(a.path) || !removeFile(a.path+".sha256") {
			continue
		}
		deleted = append(deleted, a.name)
	}
	return deleted
}

// temporaryName is the name of a run's temporary file:
// <archive name>.<pid>.<milliseconds>.tmp.
var temporaryName = regexp.MustCompile(`^betterdesk-backup-[^\n\r\x{2028}\x{2029}]*\.tar\.gz\.[0-9]+\.[0-9]+\.tmp$`)

// staleTemporary is the age from which a temporary file counts as left by
// a run that ended without removing it (a crash, a kill); a younger one
// may belong to a run of another process that still writes it.
const staleTemporary = time.Hour

// removeStaleTemporaryFiles removes the temporary files of runs in dir
// last changed before the given time.
func removeStaleTemporaryFiles(dir string, before time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !temporaryName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(before) {
			continue
		}
		removeFile(filepath.Join(dir, e.Name()))
	}
}

// removeFile is fs.rmSync(path, {force: true}): a missing file is fine, a
// folder is an error.
func removeFile(path string) bool {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || info.IsDir() {
		return false
	}
	return os.Remove(path) == nil
}

// Skip says why RunIfDue did not run.
type Skip struct {
	Reason string
}

// RunIfDue runs a backup when the schedule is on and its interval has
// passed since the last attempt (runIfDue). It returns the skip reason
// ("running", "disabled", "not_due") or the run's result.
func (s *Service) RunIfDue(ctx context.Context) (*Skip, *Result, error) {
	if s.running.Load() {
		return &Skip{"running"}, nil, nil
	}
	schedule, err := s.GetConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !schedule.Enabled {
		return &Skip{"disabled"}, nil, nil
	}
	status, err := s.GetStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	last, ok := parseDate(prop(status, "lastAttemptAt"))
	if ok && s.o.Now().UnixMilli()-last < int64(schedule.IntervalHours)*3600*1000 {
		return &Skip{"not_due"}, nil, nil
	}
	res, err := s.CreateScheduledBackup(ctx)
	return nil, res, err
}

// parseDate is Date.parse(v || "") for the times this service writes.
func parseDate(v any) (int64, bool) {
	text, ok := v.(string)
	if !ok || text == "" {
		return 0, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000Z07:00", time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

// Run is the job: every minute RunIfDue, until ctx ends. A failed run is
// logged.
func (s *Service) Run(ctx context.Context) {
	tick, stop := s.o.Ticker(time.Minute)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			if _, _, err := s.RunIfDue(ctx); err != nil {
				logging.Logf("backup", "Scheduled backup failed: %s", err.Error())
			}
		}
	}
}

// TestDestination makes the destination of a schedule, writes a probe file
// there and removes it.
func (s *Service) TestDestination(v any) (string, error) {
	c, err := Normalize(v)
	if err != nil {
		return "", err
	}
	destination, err := s.ResolveDestination(c)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destination, 0o777); err != nil {
		return "", backup.NodeFSError(err, "mkdir", destination)
	}
	probe := filepath.Join(destination, fmt.Sprintf(".betterdesk-backup-probe-%d-%d", s.o.PID, s.o.Now().UnixMilli()))
	f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", backup.NodeFSError(err, "open", probe)
	}
	_, werr := f.WriteString("BetterDesk backup destination test\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		// The old service threw before it removed the probe.
		return "", backup.NodeFSError(werr, "write", "")
	}
	_ = os.Remove(probe)
	return destination, nil
}
