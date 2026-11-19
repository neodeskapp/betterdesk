package netmonitor

import (
	"context"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// Every check asks console/ssrfguard before anything leaves the server, in
// its mode for the network tools of an administrator: private networks are
// allowed, loopback, link-local and the cloud metadata names are not. A
// refusal is a *ssrfguard.BlockedError; its message goes to the client.

// tools are the options of the guard for the checks.
var tools = ssrfguard.Options{AllowPrivate: true}

// Resolver looks host names up. *net.Resolver is one.
type Resolver = ssrfguard.Resolver

// newGuard is the guard of the checks, with its lookups on resolver.
func newGuard(resolver Resolver) *ssrfguard.Guard {
	return &ssrfguard.Guard{Lookup: func(ctx context.Context, host string) ([]string, error) {
		return lookupAll(ctx, resolver, host)
	}}
}

// lookupAll is dns.lookup(host, {all: true}): the addresses of a name as
// text, in the order the system's resolver gives them. An address is its
// own answer, and so are the spellings of an IPv4 address the system
// resolver reads without asking anyone: "2130706433", "0x7f.1", "127.1".
func lookupAll(ctx context.Context, resolver Resolver, host string) ([]string, error) {
	if ssrfguard.IsIP(host) != 0 {
		return []string{host}, nil
	}
	return ssrfguard.ResolverLookup(resolver)(ctx, host)
}

// lookupFirst is dns.lookup(host): the first address of a name.
func lookupFirst(ctx context.Context, resolver Resolver, host string) (string, error) {
	addresses, err := lookupAll(ctx, resolver, host)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", &ssrfguard.LookupError{Code: "ENOTFOUND", Host: host}
	}
	return addresses[0], nil
}
