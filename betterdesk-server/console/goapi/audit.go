package goapi

import (
	"context"
	"net/http"

	"github.com/unitronix/betterdesk-server/logging"
)

// The audit log and the client audit records.

// GetAuditEvents calls GET /api/audit/events with limit; a nil limit is
// 100. W or F1.
func (c *Client) GetAuditEvents(ctx context.Context, limit any) *Object {
	if limit == nil {
		limit = float64(100)
	}
	return c.wrapped(ctx, f1, http.MethodGet, "/audit/events", Query("limit", limit))
}

// GetClientAuditConnections calls GET /api/audit/conn with filters
// (host_id, peer_id, action, limit, offset) as the query. It returns {data,
// total}, or nil when the call fails.
func (c *Client) GetClientAuditConnections(ctx context.Context, filters any) *Object {
	return c.clientAudit(ctx, "getClientAuditConnections", "/audit/conn", filters)
}

// GetClientAuditFiles calls GET /api/audit/file. See
// GetClientAuditConnections.
func (c *Client) GetClientAuditFiles(ctx context.Context, filters any) *Object {
	return c.clientAudit(ctx, "getClientAuditFiles", "/audit/file", filters)
}

// GetClientAuditAlarms calls GET /api/audit/alarm. See
// GetClientAuditConnections.
func (c *Client) GetClientAuditAlarms(ctx context.Context, filters any) *Object {
	return c.clientAudit(ctx, "getClientAuditAlarms", "/audit/alarm", filters)
}

func (c *Client) clientAudit(ctx context.Context, name, path string, filters any) *Object {
	data, e := c.data(ctx, http.MethodGet, path, QueryParams(filters))
	var rows, total any
	if e == nil {
		rows, e = propOf(data, "data")
	}
	if e == nil {
		total, e = propOf(data, "total")
	}
	if e != nil {
		logging.Logf("goapi", "%s error: %s", name, e.Message)
		return nil
	}
	return NewObject("data", or(rows, []any{}), "total", or(total, float64(0)))
}
