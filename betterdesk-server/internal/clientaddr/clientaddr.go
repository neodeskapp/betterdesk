// Package clientaddr picks a request's client address out of the socket
// address and the X-Forwarded-For header, by one rule for the server's API
// and the console: walk from the socket leftwards through the entries and
// take the first address that is not a trusted proxy. Each proxy appends
// the address it saw, so entries left of the last trusted hop are what the
// client wrote itself and never count.
package clientaddr

import (
	"net/http"
	"strings"
)

// Forwarded returns the entries of every X-Forwarded-For line of h in
// order, trimmed, without empty ones.
func Forwarded(h http.Header) []string {
	var out []string
	for _, line := range h.Values("X-Forwarded-For") {
		for _, part := range strings.Split(line, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// Rightmost returns the client's address: the first address, from the
// socket (hop 0) through forwarded from right to left, that trusted does
// not accept as a proxy. When every hop is trusted it is the leftmost
// entry, and the socket when there are no entries.
func Rightmost(socket string, forwarded []string, trusted func(addr string, hop int) bool) string {
	chain := make([]string, 0, len(forwarded)+1)
	chain = append(chain, socket)
	for i := len(forwarded) - 1; i >= 0; i-- {
		chain = append(chain, forwarded[i])
	}
	for i, addr := range chain {
		if i == len(chain)-1 || !trusted(addr, i) {
			return addr
		}
	}
	return socket
}
