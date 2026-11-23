// Package settingsbackup holds the backup routes of the settings area:
// the full backup as a download, its scheduled form with destination and
// SMB checks, the restore of an uploaded backup and the retention count
// of the updater's backups. Every route needs a session and server.config.
//
// The services are packages backup and scheduledbackup; the scheduled
// backup's minute timer is the job "scheduled backups".
package settingsbackup

import (
	"net/http"
	"path/filepath"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/backup"
	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/scheduledbackup"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// maxUpload is the largest backup a restore takes, 200 MB.
const maxUpload = 200 * 1024 * 1024

type handlers struct {
	d         *kit.Deps
	log       *kit.Logger
	backups   *backup.Service
	scheduled *scheduledbackup.Service
	getenv    func(string) string
}

// Register adds the group's routes and the scheduled backup job, on a
// backup service of their own that takes the server's database from
// DB_PATH (NewBackups without the server's DSN).
func Register(d *kit.Deps) { RegisterWith(d, NewBackups(d, "")) }

// RegisterWith adds the group's routes and the scheduled backup job on
// backups, the service NewBackups built.
func RegisterWith(d *kit.Deps, backups *backup.Service) {
	cfg := d.Config
	scheduled := scheduledbackup.New(scheduledbackup.Options{
		Store:   d.Store,
		Backups: backups,
		DataDir: cfg.DataDir,
		AppDir:  cfg.ConsoleDir,
	})
	register(d, backups, scheduled)
}

// NewBackups builds the console's backup service from its services and
// configuration. serverDB is the server's database DSN (its -db flag or
// DB_URL), the file a full backup holds and a restore of the server's
// database is left next to; empty takes DB_PATH. The services it reads
// from d (Store, Branding, DeviceGroups, GoAPI, Avatars, Log) must be set.
func NewBackups(d *kit.Deps, serverDB string) *backup.Service {
	cfg := d.Config
	var b backup.Branding
	if d.Branding != nil {
		b = d.Branding
	}
	var groups backup.Groups
	if d.DeviceGroups != nil {
		groups = d.DeviceGroups
	}
	var api backup.GoServer
	if d.GoAPI != nil {
		api = d.GoAPI
	}
	var pictures backup.Avatars
	if d.Avatars != nil {
		pictures = d.Avatars
	}
	o := FileOptions(cfg, serverDB)
	o.Store, o.Branding, o.Groups, o.GoAPI, o.Avatars = d.Store, b, groups, api, pictures
	o.Limits = archiveLimits(cfg.Getenv, d.Log.Child("SETTINGS").Warn)
	return backup.New(o)
}

// FileOptions are the files and folders of the console's backup by its
// configuration, and the server's database DSN as for NewBackups: what a
// full backup holds besides the database. The console's backup service
// and neodesk-server backup both take them, so both make the same
// archive.
func FileOptions(cfg *config.Config, serverDB string) backup.Options {
	return backup.Options{
		DataDir:    cfg.DataDir,
		EnvPath:    cfg.EnvPath,
		KeysPath:   cfg.KeysPath,
		PubKeyPath: cfg.PubKeyPath,
		APIKeyPath: cfg.APIKeyPath,
		DBPath:     cfg.DBPath,
		ServerDB:   serverDB,
		// the folder of the chat routes (chat.FilesDir)
		ChatFilesDir: filepath.Join(cfg.ConsoleDir, "data", "chat-files"),
		// the folder of the ticket routes (tickets.uploadsDir)
		AttachmentsDir: attachmentsDir(cfg),
		FontsDir:       cfg.FontsDir,
		CertFile:       cfg.CertFile,
		KeyFile:        cfg.KeyFile,
		CAFile:         cfg.CAFile,
		EndpointsFile:  filepath.Join(cfg.DataDir, serverinfo.DurableBasename),
		AppVersion:     cfg.Version,
	}
}

// attachmentsDir is the folder of the ticket attachments, as the ticket
// routes pick it: data/attachments in CONSOLE_DIR, in DATA_DIR when there
// is no CONSOLE_DIR.
func attachmentsDir(cfg *config.Config) string {
	if cfg.ConsoleDir != "" {
		return filepath.Join(cfg.ConsoleDir, "data", "attachments")
	}
	return filepath.Join(cfg.DataDir, "attachments")
}

// archiveLimits are the limits of an uploaded archive, read once at the
// start: BACKUP_MAX_DECOMPRESSED_BYTES and BACKUP_MAX_ENTRIES as whole
// numbers of at least 1 (backup.EnvLimit), the default for what is unset.
// A value that is no such number gets the default and a warning, so a
// limit such as -1 does not refuse every restore.
func archiveLimits(getenv func(string) string, warn func(args ...any)) backup.Limits {
	if getenv == nil {
		return backup.Limits{}
	}
	read := func(name string) float64 {
		limit, ok := backup.EnvLimit(getenv(name))
		if !ok {
			warn(name + " is not a whole number of at least 1, the default limit holds")
		}
		return limit
	}
	return backup.Limits{
		MaxBytes:   read("BACKUP_MAX_DECOMPRESSED_BYTES"),
		MaxEntries: read("BACKUP_MAX_ENTRIES"),
	}
}

func register(d *kit.Deps, backups *backup.Service, scheduled *scheduledbackup.Service) {
	h := &handlers{d: d, log: d.Log.Child("SETTINGS"), backups: backups, scheduled: scheduled, getenv: d.Config.Getenv}
	if h.getenv == nil {
		h.getenv = func(string) string { return "" }
	}
	if d.Jobs != nil {
		d.Jobs.Add("scheduled backups", scheduled.Run)
	}
	auth, config := d.RequireAuth, d.RequirePermission("server.config")
	r := d.Router
	r.Get("/api/settings/backup/stats", h.stats, auth, config)
	r.Get("/api/settings/backup", h.download, auth, config, onlySuperAdmins)
	r.Get("/api/settings/backup/schedule", h.schedule, auth, config)
	r.Get("/api/settings/backup/schedule/status", h.scheduleStatus, auth, config)
	r.Put("/api/settings/backup/schedule", h.saveSchedule, auth, config, onlySuperAdmins)
	r.Post("/api/settings/backup/schedule/run", h.runSchedule, auth, config, onlySuperAdmins)
	r.Post("/api/settings/backup/destination/test", h.testDestination, auth, config, onlySuperAdmins)
	r.Post("/api/settings/restore", h.restore, auth, config, onlySuperAdmins)
	r.Get("/api/settings/backup/retention", h.retention, auth, config)
	r.Put("/api/settings/backup/retention", h.saveRetention, auth, config)
}

// onlySuperAdmins lets through the roles that hold every permission. A
// full backup holds every secret of the server (the database with the
// token signing secret, password hashes and API keys, the key pair, the
// API key file): downloading, restoring or sending one elsewhere would
// lift a server admin with server.config above its role (L387).
func onlySuperAdmins(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !access.IsSuperAdminRole(kit.Access(r).Role) {
			kit.Fail(w, r, http.StatusForbidden, "Only a super admin can download, restore or send a full backup: it holds every secret of the server")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// body is req.body || {} in the values of package branding.
func body(r *http.Request) any {
	v, err := branding.ParseJSON(middleware.BodyValue(r).Raw())
	if err != nil || !branding.Truthy(v) {
		return branding.NewObject()
	}
	return v
}

// writeJS answers with JSON made by JSON.stringify.
func writeJS(w http.ResponseWriter, r *http.Request, status int, v any) {
	httpx.WriteJSONBytes(w, r, status, []byte(branding.Stringify(v)))
}

// success is {"success":true,"data":<data>} with data as JSON text.
func success(data string) string { return `{"success":true,"data":` + data + `}` }

func writeData(w http.ResponseWriter, r *http.Request, data string) {
	httpx.WriteJSONBytes(w, r, http.StatusOK, []byte(success(data)))
}

func (h *handlers) serverError(w http.ResponseWriter, r *http.Request) {
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}
