package goapi

import (
	"context"
	"net/http"
	"sync"
)

// Health and statistics.

// GetHealth calls GET /api/health/detail, the full health payload the API
// server gives the API key and the console, and GET /api/health, which
// anyone gets with status and version only, when that call fails. It
// returns the health object of the call that answered, with "status" set
// to "running" and "backend" set to "neodesk", or
// {status:"unreachable", backend:"neodesk", error} with the error of the
// second call when both fail.
func (c *Client) GetHealth(ctx context.Context) *Object {
	data, e := c.data(ctx, http.MethodGet, "/health/detail")
	if e != nil {
		data, e = c.data(ctx, http.MethodGet, "/health")
	}
	if e != nil {
		return NewObject("status", "unreachable", "backend", "neodesk", "error", e.Message)
	}
	return spread(data).Set("status", "running").Set("backend", "neodesk")
}

// GetServerStats calls GET /api/server/stats. W or F1.
func (c *Client) GetServerStats(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/server/stats")
}

// GetServerInfo calls GET /api/health and GET /api/server/stats at the same
// time and returns {health, stats, backend:"neodesk"}. A call that fails
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
	return NewObject("health", health, "stats", stats, "backend", "neodesk")
}

// SyncOnlineStatus makes no call: the API server owns the peer state.
func (c *Client) SyncOnlineStatus() *Object {
	return NewObject("synced", float64(0), "skipped", true, "reason", "neodesk_manages_state")
}
