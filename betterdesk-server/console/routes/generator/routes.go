// Package generator holds the routes of the support client generator
// (routes/generator.routes.js): the old /generator address, the template
// module and the client builder (/api/generator/module/...), the bundle
// records and their builds (/api/generator/bundles...), the public
// download portal of a bundle (/api/d/{publicId}/...) and the old
// rustdesk.toml generator. The work is done by package console/generator.
//
// The bundle and preview routes were meant to take JSON of up to 16 MB
// (a logo in base64). The console's own JSON parser reads every body
// first and stops at 2 MB, so the larger limit never applied (BUGS M1);
// these routes add no parser of their own.
package generator

import (
	"context"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/branding"
	gen "github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/webui"
)

type handlers struct {
	gen        *gen.Service
	serverInfo *serverinfo.Service
	branding   *branding.Service
	apiURL     string
	dataDir    string
	artifacts  *httpx.Static
	errors     *webui.Host
	log        *kit.Logger
}

// Register adds the group's routes in the order of the Node file, and,
// when no Node console runs behind this one, the build worker.
func Register(d *kit.Deps) {
	h := &handlers{
		gen:        d.Generator,
		serverInfo: d.ServerInfo,
		branding:   d.Branding,
		apiURL:     d.Config.APIURL,
		dataDir:    d.Config.DataDir,
		errors:     d.WebUI,
		log:        d.Log.Child("generator"),
	}
	h.artifacts = httpx.NewStatic(d.Config.AgentArtifactDir, httpx.StaticOptions{
		NoIndex:       true,
		NoFallthrough: true,
		Dotfiles:      "allow",
		SetHeaders:    attachment,
		OnError: func(w http.ResponseWriter, r *http.Request, status int, err error) {
			h.errors.ServerError(w, r, err, status)
		},
	})
	admin := []kit.Middleware{d.RequireAuth, d.RequireAdmin}
	r := d.Router
	r.Get("/generator", h.oldAddress)

	r.Get("/api/generator/module/status", h.moduleStatus, admin...)
	r.Post("/api/generator/module/accept-terms", h.acceptTerms, admin...)
	r.Post("/api/generator/module/install", h.install, admin...)
	r.Post("/api/generator/module/install-local", h.installLocal, admin...)

	r.Get("/api/generator/bundles", h.listBundles, admin...)
	r.Get("/api/generator/bundles/{bundleId}", h.getBundle, admin...)
	r.Get("/api/generator/defaults", h.defaults, admin...)
	r.Post("/api/generator/bundles", h.createBundle, admin...)
	r.Put("/api/generator/bundles/{bundleId}", h.updateBundle, admin...)
	r.Post("/api/generator/bundles/{bundleId}/rebuild", h.rebuild, admin...)
	r.Post("/api/generator/bundles/{bundleId}/rebuild/{platform}/{arch}/{format}", h.rebuildPlatform, admin...)
	r.Get("/api/generator/build-status", h.buildStatus, admin...)
	r.Post("/api/generator/bundles/{bundleId}/revoke", h.revoke, admin...)
	r.Delete("/api/generator/bundles/{bundleId}", h.deleteBundle, admin...)
	r.Post("/api/generator/preview", h.preview, admin...)
	r.Get("/api/generator/platforms", h.platforms, admin...)

	r.Get("/api/d/{publicId}/manifest", h.manifest)
	r.Get("/api/d/{publicId}/page", h.page)
	r.Get("/api/d/{publicId}/download/{platform}/{arch}/{format}", h.download)

	r.Get("/api/generator/config", h.legacyConfig, d.RequireAuth)
	r.Post("/api/generator/generate-config", h.generateConfig, d.RequireAuth)

	if d.Config.NodeUpstream == nil && d.Config.AgentBuildWorker {
		d.Jobs.Add("client build worker", h.runWorker)
	}
}

// runWorker is the start-up hook of the old console: the build worker,
// and in embedded mode a sync of the client source that queues new
// generations when the commit changed.
//
// It runs only when no Node console runs behind this one. That console
// starts its own worker on the same queue table and artifact folder, and
// a worker claims a queued build with a read followed by a write, which
// two workers can both win.
func (h *handlers) runWorker(ctx context.Context) {
	if h.gen.Embedded() {
		go func() {
			result, err := h.gen.SyncAndQueueRebuilds(ctx, false)
			if err != nil {
				h.log.Warn("BetterDesk-Client sync/rebuild deferred:", err.Error())
				return
			}
			if result.Get("queued") == true {
				h.log.Info("BetterDesk-Client " + result.Str("sha") + " changed; embedded Support rebuild queued")
			}
		}()
	}
	h.gen.Run(ctx)
}

// oldAddress answers GET /generator: the agents page replaced it.
func (h *handlers) oldAddress(w http.ResponseWriter, r *http.Request) {
	httpx.Redirect(w, r, http.StatusMovedPermanently, "/agents")
}

// attachment is the Content-Disposition res.download set.
func attachment(w http.ResponseWriter, filePath string, _ fs.FileInfo) {
	w.Header().Set("Content-Disposition", contentDisposition(filepath.Base(filePath)))
}
