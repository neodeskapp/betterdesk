// Package scaling keeps the console's list of relay nodes and the rules
// that assign devices to them, and answers the heartbeat of a relay.
//
// The list is an inventory for operators. The server does not route by
// it: which relays clients use is decided by the server's own settings.
// Both lists are JSON arrays in the settings table (scaling_relay_nodes,
// scaling_assignment_rules).
package scaling

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Options are what the group takes besides the console's services.
type Options struct {
	// Now is the clock. nil means time.Now.
	Now func() time.Time
	// Random is the source of new ids. nil means crypto/rand.
	Random io.Reader
}

type handlers struct {
	log    *kit.Logger
	db     *store.DB
	now    func() time.Time
	random io.Reader
}

// Register adds the group's routes below the mount of d. The old console
// mounted the group at the root and at /api/bd, so every route answers at
// both: /api/panel/scaling/relays and /api/bd/api/panel/scaling/relays.
func Register(d *kit.Deps) { RegisterWith(d, Options{}) }

// RegisterWith is Register with the clock and the random source of the
// caller.
func RegisterWith(d *kit.Deps, o Options) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	h := &handlers{log: d.Log.Child("SCALING"), db: d.Store, now: o.Now, random: o.Random}
	r := d.Router
	admin := []kit.Middleware{d.RequireAuth, d.RequirePermission("server.config")}
	r.Get("/api/panel/scaling/relays", h.listRelays, admin...)
	r.Get("/api/panel/scaling/relays/{nodeId}", h.getRelay, admin...)
	r.Post("/api/panel/scaling/relays", h.createRelay, admin...)
	r.Put("/api/panel/scaling/relays/{nodeId}", h.updateRelay, admin...)
	r.Delete("/api/panel/scaling/relays/{nodeId}", h.deleteRelay, admin...)
	r.Get("/api/panel/scaling/relays/{nodeId}/health", h.relayHealth, admin...)
	r.Get("/api/panel/scaling/relays/{nodeId}/metrics", h.relayMetrics, admin...)
	r.Get("/api/panel/scaling/capacity", h.capacity, admin...)
	r.Get("/api/panel/scaling/rules", h.listRules, admin...)
	r.Post("/api/panel/scaling/rules", h.createRule, admin...)
	r.Put("/api/panel/scaling/rules/{ruleId}", h.updateRule, admin...)
	r.Delete("/api/panel/scaling/rules/{ruleId}", h.deleteRule, admin...)
	r.Post("/api/bd/scaling/relay-heartbeat", h.heartbeat)
}

// The answers of this group have no "success" member.

type dataAnswer struct {
	Data any `json:"data"`
}

type errorAnswer struct {
	Error string `json:"error"`
}

type okAnswer struct {
	OK bool `json:"ok"`
}

type noteAnswer struct {
	Data any    `json:"data"`
	Note string `json:"note,omitempty"`
}

type heartbeatAnswer struct {
	OK   bool   `json:"ok"`
	Note string `json:"note"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, errorAnswer{message})
}

// newID is "r-" and sixteen hex digits.
func (h *handlers) newID() (string, error) {
	raw := make([]byte, 8)
	if _, err := io.ReadFull(h.random, raw); err != nil {
		return "", err
	}
	return "r-" + hex.EncodeToString(raw), nil
}

// isoNow is new Date().toISOString().
func (h *handlers) isoNow() string {
	return h.now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// heartbeat answers POST /api/bd/scaling/relay-heartbeat for anyone, with
// or without a session: 202, and nothing is stored (BUGS.md I4).
func (h *handlers) heartbeat(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusAccepted, heartbeatAnswer{OK: true, Note: "relay heartbeat acknowledged but not stored"})
}
