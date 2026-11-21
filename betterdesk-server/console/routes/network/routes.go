// Package network holds the network monitoring routes: the targets to
// watch, their checks and history, the tools for a single ping, TCP, HTTP
// or DNS check, and the switch of the monitor that polls the targets.
//
// Reading targets, history and the monitor's state needs device.view;
// running a check or a tool needs device.connect; changing targets and
// starting or stopping the monitor needs server.config.
//
// The members of a target are checked before anything is stored; a
// refusal is 400 with a message that names the member.
package network

import (
	"context"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
	"github.com/unitronix/betterdesk-server/console/store"
)

type handlers struct {
	db      *store.DB
	monitor *netmonitor.Monitor
	log     *kit.Logger
}

// Register adds the group's routes below the mount (/api/network) and the
// monitor's polling loop as a background job. The loop does nothing until
// somebody starts the monitor through POST /monitor/start, or until the
// console starts again after it ran.
func Register(d *kit.Deps) {
	opts := netmonitor.Options{
		Store:                d.Store,
		AllowSelfSignedCerts: d.Config != nil && d.Config.AllowSelfSignedCerts,
	}
	if d.Log != nil {
		opts.Log = d.Log
	}
	register(d, netmonitor.New(opts))
}

func register(d *kit.Deps, monitor *netmonitor.Monitor) {
	h := &handlers{db: d.Store, monitor: monitor, log: d.Log}
	if d.Jobs != nil {
		d.Jobs.Add("network monitor", monitor.Run)
		h.resumeMonitor()
	}
	view := d.RequirePermission("device.view")
	connect := d.RequirePermission("device.connect")
	config := d.RequirePermission("server.config")
	r := d.Router

	r.Get("/targets", h.listTargets, requireSession, view)
	r.Post("/targets", h.createTarget, requireSession, config)
	r.Get("/targets/{id}", h.getTarget, requireSession, view)
	r.Patch("/targets/{id}", h.updateTarget, requireSession, config)
	r.Delete("/targets/{id}", h.deleteTarget, requireSession, config)

	// A check connects from the server, as the four tools below do: it is
	// a toolkit route (device.connect), where a write to the network
	// section asks server.config
	r.Section("toolkit").Post("/targets/{id}/check", h.checkTarget, requireSession, connect)
	r.Get("/targets/{id}/history", h.history, requireSession, view)
	r.Get("/stats", h.stats, requireSession, view)

	r.Post("/ping", h.ping, requireSession, connect)
	r.Post("/tcp", h.tcp, requireSession, connect)
	r.Post("/http", h.httpCheck, requireSession, connect)
	r.Post("/resolve", h.resolve, requireSession, connect)

	r.Post("/monitor/start", h.startMonitor, requireSession, config)
	r.Post("/monitor/stop", h.stopMonitor, requireSession, config)
	r.Get("/monitor/status", h.monitorStatus, requireSession, view)
}

// failure is the error answer of this group: {"error": message}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// requireSession lets a request with a signed-in account through and
// answers 401 otherwise.
func requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kit.User(r) == nil {
			fail(w, r, http.StatusUnauthorized, "Authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// detached is the context of a check or a write that goes on when the
// client has gone, as the old routes did.
func detached(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}
