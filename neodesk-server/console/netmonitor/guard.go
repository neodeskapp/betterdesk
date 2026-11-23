package netmonitor

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard"
)

// Every check asks console/ssrfguard before anything leaves the server, in
// its mode for the network tools of an administrator: private networks are
// allowed, loopback, link-local and the cloud metadata names are not. A
// refusal is a *ssrfguard.BlockedError; its message goes to the client.
// The check then goes to an address the guard checked, never to the name
// again.

// tools are the options of the guard for the checks.
var tools = ssrfguard.Options{AllowPrivate: true}

// Resolver looks host names up. *net.Resolver is one.
type Resolver = ssrfguard.Resolver

// newGuard is the guard of the checks, with its lookups on resolver: the
// spellings of an IPv4 address the system resolver reads without asking
// anyone ("2130706433", "0x7f.1", "127.1") are that address, every other
// name is asked for its addresses.
func newGuard(resolver Resolver) *ssrfguard.Guard {
	return &ssrfguard.Guard{Lookup: ssrfguard.ResolverLookup(resolver)}
}
