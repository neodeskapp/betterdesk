// Package settingsupdates holds the update and management routes of the
// settings area (routes/settings.routes.js): the update check and
// install, the update backups, the update channel, and the report and
// configuration of the install. The work is console/updater's and
// console/managedconfig's; the answers keep the old console's shapes,
// and the fields about the old console's own files and the Go toolchain
// get the values of DECISIONS A29.
package settingsupdates

import (
	"context"
	"errors"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/managedconfig"
)

// exitDelay is how long an install, a repair or a restore waits after its
// answer before the process ends (the old routes' two seconds).
const exitDelay = 2 * time.Second

type handlers struct {
	d       *kit.Deps
	log     *kit.Logger
	managed *managedconfig.Config
}

// Register adds the group's routes, in the order of the Node file, and
// the rollback of a saved managed configuration to the restart
// coordinator.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("SETTINGS")}
	if d.Config != nil {
		getenv := d.Config.Getenv
		if getenv == nil {
			getenv = func(string) string { return "" }
		}
		h.managed = managedconfig.New(managedconfig.Options{
			ConsoleDir: d.Config.ConsoleDir, EnvFile: getenv("BETTERDESK_ENV_FILE"), Getenv: getenv,
		})
	} else {
		h.managed = managedconfig.New(managedconfig.Options{})
	}
	if d.Restart != nil {
		d.Restart.SetRollback("env-config", h.rollbackEnv)
	}
	r := d.Router
	server := d.RequirePermission("server.config")
	r.Get("/api/settings/updates/server-info", h.serverInfo, d.RequireAuth, server)
	r.Post("/api/settings/updates/install-go", h.installGo, d.RequireAuth, server)
	r.Get("/api/settings/updates/preflight", h.preflight, d.RequireAuth, server)
	r.Get("/api/settings/management/capabilities", h.capabilities, d.RequireAuth, server)
	r.Get("/api/settings/management/config", h.getConfig, d.RequireAuth, server)
	r.Put("/api/settings/management/config", h.putConfig, d.RequireAuth, server)
	r.Get("/api/settings/updates/channel", h.getChannel, d.RequireAuth, server)
	r.Post("/api/settings/updates/channel", h.setChannel, d.RequireAuth, server)
	r.Get("/api/settings/updates/check", h.check, d.RequireAuth, server)
	r.Get("/api/settings/updates/last-result", h.lastResult, d.RequireAuth, server)
	r.Get("/api/settings/updates/install-status", h.installStatus, d.RequireAuth, server)
	r.Get("/api/settings/updates/changes", h.changes, d.RequireAuth, server)
	r.Post("/api/settings/updates/install", h.install, d.RequireAuth, server)
	r.Get("/api/settings/updates/server-binary/status", h.binaryStatus, d.RequireAuth, server)
	r.Post("/api/settings/updates/server-binary/rebuild", h.rebuild, d.RequireAuth, server)
	r.Get("/api/settings/updates/backups", h.listBackups, d.RequireAuth, server)
	r.Post("/api/settings/updates/restore", h.restore, d.RequireAuth, server)
	r.Delete("/api/settings/updates/backups/{name}", h.deleteBackup, d.RequireAuth, server)
	r.Post("/api/settings/updates/backups/prune", h.pruneBackups, d.RequireAuth, server)
}

// rollbackEnv undoes a saved managed configuration for the restart
// transaction: the backup the save made goes back over the env file. The
// env file is the one configured now; the descriptor's own paths are not
// used, so nothing the transaction file says becomes a path to write.
func (h *handlers) rollbackEnv(_ context.Context, descriptor *goapi.Object) error {
	backup, ok := descriptor.Get("backupPath").(string)
	if !ok {
		return errors.New("Invalid environment backup path")
	}
	return h.managed.RestoreBackup(backup)
}

// answer is {success, data}.
type answer struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

// failure is {success: false, error}.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// failureCode is {success: false, error, code}, code left out when empty.
type failureCode struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}
