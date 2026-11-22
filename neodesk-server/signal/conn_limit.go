package signal

import "sync"

// Limits on open connections of the WebSocket signal port and the NAT test
// port. A per-address count bounds what one client can hold open without
// refusing the bursts of new connections a client behind a shared NAT makes
// (online polling, one WebSocket per session); the total bounds the file
// descriptors the ports can take from the API and the console of the same
// process.
const (
	maxWSSignalConnsPerHost = 256
	maxWSSignalConns        = 10000
	maxNATConnsPerHost      = 32
	maxNATConns             = 2048
)

// connLimiter counts open connections per address and in total.
type connLimiter struct {
	mu         sync.Mutex
	perHost    map[string]int
	total      int
	maxPerHost int
	maxTotal   int
}

func newConnLimiter(maxPerHost, maxTotal int) *connLimiter {
	return &connLimiter{perHost: make(map[string]int), maxPerHost: maxPerHost, maxTotal: maxTotal}
}

// acquire takes a slot for host and reports whether one was free. Every
// successful acquire needs one release.
func (l *connLimiter) acquire(host string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || l.perHost[host] >= l.maxPerHost {
		return false
	}
	l.total++
	l.perHost[host]++
	return true
}

func (l *connLimiter) release(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if l.perHost[host] <= 1 {
		delete(l.perHost, host)
		return
	}
	l.perHost[host]--
}
