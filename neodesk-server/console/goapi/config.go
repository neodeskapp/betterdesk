package goapi

import (
	"context"
	"net/http"
)

// Server configuration values.

// GetConfig calls GET /api/config/{key}. W or F1; an unset key has the
// value "".
func (c *Client) GetConfig(ctx context.Context, key string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/config/"+seg(key))
}

// SetConfig calls PUT /api/config/{key} with {value}. W or F2.
func (c *Client) SetConfig(ctx context.Context, key string, value any) *Object {
	return c.wrapped(ctx, f2, http.MethodPut, "/config/"+seg(key), Body(NewObject("value", value)))
}
