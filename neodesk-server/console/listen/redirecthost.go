package listen

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// The redirect to HTTPS names one host, chosen when its listener is made
// (lib/redirectHost.js of the old console, audit H5): the configured
// public host, else the address the console binds, else "localhost".
// Nothing of a request reaches the host, so the plain port cannot send a
// browser to another site.

// hostnamePattern is the host name syntax the redirect writes: labels of
// ASCII letters, digits and inner hyphens, joined by dots. It is tested
// on a host already in lower case.
var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// schemePattern finds a configured value written as a URL.
var schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// normalizeConfiguredHost is the host a configured value names, in lower
// case without a port, or false when it names none: a value written as a
// URL gives the host of new URL() (a URL that does not parse gives none);
// one pair of brackets around it goes; then it must be an IP address as
// Node's net.isIP reads them, or a host name of at most 253 characters.
// An IPv6 address comes out without its brackets.
//
// A URL whose host needs the rules for international domain names
// (letters outside ASCII, a label starting with "xn--") names no host
// here, where Node took its ASCII form (DECISIONS A21).
func normalizeConfiguredHost(raw string) (string, bool) {
	value := jsTrim(raw)
	if value == "" {
		return "", false
	}
	host := value
	if schemePattern.MatchString(value) {
		u, ok := ssrfguard.ParseURL(value)
		if !ok {
			return "", false
		}
		host = u.Hostname
	}
	host = jsLower(jsTrim(stripBrackets(host)))
	if host == "" || len(host) > 253 {
		return "", false
	}
	if ssrfguard.IsIP(host) != 0 || hostnamePattern.MatchString(host) {
		return host, true
	}
	return "", false
}

// RedirectHost is the host the redirect sends browsers to
// (resolveRedirectHost): the configured host (PANEL_PUBLIC_HOST) when it
// names one, else the address the listener binds when it is a concrete
// IP address (not 0.0.0.0 or ::), else "localhost". source says which:
// "config", "listen" or "fallback".
func RedirectHost(configured, listenHost string) (host, source string) {
	if h, ok := normalizeConfiguredHost(configured); ok {
		return h, "config"
	}
	l := stripBrackets(jsTrim(listenHost))
	if l != "" && l != "0.0.0.0" && l != "::" && ssrfguard.IsIP(l) != 0 {
		return l, "listen"
	}
	return "localhost", "fallback"
}

// SafeRedirectURL is the address of the redirect (buildSafeRedirectUrl):
// https://, the host as normalizeConfiguredHost gives it ("localhost"
// when it gives none; an IPv6 address in brackets and without its zone,
// which browsers take in no URL), the HTTPS port unless it is 443 or not
// above 0, and the request target when it is a path that starts with one
// "/"; any other target (an absolute URL, "//host/...", "*") becomes "/".
func SafeRedirectURL(host string, httpsPort int, target string) string {
	return redirectOrigin(host, httpsPort) + samePath(target)
}

func redirectOrigin(host string, httpsPort int) string {
	safe, ok := normalizeConfiguredHost(host)
	if !ok {
		safe = "localhost"
	}
	if ssrfguard.IsIP(safe) == 6 {
		address, _, _ := strings.Cut(safe, "%")
		safe = "[" + address + "]"
	}
	suffix := ""
	if httpsPort > 0 && httpsPort != 443 {
		suffix = ":" + strconv.Itoa(httpsPort)
	}
	return "https://" + safe + suffix
}

func samePath(target string) string {
	if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
		return target
	}
	return "/"
}

// stripBrackets removes a "[" at the start and a "]" at the end, each on
// its own, as the old code's replace(/^\[|\]$/g) did.
func stripBrackets(s string) string {
	return strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
}

// isJSSpace reports the white space and line ends JavaScript's trim
// removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsLower is toLowerCase as far as the host checks see it: they take
// ASCII only, and the one letter Go lowers to ASCII where JavaScript does
// not is the dotted capital I (an i and a combining dot there).
func jsLower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
}
