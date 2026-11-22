package onboard

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// hostName is a host name of letters, digits and inner hyphens in labels
// joined by dots, in lower case. Links, file names and the shell scripts
// carry it as it is.
var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// Console is the console's address as a code's links name it: the scheme
// and the host with its port, as people reach the console.
type Console struct {
	// HTTPS is false for a console on plain HTTP, which the pages and the
	// client warn about.
	HTTPS bool
	// Host is the host name or address, without brackets.
	Host string
	// Port is the port, 0 for the scheme's own.
	Port int
}

// ParseConsole reads a base address (https://desk.example.com:8443).
func ParseConsole(base string) (Console, bool) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Console{}, false
	}
	c := Console{HTTPS: u.Scheme == "https", Host: strings.ToLower(u.Hostname())}
	if net.ParseIP(c.Host) == nil && !hostName.MatchString(c.Host) {
		return Console{}, false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return Console{}, false
		}
		if !(c.HTTPS && n == 443) && !(!c.HTTPS && n == 80) {
			c.Port = n
		}
	}
	return c, true
}

// Base is the address: https://host[:port], without a slash at the end.
func (c Console) Base() string {
	scheme := "https"
	if !c.HTTPS {
		scheme = "http"
	}
	return scheme + "://" + c.hostPort()
}

func (c Console) hostPort() string {
	host := c.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if c.Port != 0 {
		host += ":" + strconv.Itoa(c.Port)
	}
	return host
}

// JoinLink is the link of a join code: <base>/join/<code>.
func (c Console) JoinLink(code string) string { return c.Base() + "/join/" + Normalize(code) }

// SupportLink is the link of a support code: <base>/s/<code>.
func (c Console) SupportLink(code string) string { return c.Base() + "/s/" + Normalize(code) }

// Link is the link of a code of the kind.
func (c Console) Link(kind, code string) string {
	if kind == KindSupport {
		return c.SupportLink(code)
	}
	return c.JoinLink(code)
}

// DeepLink is the link the desktop client takes:
// betterdesk://join/<host[:port]>/<code>, or support instead of join,
// with ?http=1 for a console on plain HTTP.
func (c Console) DeepLink(kind, code string) string {
	verb := "join"
	if kind == KindSupport {
		verb = "support"
	}
	link := "betterdesk://" + verb + "/" + c.hostPort() + "/" + Normalize(code)
	if !c.HTTPS {
		link += "?http=1"
	}
	return link
}

// FileHost is the console as a file name carries it: the host, then
// _<port> when there is one, then _http for plain HTTP
// (desk.example.com, desk.example.com_8443, desk.lan_5000_http). A host
// name never has "_". An IPv6 address has no form there: "".
func (c Console) FileHost() string {
	if net.ParseIP(c.Host) != nil && strings.Contains(c.Host, ":") {
		return ""
	}
	out := c.Host
	if c.Port != 0 {
		out += "_" + strconv.Itoa(c.Port)
	}
	if !c.HTTPS {
		out += "_http"
	}
	return out
}

// FileName is the name a client package is served under for a code:
// BetterDesk-Setup-<host>-<code><ext> for join codes,
// BetterDesk-QuickSupport-<host>-<code><ext> for support codes. Without a
// host form (FileHost) the host part is left out.
func (c Console) FileName(kind, code, ext string) string {
	prefix := "BetterDesk-Setup-"
	if kind == KindSupport {
		prefix = "BetterDesk-QuickSupport-"
	}
	if host := c.FileHost(); host != "" {
		return prefix + host + "-" + Normalize(code) + ext
	}
	return prefix + Normalize(code) + ext
}
