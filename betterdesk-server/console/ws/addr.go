package ws

import "sync"

// AddrCounter counts open sockets per client address, for a limit per
// address. It counts the way the old relay did: Add after the checks, Done
// for every end the socket reports. Done never goes below zero.
type AddrCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

// NewAddrCounter returns an empty counter.
func NewAddrCounter() *AddrCounter {
	return &AddrCounter{counts: map[string]int{}}
}

// Count is the number of sockets counted for addr.
func (c *AddrCounter) Count(addr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[addr]
}

// Add counts one more socket for addr.
func (c *AddrCounter) Add(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[addr]++
}

// Done counts one socket less for addr; at zero the address is forgotten.
func (c *AddrCounter) Done(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.counts[addr] - 1
	if n <= 0 {
		delete(c.counts, addr)
		return
	}
	c.counts[addr] = n
}
