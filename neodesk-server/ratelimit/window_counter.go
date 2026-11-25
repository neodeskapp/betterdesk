package ratelimit

import (
	"sync"
	"time"
)

// WindowCounter allows a number of events per key in fixed windows (a
// minute, say). Unlike IPLimiter it keeps one counter per key, not one
// time per event, so a high limit (thousands a minute from one office
// address) costs no more memory than a low one.
type WindowCounter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]*windowEntry
	// maxEntries is the most keys kept at once; see Allow.
	maxEntries int
}

type windowEntry struct {
	start time.Time
	count int
}

// defaultWindowEntries is how many keys a WindowCounter keeps at most.
const defaultWindowEntries = 200_000

// NewWindowCounter allows limit events per key in each window. A limit of
// 0 or less allows everything.
func NewWindowCounter(limit int, window time.Duration) *WindowCounter {
	return &WindowCounter{limit: limit, window: window, entries: make(map[string]*windowEntry), maxEntries: defaultWindowEntries}
}

// Limit is the number of events a key may have in one window.
func (c *WindowCounter) Limit() int {
	if c == nil {
		return 0
	}
	return c.limit
}

// Allow counts an event of key at now. It returns false when the key had
// its limit in the current window already, with the time until that window
// ends. When the counter holds maxEntries keys, the ones whose window has
// ended are dropped first; if none has, the event is allowed without being
// counted, so a flood of new keys never refuses a known one.
func (c *WindowCounter) Allow(key string, now time.Time) (bool, time.Duration) {
	if c == nil || c.limit <= 0 {
		return true, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		if len(c.entries) >= c.maxEntries {
			for k, e := range c.entries {
				if now.Sub(e.start) >= c.window {
					delete(c.entries, k)
				}
			}
			if len(c.entries) >= c.maxEntries {
				return true, 0
			}
		}
		entry = &windowEntry{start: now}
		c.entries[key] = entry
	}
	if now.Sub(entry.start) >= c.window {
		entry.start, entry.count = now, 0
	}
	if entry.count >= c.limit {
		return false, c.window - now.Sub(entry.start)
	}
	entry.count++
	return true, 0
}

// AddressKey is the key an address counts under: an IPv6 address as its
// /64 network, anything else as it is (the same as IPLimiter).
func AddressKey(ip string) string { return limiterKey(ip) }
