package goapi

import (
	"context"
	"net/http"

	"github.com/unitronix/betterdesk-server/logging"
)

// Live status, telemetry and connection mode of a peer.

// GetConnectionMode calls GET /api/peers/{id}/connection-mode. W or F3.
func (c *Client) GetConnectionMode(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f3, http.MethodGet, "/peers/"+seg(id)+"/connection-mode")
}

// SetConnectionMode calls POST /api/peers/{id}/connection-mode with body
// ({mode, reason, operator_id, operator_name}). W or F3.
func (c *Client) SetConnectionMode(ctx context.Context, id string, body any) *Object {
	return c.wrapped(ctx, f3, http.MethodPost, "/peers/"+seg(id)+"/connection-mode", Body(body))
}

// GetStatusSummary calls GET /api/peers/status/summary. W or F1.
func (c *Client) GetStatusSummary(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/peers/status/summary")
}

// GetOnlinePeers calls GET /api/peers/online and returns its "peers" list
// as the API server sent it. Every failure gives an empty list.
func (c *Client) GetOnlinePeers(ctx context.Context) []any {
	data, e := c.data(ctx, http.MethodGet, "/peers/online")
	var peers []any
	if e == nil {
		peers, e = listOf(data, "peers")
	}
	if e != nil {
		logging.Logf("goapi", "getOnlinePeers error: %s", e.Message)
		return []any{}
	}
	return peers
}

// GetPeerStatus calls GET /api/peers/{id}/status. W or F1.
func (c *Client) GetPeerStatus(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/peers/"+seg(id)+"/status")
}

// GetPeerTelemetry calls GET /api/peers/{id}/telemetry. W or F1.
func (c *Client) GetPeerTelemetry(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/peers/"+seg(id)+"/telemetry")
}

// RefreshPeerHardware calls POST /api/peers/{id}/telemetry/refresh. W or
// F2.
func (c *Client) RefreshPeerHardware(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/peers/"+seg(id)+"/telemetry/refresh")
}

// QueuePeerTelemetryCommand calls POST /api/peers/{id}/telemetry/command
// with {command, args}; nil args are sent as {}. W or F2.
func (c *Client) QueuePeerTelemetryCommand(ctx context.Context, id string, command, args any) *Object {
	if args == nil {
		args = NewObject()
	}
	return c.wrapped(ctx, f2, http.MethodPost, "/peers/"+seg(id)+"/telemetry/command",
		Body(NewObject("command", command, "args", args)))
}

// GetPeerAvailability calls GET /api/peers/{id}/availability with day when
// it is given and tz_offset when it is neither nil nor "". W or F2.
func (c *Client) GetPeerAvailability(ctx context.Context, id string, day, tzOffset any) *Object {
	var opts []CallOption
	if Truthy(day) {
		opts = append(opts, Query("day", day))
	}
	if tzOffset != nil && tzOffset != "" {
		opts = append(opts, Query("tz_offset", tzOffset))
	}
	return c.wrapped(ctx, f2, http.MethodGet, "/peers/"+seg(id)+"/availability", opts...)
}

// GetPeerConnectionSessions calls GET /api/peers/{id}/connection-sessions
// with limit when it is given. W or F2.
func (c *Client) GetPeerConnectionSessions(ctx context.Context, id string, limit any) *Object {
	var opts []CallOption
	if Truthy(limit) {
		opts = append(opts, Query("limit", limit))
	}
	return c.wrapped(ctx, f2, http.MethodGet, "/peers/"+seg(id)+"/connection-sessions", opts...)
}
