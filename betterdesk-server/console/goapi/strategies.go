package goapi

import (
	"context"
	"net/http"
)

// Strategies and the device list of the client API.

// AssignStrategy calls POST /api/strategies/assign with payload. W or F2.
func (c *Client) AssignStrategy(ctx context.Context, payload any) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/strategies/assign", Body(payload))
}

// GetStrategy calls GET /api/strategies/{guid}. W or F1.
func (c *Client) GetStrategy(ctx context.Context, guid string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/strategies/"+seg(guid))
}

// DeleteStrategy calls DELETE /api/strategies/{guid}. W or F2.
func (c *Client) DeleteStrategy(ctx context.Context, guid string) *Object {
	return c.wrapped(ctx, f2, http.MethodDelete, "/strategies/"+seg(guid))
}

// SetStrategyStatus calls PUT /api/strategies/{guid}/status. The body is
// the bare JSON value of enabled: true or false. W or F2.
func (c *Client) SetStrategyStatus(ctx context.Context, guid string, enabled any) *Object {
	return c.wrapped(ctx, f2, http.MethodPut, "/strategies/"+seg(guid)+"/status", Body(enabled))
}

// ListProDevices calls GET /api/devices with the id and pageSize of params
// (an *Object or map[string]any) that are set. W, or {success:false, error,
// data:{total:0, data:[]}}.
func (c *Client) ListProDevices(ctx context.Context, params any) *Object {
	var pairs []string
	if v := prop(params, "id"); Truthy(v) {
		pairs = append(pairs, "id", jsString(v))
	}
	if v := prop(params, "pageSize"); Truthy(v) {
		pairs = append(pairs, "pageSize", jsString(v))
	}
	path := "/devices"
	if q := queryOf(pairs...); q != "" {
		path += "?" + q
	}
	data, e := c.data(ctx, http.MethodGet, path)
	if e != nil {
		return NewObject("success", false, "error", e.Message,
			"data", NewObject("total", float64(0), "data", []any{}))
	}
	return wrap(data)
}
