package branding

import (
	"context"
	"net"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard"
)

// guardedDial connects the font download to a host the outbound guard
// lets through: not localhost or a metadata name (ssrfguard.SafeHostname),
// and every address the name resolves to outside loopback, link-local and
// the private ranges (ssrfguard.IsBlockedIP). The connection goes to an
// address that was checked, so a name that changes its address between
// check and connect cannot reach a refused one. A failed lookup keeps
// Node's message ("getaddrinfo ENOTFOUND fonts.googleapis.com"); a refusal
// has the guard's wording.
func guardedDial(lookup ssrfguard.LookupFunc, dial func(ctx context.Context, network, address string) (net.Conn, error)) func(ctx context.Context, network, address string) (net.Conn, error) {
	if lookup == nil {
		lookup = ssrfguard.SystemLookup
	}
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	refuse := func(message string) error {
		return &ssrfguard.BlockedError{Message: message, Code: ssrfguard.Code}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		clean, err := ssrfguard.SafeHostname(host, ssrfguard.Options{})
		if err != nil {
			return nil, err
		}
		addresses := []string{clean}
		if ssrfguard.IsIP(clean) == 0 {
			if addresses, err = lookup(ctx, clean); err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, refuse("No DNS records for " + clean)
			}
			for _, a := range addresses {
				if ssrfguard.IsBlockedIP(a, ssrfguard.Options{}) {
					return nil, refuse("Hostname " + clean + " resolves to blocked address " + a)
				}
			}
		}
		var lastErr error
		for _, a := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(a, port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}
