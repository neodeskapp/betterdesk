// Package serverattestation answers the routes of the server attestation
// (routes/server-attestation.routes.js): the state of the capacity test,
// starting and aborting one, its last result, the badge texts, and the
// public badge for the sign-in page. The test itself is in package
// console/attestation.
package serverattestation

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/attestation"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// permission is what every route but the public one needs.
const permission = "server.attestation"

type handlers struct {
	svc *attestation.Service
	log *kit.Logger
	// translate is req.t for the badge texts
	translate func(r *http.Request) attestation.Translate
	// brand is brandingService.getBranding().appName
	brand func(r *http.Request) any
	// logAction is db.logAction(req.session.userId, action, details, req.ip)
	logAction func(r *http.Request, action, details string) error
}

// Register adds the group's routes and the job that runs the capacity
// tests.
func Register(d *kit.Deps) {
	svc := attestation.New(Options(d))
	if d.Jobs != nil {
		d.Jobs.Add("server attestation runs", svc.Serve)
	}
	RegisterWith(d, svc)
}

// RegisterWith adds the group's routes for a service the caller made.
func RegisterWith(d *kit.Deps, svc *attestation.Service) {
	h := &handlers{
		svc: svc,
		log: d.Log.Child("ServerAttestation"),
		translate: func(r *http.Request) attestation.Translate {
			return func(key string) string { return kit.T(r, key, nil) }
		},
		brand: func(r *http.Request) any {
			d.Branding.Refresh(r.Context())
			return branding.Field(d.Branding.GetBranding(), "appName")
		},
		logAction: d.LogAction,
	}
	auth := []kit.Middleware{d.RequireAuth, d.RequirePermission(permission)}
	r := d.Router
	r.Get("/api/server-attestation/status", h.status, auth...)
	r.Post("/api/server-attestation/run", h.run, auth...)
	r.Post("/api/server-attestation/abort", h.abort, auth...)
	r.Get("/api/server-attestation/result", h.result, auth...)
	r.Get("/api/server-attestation/badge-config", h.badgeConfig, auth...)
	r.Put("/api/server-attestation/badge-config", h.saveBadgeConfig, auth...)
	r.Get("/api/public/server-attestation", h.public)
}

// Options are the service's settings from the console's configuration
// and its connections to the database and the API server.
func Options(d *kit.Deps) attestation.Options {
	env := func(string) string { return "" }
	o := attestation.Options{Settings: attestation.StoreSettings(d.Store)}
	if cfg := d.Config; cfg != nil {
		if cfg.Getenv != nil {
			env = cfg.Getenv
		}
		o.Network = attestation.Network{
			SignalHostEnv:   env("WS_HBBS_HOST"),
			RelayHostEnv:    env("WS_HBBR_HOST"),
			APIURL:          cfg.APIURL,
			ProxySignalHost: cfg.SignalHost,
			SignalPort:      cfg.SignalPort,
			RelayPort:       cfg.RelayPort,
			TLS:             strings.ToLower(env("WS_USE_TLS")) == "true",
			AllowSelfSigned: cfg.AllowSelfSignedCerts,
		}
	}
	if d.GoAPI != nil {
		o.ServerStats = func(ctx context.Context) any { return serverStats(d.GoAPI.GetServerStats(ctx)) }
	}
	if d.Log != nil {
		log := d.Log.Child("ServerAttestation")
		o.Logf = func(format string, args ...any) { log.Error(fmt.Sprintf(format, args...)) }
	}
	return o
}

// serverStats is res.success ? res.data : null for an answer of
// getServerStats.
func serverStats(res *goapi.Object) any {
	if res == nil || !goapi.Truthy(res.Get("success")) {
		return nil
	}
	if !res.Has("data") {
		return branding.Undefined
	}
	return res.Get("data")
}
