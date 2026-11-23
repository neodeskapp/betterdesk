// Package system answers the system widgets of the dashboard: processes
// and disks, recent log lines, database statistics, containers, a runner
// for a fixed list of commands, and the payload of the speed test.
//
// The routes get their numbers from commands of the operating system (ps,
// df, journalctl, docker). A command that is missing or fails counts as
// one that printed nothing.
package system

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Options are what the group takes besides the console's services. A zero
// field is the real thing.
type Options struct {
	// Run runs a command line through the system's shell and returns what
	// it printed to standard output. A command that is missing, exits with
	// an error or runs longer than timeout returns an error.
	Run func(ctx context.Context, command string, timeout time.Duration) (string, error)
	// Platform is "linux", "win32" or another name of the system as Node
	// called it (process.platform).
	Platform string
	// TotalMemory is the machine's memory in bytes (os.totalmem()).
	TotalMemory func() uint64
	// OpenFile opens a log file for reading. An error that is
	// fs.ErrNotExist means the file is not there; any other error fails
	// the request.
	OpenFile func(path string) (io.ReadSeekCloser, error)
	// Process describes the console's own process.
	Process func() ProcessInfo
	// DatabaseFile is the SQLite file whose size the database statistics
	// name. Default: DB_PATH, else data/auth.db below the console's folder.
	DatabaseFile string
	// BackupDir is where the database statistics look for the newest
	// backup. Default: data/backups below the console's folder.
	BackupDir string
}

// ProcessInfo is the console's process as the system information shows
// it. The sizes are in megabytes.
type ProcessInfo struct {
	PID       int   `json:"pid"`
	Uptime    int64 `json:"uptime"`
	HeapUsed  int64 `json:"heapUsed"`
	HeapTotal int64 `json:"heapTotal"`
	RSS       int64 `json:"rss"`
}

type handlers struct {
	log   *kit.Logger
	db    *store.DB
	o     Options
	linux bool
	win   bool
}

// Register adds the group's routes.
func Register(d *kit.Deps) { RegisterWith(d, Options{}) }

// RegisterWith is Register with the commands, files and clock of the
// caller.
func RegisterWith(d *kit.Deps, o Options) {
	consoleDir, env := "", func(string) string { return "" }
	if d.Config != nil {
		consoleDir = d.Config.ConsoleDir
		if d.Config.Getenv != nil {
			env = d.Config.Getenv
		}
	}
	if o.DatabaseFile == "" {
		o.DatabaseFile = env("DB_PATH")
	}
	if o.DatabaseFile == "" {
		o.DatabaseFile = filepath.Join(consoleDir, "data", "auth.db")
	}
	if o.BackupDir == "" {
		o.BackupDir = filepath.Join(consoleDir, "data", "backups")
	}
	h := newHandlers(d.Log.Child("SYSTEM"), d.Store, o)
	r := d.Router
	// The host's process list, its logs, the table counts and the
	// containers show what other services run with: server.config. The
	// disks and the console's own process stay with metrics.view
	metrics, config := d.RequirePermission("metrics.view"), d.RequirePermission("server.config")
	r.Get("/api/system/info", h.info, d.RequireAuth, metrics)
	r.Get("/api/logs/recent", h.logs, d.Limits.API.Middleware, d.RequireAuth, config)
	r.Get("/api/database/stats", h.databaseStats, d.Limits.API.Middleware, d.RequireAuth, config)
	r.Get("/api/docker/containers", h.containers, d.RequireAuth, config)
	r.Post("/api/system/exec", h.exec, d.RequireAuth, config)
	r.Get("/api/speed-test", h.speedTest, d.RequireAuth, metrics)
}

func newHandlers(log *kit.Logger, db *store.DB, o Options) *handlers {
	if o.Run == nil {
		o.Run = runShell
	}
	if o.Platform == "" {
		o.Platform = runtime.GOOS
		if o.Platform == "windows" {
			o.Platform = "win32"
		}
	}
	if o.TotalMemory == nil {
		o.TotalMemory = totalMemory
	}
	if o.OpenFile == nil {
		o.OpenFile = func(path string) (io.ReadSeekCloser, error) { return os.Open(path) }
	}
	if o.Process == nil {
		started := time.Now()
		o.Process = func() ProcessInfo { return ownProcess(started) }
	}
	return &handlers{log: log, db: db, o: o, linux: o.Platform == "linux", win: o.Platform == "win32"}
}

// run is safeExec: what the command printed, trimmed, or "" when it
// failed.
func (h *handlers) run(ctx context.Context, command string, timeout time.Duration) string {
	out, err := h.o.Run(ctx, command, timeout)
	if err != nil {
		return ""
	}
	return trim(decodeUTF8([]byte(out)))
}

const megabyte = 1048576

// ownProcess reads the numbers of the running process.
func ownProcess(started time.Time) ProcessInfo {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	rss := residentBytes()
	if rss == 0 {
		rss = mem.Sys
	}
	round := func(bytes uint64) int64 { return int64((bytes + megabyte/2) / megabyte) }
	return ProcessInfo{
		PID:       os.Getpid(),
		Uptime:    int64(time.Since(started) / time.Second),
		HeapUsed:  round(mem.HeapAlloc),
		HeapTotal: round(mem.HeapSys),
		RSS:       round(rss),
	}
}
