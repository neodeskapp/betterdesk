package settings

import (
	"net/http"
	"runtime"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// noAPIKey stands in for the masked API key when there is none: eight
// bullets.
const noAPIKey = "••••••••"

type infoData struct {
	App      appInfo     `json:"app"`
	Server   processInfo `json:"server"`
	GoServer any         `json:"goServer"`
	Backend  string      `json:"backend"`
	Paths    pathsInfo   `json:"paths"`
	Stats    any         `json:"stats"`
}

type appInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// NodeVersion names the runtime of the console: the Go version.
	NodeVersion string `json:"nodeVersion"`
	Env         string `json:"env"`
}

type processInfo struct {
	Hostname    string `json:"hostname"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Uptime      int64  `json:"uptime"`
	MemoryUsage uint64 `json:"memoryUsage"`
}

type pathsInfo struct {
	Database  string `json:"database"`
	PublicKey string `json:"publicKey"`
	APIKey    string `json:"apiKey"`
}

// info answers GET /api/settings/info: the console's version and process,
// the server's health, the database and key files, and the device counts.
func (g *general) info(w http.ResponseWriter, r *http.Request) {
	health := g.d.Backend.GetHealth(r.Context())
	stats, err := g.d.DeviceGroups.PeerStats(r.Context())
	if err != nil {
		g.serverError(w, r, "Get server info error", err)
		return
	}
	cfg := g.d.Config
	kit.OK(w, r, infoData{
		App: appInfo{Name: "BetterDesk Console", Version: g.version, NodeVersion: runtime.Version(), Env: cfg.NodeEnv},
		Server: processInfo{
			Hostname: hostname(), Platform: nodePlatform(), Arch: nodeArch(),
			Uptime: int64(time.Since(g.started) / time.Second), MemoryUsage: heapUsed(),
		},
		GoServer: health,
		Backend:  g.d.Backend.GetActiveBackend(),
		Paths:    pathsInfo{Database: cfg.DBPath, PublicKey: cfg.PubKeyPath, APIKey: cfg.APIKeyPath},
		Stats:    stats,
	})
}

type serverInfoData struct {
	ServerID          string                     `json:"server_id"`
	RelayServer       string                     `json:"relay_server"`
	APIURL            string                     `json:"api_url"`
	APIKeyMasked      string                     `json:"api_key_masked"`
	EndpointSources   serverinfo.EndpointSources `json:"endpoint_sources"`
	EnvOverrideActive bool                       `json:"env_override_active"`
}

// serverInfo answers GET /api/settings/server-info: the addresses a
// client is given for the host of the request (a ?host= is not read),
// where each came from, and the masked API key.
func (g *general) serverInfo(w http.ResponseWriter, r *http.Request) {
	endpoints := g.d.ServerInfo.ResolveRustDeskEndpoints(serverinfo.RequestHostOf(r), "")
	key, _ := g.d.ServerInfo.GetAPIKey(true)
	if key == "" {
		key = noAPIKey
	}
	kit.OK(w, r, serverInfoData{
		ServerID:          endpoints.Host,
		RelayServer:       endpoints.Relay,
		APIURL:            endpoints.API,
		APIKeyMasked:      key,
		EndpointSources:   endpoints.Sources,
		EnvOverrideActive: endpoints.EnvOverrideActive,
	})
}

// audit answers GET /api/settings/audit?limit=: the newest entries of the
// audit log with the account's name, limit held between 1 and 500, 100
// when it is no number or 0.
func (g *general) audit(w http.ResponseWriter, r *http.Request) {
	limit := jsreq.ClampedInt(jsreq.Query(r, "limit"), 100, 1, 500)
	logs, err := auditLogs(r.Context(), g.d.Store, limit)
	if err != nil {
		g.serverError(w, r, "Get audit logs error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: logs})
}
