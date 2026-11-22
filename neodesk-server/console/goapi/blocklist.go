package goapi

import (
	"context"
	"net/http"
)

// The blocklist.

// GetBlocklist calls GET /api/blocklist. W or F1. "entries" is null when
// the list is empty.
func (c *Client) GetBlocklist(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/blocklist")
}

// RemoveBlocklistEntry calls DELETE /api/blocklist/{entry}. W or F2.
func (c *Client) RemoveBlocklistEntry(ctx context.Context, entry string) *Object {
	return c.wrapped(ctx, f2, http.MethodDelete, "/blocklist/"+seg(entry))
}
