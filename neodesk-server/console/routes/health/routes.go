// Package health answers the console's health check.
package health

import (
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	now func() time.Time
}

// Register adds the group's route.
func Register(d *kit.Deps) {
	h := &handlers{now: time.Now}
	d.Router.Get("/health", h.get)
}

type status struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// get answers GET /health for anyone: {"status":"ok","timestamp":<now>}.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, status{Status: "ok", Timestamp: httpx.ISOTime(h.now())})
}
