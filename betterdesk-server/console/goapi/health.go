package goapi

import (
	"context"
	"net/http"
	"sync"
)

// Health and statistics.

// GetHealth calls GET /api/health. It returns the health object with
// "status" set to "running" and "backend" set to "betterdesk", or
// {status:"unreachable", backend:"betterdesk", error} when the call fails.
func (c *Client) GetHealth(ctx context.Context) *Object {
	data, e := c.data(ctx, http.MethodGet, "/health")
	if e != nil {
		return NewObject("status", "unreachable", "backend", "betterdesk", "error", e.Message)
	}
	return spread(data).Set("status", "running").Set("backend", "betterdesk")
}

// GetServerStats calls GET /api/server/stats. W or F1.
func (c *Client) GetServerStats(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/server/stats")
}

// GetServerInfo calls GET /api/health and GET /api/server/stats at the same
// time and returns {health, stats, backend:"betterdesk"}. A call that fails
// leaves {} in its place.
func (c *Client) GetServerInfo(ctx context.Context) *Object {
	var health, stats any
	var wg sync.WaitGroup
	fetch := func(path string, into *any) {
		defer wg.Done()
		data, e := c.data(ctx, http.MethodGet, path)
		if e != nil {
			data = NewObject()
		}
		*into = data
	}
	wg.Add(2)
	go fetch("/health", &health)
	go fetch("/server/stats", &stats)
	wg.Wait()
	return NewObject("health", health, "stats", stats, "backend", "betterdesk")
}

// SyncOnlineStatus makes no call: the API server owns the peer state.
func (c *Client) SyncOnlineStatus() *Object {
	return NewObject("synced", float64(0), "skipped", true, "reason", "betterdesk_manages_state")
}
