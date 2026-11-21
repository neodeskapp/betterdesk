// Package dashboard answers what the overview shows: the device counts
// and the server's health, the server status with its ports, the client
// configuration, the relay counters and the activity feed.
package dashboard

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

type handlers struct {
	d       *kit.Deps
	info    *serverinfo.Service
	backend *goapi.Backend
	log     *kit.Logger
	// started is when the console came up; the server status reports the
	// time since then.
	started time.Time
	now     func() time.Time
	// dial opens the connection of a port probe.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	// scope is the device scope of the account of a request, nil when it
	// sees every device
	scope func(r *http.Request) (*access.DeviceScope, error)
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	newHandlers(d).register(d.Router)
}

func newHandlers(d *kit.Deps) *handlers {
	dialer := &net.Dialer{}
	counts := deviceCounts{db: d.Store}
	if d.DeviceGroups != nil {
		counts.sync = d.DeviceGroups.SyncServerPeers
	}
	return &handlers{
		d:       d,
		info:    d.ServerInfo,
		backend: goapi.NewBackend(d.GoAPI, counts),
		log:     d.Log.Child("DASHBOARD"),
		started: time.Now(),
		now:     time.Now,
		dial:    dialer.DialContext,
		scope:   scopeOf(d),
	}
}

// scopeOf is the device scope of the account of a request, nil when it
// sees every device or when the console has no device groups.
func scopeOf(d *kit.Deps) func(r *http.Request) (*access.DeviceScope, error) {
	return func(r *http.Request) (*access.DeviceScope, error) {
		if d.DeviceGroups == nil || d.Backend == nil {
			return nil, nil
		}
		ctx := r.Context()
		return d.DeviceGroups.LimitedScope(ctx, kit.User(r), func() []*goapi.Object {
			return d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
		})
	}
}

func (h *handlers) register(r *kit.Router) {
	d := h.d
	r.Get("/api/stats", h.stats, d.RequireAuth)
	r.Get("/api/server/status", h.serverStatus, d.RequireAuth)
	r.Get("/api/dashboard/client-config", h.clientConfig, d.RequireAuth)
	r.Get("/api/server/bandwidth", h.bandwidth, d.RequireAuth)
	r.Post("/api/sync-status", h.syncStatus, d.RequireAuth)
	r.Get("/api/dashboard/activity", h.activity, d.RequireAuth, d.RequirePermission("audit.view"))
}

// serverError answers 500 with the request language's text for it.
func serverError(w http.ResponseWriter, r *http.Request) {
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

type statsData struct {
	Devices any           `json:"devices"`
	Hbbs    *goapi.Object `json:"hbbs"`
	// Backend names the server the console works with: always betterdesk.
	Backend   string `json:"backend"`
	PublicKey bool   `json:"publicKey"`
}

// stats answers GET /api/stats: the device counts, the server's health,
// the name of the backend and whether the server has a public key. An
// account limited to some devices gets the counts of those.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	scope, err := h.scope(r)
	var devices any
	switch {
	case err != nil:
	case scope != nil:
		devices = scopedCounts(h.d.GoAPI.GetAllPeers(r.Context(), false), scope)
	default:
		devices, err = h.backend.GetStats(r.Context())
	}
	if err != nil {
		h.log.Error("Stats error:", err)
		serverError(w, r)
		return
	}
	health := h.backend.GetHealth(r.Context())
	key := h.info.ResolvePublicKey(r.Context())
	kit.OK(w, r, statsData{Devices: devices, Hbbs: health, Backend: "betterdesk", PublicKey: key != ""})
}

type clientConfigData struct {
	serverinfo.ClientConfig
	ClientServerHost     string                     `json:"client_server_host"`
	EndpointSources      serverinfo.EndpointSources `json:"endpoint_sources"`
	EnvOverrideActive    bool                       `json:"env_override_active"`
	PhoneUnreachableHost bool                       `json:"phone_unreachable_host"`
	QR                   serverinfo.NullString      `json:"qr"`
}

// clientConfig answers GET /api/dashboard/client-config: what a client
// needs to reach this server, as fields, as the text and the link a
// client imports, and as a QR code. ?host= names the host to build them
// for when it is a valid one.
func (h *handlers) clientConfig(w http.ResponseWriter, r *http.Request) {
	endpoints := h.info.ResolveRustDeskEndpoints(serverinfo.RequestHostOf(r), serverinfo.QueryString(r, "host"))
	config := h.info.GetClientConfig(r.Context(), endpoints.Endpoints())
	code := h.info.GetServerConfigQR(r.Context(), endpoints.Endpoints())
	kit.OK(w, r, clientConfigData{
		ClientConfig:         config,
		ClientServerHost:     endpoints.Host,
		EndpointSources:      endpoints.Sources,
		EnvOverrideActive:    endpoints.EnvOverrideActive,
		PhoneUnreachableHost: serverinfo.IsPhoneUnreachableHost(endpoints.Host),
		QR:                   code,
	})
}

type bandwidthData struct {
	RelayActive      any `json:"relay_active"`
	TotalRelayed     any `json:"total_relayed"`
	BytesTransferred any `json:"bytes_transferred"`
	ActiveSessions   any `json:"active_sessions"`
	ThrottleHits     any `json:"throttle_hits"`
}

// bandwidth answers GET /api/server/bandwidth: the relay's counters from
// the server's statistics, all 0 when the server gives none.
func (h *handlers) bandwidth(w http.ResponseWriter, r *http.Request) {
	// A failed call has no data
	data := h.d.GoAPI.GetServerStats(r.Context()).Object("data")
	counter := func(key string) any {
		if value := data.Get(key); goapi.Truthy(value) {
			return value
		}
		return 0
	}
	kit.OK(w, r, bandwidthData{
		RelayActive:      counter("relay_active_sessions"),
		TotalRelayed:     counter("relay_total_relayed"),
		BytesTransferred: counter("bandwidth_bytes_transferred"),
		ActiveSessions:   counter("bandwidth_active_sessions"),
		ThrottleHits:     counter("bandwidth_throttle_hits"),
	})
}

// syncStatus answers POST /api/sync-status. The server keeps the online
// state itself, so nothing is synced and the answer says so.
func (h *handlers) syncStatus(w http.ResponseWriter, r *http.Request) {
	kit.OK(w, r, h.backend.SyncOnlineStatus())
}
