package ssrfguard

import "regexp"

// The address tests are those of Node's net module, which the guard was
// written against: an IPv4 address is four decimal numbers from 0 to 255
// without leading zeros, an IPv6 address may end in a zone ("%eth0").
// Anything else is a name, also "127.1", "0x7f.0.0.1" and "2130706433".
const (
	v4Seg = `(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])`
	v4Str = `(?:` + v4Seg + `\.){3}` + v4Seg
	v6Seg = `(?:[0-9a-fA-F]{1,4})`
)

var (
	ipv4Reg = regexp.MustCompile(`^` + v4Str + `$`)
	ipv6Reg = regexp.MustCompile(`^(?:` +
		`(?:` + v6Seg + `:){7}(?:` + v6Seg + `|:)|` +
		`(?:` + v6Seg + `:){6}(?:` + v4Str + `|:` + v6Seg + `|:)|` +
		`(?:` + v6Seg + `:){5}(?::` + v4Str + `|(?::` + v6Seg + `){1,2}|:)|` +
		`(?:` + v6Seg + `:){4}(?:(?::` + v6Seg + `){0,1}:` + v4Str + `|(?::` + v6Seg + `){1,3}|:)|` +
		`(?:` + v6Seg + `:){3}(?:(?::` + v6Seg + `){0,2}:` + v4Str + `|(?::` + v6Seg + `){1,4}|:)|` +
		`(?:` + v6Seg + `:){2}(?:(?::` + v6Seg + `){0,3}:` + v4Str + `|(?::` + v6Seg + `){1,5}|:)|` +
		`(?:` + v6Seg + `:){1}(?:(?::` + v6Seg + `){0,4}:` + v4Str + `|(?::` + v6Seg + `){1,6}|:)|` +
		`(?::(?:(?::` + v6Seg + `){0,5}:` + v4Str + `|(?::` + v6Seg + `){1,7}|:))` +
		`)(?:%[0-9a-zA-Z\-.:]{1,})?$`)
)

// IsIPv4 is net.isIPv4.
func IsIPv4(s string) bool { return ipv4Reg.MatchString(s) }

// IsIPv6 is net.isIPv6.
func IsIPv6(s string) bool { return ipv6Reg.MatchString(s) }

// IsIP is net.isIP: 4, 6, or 0 for text that is no address.
func IsIP(s string) int {
	switch {
	case IsIPv4(s):
		return 4
	case IsIPv6(s):
		return 6
	}
	return 0
}
