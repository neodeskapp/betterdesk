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

	"github.com/unitronix/betterdesk-server/console/backup"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/scheduledbackup"
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

// Register adds the group's routes and the scheduled backup job.
func Register(d *kit.Deps) {
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
	backups := backup.New(backup.Options{
		Store:      d.Store,
		Branding:   b,
		Groups:     groups,
		GoAPI:      api,
		DataDir:    cfg.DataDir,
		EnvPath:    cfg.EnvPath,
		KeysPath:   cfg.KeysPath,
		PubKeyPath: cfg.PubKeyPath,
		APIKeyPath: cfg.APIKeyPath,
		DBPath:     cfg.DBPath,
		AppVersion: cfg.Version,
		Limits:     archiveLimits(cfg.Getenv, d.Log.Child("SETTINGS").Warn),
	})
	scheduled := scheduledbackup.New(scheduledbackup.Options{
		Store:   d.Store,
		Backups: backups,
		DataDir: cfg.DataDir,
		AppDir:  cfg.ConsoleDir,
	})
	register(d, backups, scheduled)
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
	r.Get("/api/settings/backup", h.download, auth, config)
	r.Get("/api/settings/backup/schedule", h.schedule, auth, config)
	r.Get("/api/settings/backup/schedule/status", h.scheduleStatus, auth, config)
	r.Put("/api/settings/backup/schedule", h.saveSchedule, auth, config)
	r.Post("/api/settings/backup/schedule/run", h.runSchedule, auth, config)
	r.Post("/api/settings/backup/destination/test", h.testDestination, auth, config)
	r.Get("/api/settings/backup/smb", h.smb, auth, config)
	r.Put("/api/settings/backup/smb", h.saveSMB, auth, config)
	r.Post("/api/settings/backup/smb/test", h.testSMB, auth, config)
	r.Post("/api/settings/restore", h.restore, auth, config)
	r.Get("/api/settings/backup/retention", h.retention, auth, config)
	r.Put("/api/settings/backup/retention", h.saveRetention, auth, config)
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
