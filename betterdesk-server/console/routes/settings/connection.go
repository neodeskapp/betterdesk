package settings

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// The connection mode (P2P first or relay only, and its switches) and the
// public addresses RustDesk clients are given (package
// console/serverinfo).

type connectionData struct {
	serverinfo.ConnectionSettings
	Deployment string `json:"deployment"`
	Runtime    any    `json:"runtime"`
}

// getConnectionMode answers GET /api/settings/connection-mode: the saved
// settings, where they live, and what the running server reports.
func (g *general) getConnectionMode(w http.ResponseWriter, r *http.Request) {
	saved, err := g.d.ServerInfo.GetConnectionMode(r.Context())
	if err != nil {
		g.log.Error("Get connection mode error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, messageOr(err, kit.T(r, "errors.server_error", nil)))
		return
	}
	// the running settings are on /api/health/detail; /api/health
	// answers status and version only
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var runtime any
	if connection := g.d.GoAPI.GetHealth(ctx).Get("connection"); goapi.Truthy(connection) {
		runtime = connection
	}
	kit.OK(w, r, connectionData{ConnectionSettings: saved, Deployment: g.d.ServerInfo.DetectDeploymentSource(), Runtime: runtime})
}

// setConnectionMode answers PUT /api/settings/connection-mode. Unless the
// server took the settings at once, the change goes into the caller's
// restart transaction with the settings before it as the way back.
func (g *general) setConnectionMode(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	previous, err := g.d.ServerInfo.GetConnectionMode(r.Context())
	var write *serverinfo.ConnectionWrite
	if err == nil {
		write, err = g.d.ServerInfo.SetConnectionMode(r.Context(), serverinfo.ConnectionInput{
			Mode:                    serverinfo.Field(body, "mode"),
			P2PFallbackMs:           serverinfo.Field(body, "p2p_fallback_ms"),
			SameNATRelay:            serverinfo.Field(body, "same_nat_relay"),
			AllowSharedNATInitiator: serverinfo.Field(body, "allow_shared_nat_initiator"),
			LoggedInOnlyInitiator:   serverinfo.Field(body, "logged_in_only_initiator"),
			OperatorOnlyOutbound:    serverinfo.Field(body, "operator_only_outbound"),
		})
	}
	var pending *restart.Public
	if err == nil && write.Source != serverinfo.ConnectionSourceGoAPI {
		var settings any
		settings, err = goapi.FromGo(previous.ConnectionMode)
		if err == nil {
			pending, err = g.d.Restart.RegisterChange(kit.UserID(r), restart.Change{
				Key:      "connection-mode",
				Label:    kit.T(r, "settings.connection_mode_title", nil),
				Rollback: goapi.NewObject("type", "connection-mode", "settings", settings),
			})
		}
	}
	if err == nil {
		s := write.Settings
		details := "Connection mode set to " + s.Mode + ", logged_in_only_initiator=" + yn(s.LoggedInOnlyInitiator) +
			", operator_only_outbound=" + yn(s.OperatorOnlyOutbound) + " (" + write.Source + ")"
		err = g.d.LogAction(r, "connection_mode_changed", details)
	}
	switch {
	case err == nil:
		kit.JSON(w, r, http.StatusOK, answerMessage{
			Success: true,
			Data:    write.Object().Set("restartRequired", pending),
			Message: kit.T(r, "settings.connection_mode_saved", nil),
		})
	case errors.Is(err, serverinfo.ErrNotConfigurable):
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.connection_mode_not_configurable", nil))
	case errors.Is(err, serverinfo.ErrComposeServerNotFound):
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.connection_mode_compose_error", nil))
	default:
		g.log.Error("Set connection mode error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, messageOr(err, kit.T(r, "errors.server_error", nil)))
	}
}

func yn(value bool) string {
	if value {
		return "Y"
	}
	return "N"
}

type publicEndpointsData struct {
	serverinfo.PublicEndpoints
	EnvOverrideActive bool `json:"env_override_active"`
}

// getPublicEndpoints answers GET /api/settings/public-endpoints: the ID
// server, relay and API address clients are given, where set.
func (g *general) getPublicEndpoints(w http.ResponseWriter, r *http.Request) {
	settings := g.d.ServerInfo.GetPublicEndpointSettings()
	kit.OK(w, r, publicEndpointsData{PublicEndpoints: settings, EnvOverrideActive: serverinfo.IsEnvOverrideActive(settings)})
}

// setPublicEndpoints answers PUT /api/settings/public-endpoints with
// {public_server_id, public_relay_server, public_api_url}: saved in the
// data folder and in the console's .env. A body with none of them is
// refused.
func (g *general) setPublicEndpoints(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, "public_server_id", "public_relay_server", "public_api_url") {
		return
	}
	saved, err := g.d.ServerInfo.SavePublicEndpointSettings(serverinfo.PublicEndpointsInput{
		PublicServerID:    serverinfo.Field(body, "public_server_id"),
		PublicRelayServer: serverinfo.Field(body, "public_relay_server"),
		PublicAPIURL:      serverinfo.Field(body, "public_api_url"),
	})
	if err == nil {
		details := "RustDesk public endpoints updated (ID=" + orDash(saved.PublicServerID) + ", relay=" +
			orDash(saved.PublicRelayServer) + ", api=" + orDash(saved.PublicAPIURL) + ")"
		err = g.d.LogAction(r, "public_endpoints_changed", details)
	}
	switch {
	case err == nil:
		kit.JSON(w, r, http.StatusOK, answerMessage{
			Success: true,
			Data:    publicEndpointsData{PublicEndpoints: saved, EnvOverrideActive: serverinfo.IsEnvOverrideActive(saved)},
			Message: kit.T(r, "settings.public_endpoints_saved", nil),
		})
	case errors.Is(err, serverinfo.ErrInvalidPublicHost):
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.public_endpoints_invalid_host", nil))
	case errors.Is(err, serverinfo.ErrInvalidPublicAPIURL):
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.public_endpoints_invalid_api_url", nil))
	default:
		g.log.Error("Save public endpoints error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, messageOr(err, kit.T(r, "errors.server_error", nil)))
	}
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
