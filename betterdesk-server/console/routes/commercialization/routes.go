// Package commercialization holds the routes of the billing pages: the
// server's clock and its time sync, billing packages, contracts,
// sessions, work reports and their exports, currencies, the help request
// mail settings and the SMTP status. Most routes pass the call on to the
// API server and answer with what it said; the clock settings are the
// console's own (package billingclock), and so is the mail configuration
// (package helpmail).
package commercialization

import (
	"context"
	"net/http"
	"runtime"

	"github.com/unitronix/betterdesk-server/console/billingclock"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/servicedef"
)

type handlers struct {
	d     *kit.Deps
	api   *goapi.Client
	log   *kit.Logger
	clock *billingclock.Service
}

// Register adds the group's routes, in the order of the Node file, and
// the rollback of a clock save to the restart transaction.
func Register(d *kit.Deps) {
	h := &handlers{d: d, api: d.GoAPI, log: d.Log, clock: NewClock(d)}
	if d.Restart != nil {
		d.Restart.SetRollback("billing-clock", h.clock.Rollback)
	}
	r := d.Router
	auth := d.RequireAuth
	server := d.RequirePermission("server.config")
	view := d.RequirePermission("billing.view")
	manage := d.RequirePermission("billing.manage")
	reports := d.RequirePermission("billing.reports")
	export := d.RequirePermission("billing.export")

	r.Get("/api/panel/billing/timesync/status", h.pass("GET", "/timesync/status"), auth, h.requireClockRead)
	r.Post("/api/panel/billing/timesync/check", h.pass("POST", "/timesync/check"), auth, server)
	r.Get("/api/panel/billing/clock/settings", h.clockSettings, auth, h.requireClockRead)
	r.Put("/api/panel/billing/clock/settings", h.saveClockSettings, auth, server)
	r.Get("/api/panel/billing/clock/detect-timezone", h.detectTimezone, auth, h.requireClockRead)

	r.Get("/api/panel/billing/packages", h.pass("GET", "/billing/packages"), auth, view)
	r.Get("/api/panel/billing/packages/{packageId}/preview", h.withQuery("/billing/packages/{packageId}/preview"), auth, view)
	r.Post("/api/panel/billing/packages", h.passBody("POST", "/billing/packages"), auth, manage)
	r.Put("/api/panel/billing/packages/{packageId}", h.passBody("PUT", "/billing/packages/{packageId}"), auth, manage)
	r.Delete("/api/panel/billing/packages/{packageId}", h.pass("DELETE", "/billing/packages/{packageId}"), auth, manage)

	r.Get("/api/panel/billing/contracts", h.withQuery("/billing/contracts"), auth, view)
	r.Post("/api/panel/billing/contracts", h.passBody("POST", "/billing/contracts"), auth, manage)
	r.Put("/api/panel/billing/contracts/{contractId}", h.passBody("PUT", "/billing/contracts/{contractId}"), auth, manage)
	r.Delete("/api/panel/billing/contracts/{contractId}", h.pass("DELETE", "/billing/contracts/{contractId}"), auth, manage)

	r.Get("/api/panel/billing/stats", h.pass("GET", "/billing/stats"), auth, view)
	r.Get("/api/panel/billing/sessions", h.withQuery("/billing/sessions"), auth, view)
	r.Get("/api/panel/billing/check", h.withQuery("/billing/check"), auth, d.RequirePermission("device.connect"))
	r.Get("/api/panel/billing/sessions/pending", h.withQuery("/billing/sessions/pending"), auth, reports)
	r.Get("/api/panel/billing/sessions/export", h.export("/billing/sessions/export"), auth, export)
	r.Get("/api/panel/billing/reports", h.withQuery("/billing/reports"), auth, view)
	r.Get("/api/panel/billing/reports/export", h.export("/billing/reports/export"), auth, export)
	r.Post("/api/panel/billing/sessions/{sessionId}/report", h.passBody("POST", "/billing/sessions/{sessionId}/report"), auth, reports)
	r.Get("/api/panel/billing/currencies", h.pass("GET", "/billing/currencies"), auth, view)
	r.Put("/api/panel/billing/currencies/{currencyCode}", h.passBody("PUT", "/billing/currencies/{currencyCode}"), auth, manage)

	r.Get("/api/panel/commercialization/email-config", h.emailConfig, auth, view)
	r.Put("/api/panel/commercialization/email-config", h.saveEmailConfig, auth, manage)
	r.Get("/api/panel/commercialization/smtp-status", h.smtpStatus, auth, view)
}

// NewClock is the billing clock service of a console: the .env file of
// its configuration, and the server's service definition on this
// machine.
func NewClock(d *kit.Deps) *billingclock.Service {
	opts := billingclock.Options{GoAPI: d.GoAPI, Linux: runtime.GOOS == "linux"}
	if cfg := d.Config; cfg != nil {
		opts.EnvPath, opts.Docker, opts.Getenv = cfg.EnvPath, cfg.Docker, cfg.Getenv
	}
	windows := runtime.GOOS == "windows"
	service := servicedef.New(servicedef.Options{
		Windows:        windows,
		ServerService:  serviceName(windows, "BetterDeskServer", "betterdesk-server"),
		ConsoleService: serviceName(windows, "BetterDeskConsole", "betterdesk-console"),
		ConsoleEnvPath: opts.EnvPath,
	})
	opts.ServiceConfig = func(ctx context.Context) any { return service.Check(ctx) }
	return billingclock.New(opts)
}

func serviceName(windows bool, onWindows, elsewhere string) string {
	if windows {
		return onWindows
	}
	return elsewhere
}

// requireClockRead lets the clock be read with server.config (the time
// zone settings) or billing.view (the billing pages): the guard of the
// permission the account has, else the guard of billing.view.
func (h *handlers) requireClockRead(next http.Handler) http.Handler {
	server := h.d.RequirePermission("server.config")(next)
	billing := h.d.RequirePermission("billing.view")(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kit.HasPermission(r, "server.config") {
			server.ServeHTTP(w, r)
			return
		}
		billing.ServeHTTP(w, r)
	})
}
