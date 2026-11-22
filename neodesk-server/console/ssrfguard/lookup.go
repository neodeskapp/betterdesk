package ssrfguard

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
)

// LookupError is a failed lookup with the message Node gave for it:
// "getaddrinfo ENOTFOUND name".
type LookupError struct {
	// Code is ENOTFOUND for a name that does not exist and EAI_AGAIN for
	// a resolver that did not answer.
	Code string
	Host string
}

func (e *LookupError) Error() string { return "getaddrinfo " + e.Code + " " + e.Host }

// SystemLookup resolves a name with the system's resolver, the way the
// console's lookups always went: a number or dotted numbers in the old
// spellings ("2130706433", "0x7f.1", "0177.0.0.1") are an IPv4 address
// and need no DNS, everything else is asked for its A and AAAA records.
// Addresses come back as text without a zone.
func SystemLookup(ctx context.Context, host string) ([]string, error) {
	return ResolverLookup(net.DefaultResolver)(ctx, host)
}

// Resolver looks names up. *net.Resolver is one.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// ResolverLookup is SystemLookup with another resolver for the names.
func ResolverLookup(resolver Resolver) LookupFunc {
	return func(ctx context.Context, host string) ([]string, error) {
		if address, ok := legacyIPv4(host); ok {
			return []string{address}, nil
		}
		found, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, lookupError(err, host)
		}
		out := make([]string, 0, len(found))
		for _, address := range found {
			out = append(out, address.IP.String())
		}
		return out, nil
	}
}

// lookupError names a resolver's failure the way getaddrinfo's were
// named.
func lookupError(err error, host string) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && !dnsErr.IsNotFound && (dnsErr.IsTimeout || dnsErr.IsTemporary) {
		return &LookupError{Code: "EAI_AGAIN", Host: host}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &LookupError{Code: "EAI_AGAIN", Host: host}
	}
	return &LookupError{Code: "ENOTFOUND", Host: host}
}

// legacyIPv4 reads an IPv4 address in the forms inet_aton takes: one to
// four numbers, each decimal, octal (leading 0) or hex (leading 0x), the
// last filling the bytes the others leave.
func legacyIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return "", false
	}
	var address uint64
	for i, part := range parts {
		n, ok := legacyNumber(part)
		if !ok {
			return "", false
		}
		last := i == len(parts)-1
		switch {
		case !last && n > 0xFF:
			return "", false
		case last && n > uint64(0xFFFFFFFF)>>(8*uint(i)):
			return "", false
		case last:
			address |= n
		default:
			address |= n << (8 * uint(3-i))
		}
	}
	return strconv.FormatUint(address>>24, 10) + "." + strconv.FormatUint(address>>16&0xFF, 10) + "." +
		strconv.FormatUint(address>>8&0xFF, 10) + "." + strconv.FormatUint(address&0xFF, 10), true
}

func legacyNumber(part string) (uint64, bool) {
	base := 10
	switch {
	case len(part) > 2 && (part[:2] == "0x" || part[:2] == "0X"):
		part, base = part[2:], 16
	case len(part) > 1 && part[0] == '0':
		part, base = part[1:], 8
	}
	if part == "" || len(part) > 16 {
		return 0, false
	}
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case c >= '0' && c <= '7':
		case base >= 10 && (c == '8' || c == '9'):
		case base == 16 && isASCIIHex(rune(c)):
		default:
			return 0, false
		}
	}
	n, err := strconv.ParseUint(part, base, 64)
	return n, err == nil
}
