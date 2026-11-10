package goapi

import (
	"context"
	"net/http"
)

// Client branding.

// GetBranding calls GET /api/branding. It returns {success:true, data} as
// the API server sent it, or F1.
func (c *Client) GetBranding(ctx context.Context) *Object {
	data, e := c.data(ctx, http.MethodGet, "/branding")
	if e != nil {
		return fail(f1, e)
	}
	return NewObject("success", true, "data", data)
}

// SaveBranding calls POST /api/branding with body. W or F1.
func (c *Client) SaveBranding(ctx context.Context, body any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/branding", Body(body))
}
