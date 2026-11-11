package smtpwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"strconv"
	"syscall"
	"time"
)

// attemptTimeout is how long one address of a host with addresses of both
// families gets before the next one is tried.
const attemptTimeout = 250 * time.Millisecond

// ipFamily is 4 or 6 for an IP address in text form and 0 for anything else.
func ipFamily(host string) int {
	addr, err := netip.ParseAddr(host)
	switch {
	case err != nil:
		return 0
	case addr.Is4():
		return 4
	}
	return 6
}

// target is where to connect: the addresses to try, in order.
type target struct {
	addresses []string
	// perAttempt limits every attempt but the last when there are several
	perAttempt time.Duration
}

// resolve decides which addresses to connect to.
//
// An IP address is used as it is. For a name the A and the AAAA records are
// asked at the same time, each only when the machine has an address of that
// family on an interface other than loopback. When they give nothing the
// lookup of the operating system is asked and its addresses are filtered
// the same way; a name it knows but with no usable address is connected by
// name. Several addresses are tried in turn: IPv6 first, families
// alternating, addresses of one family in random order.
func (c *client) resolve() (target, error) {
	if ipFamily(c.host) != 0 {
		return target{addresses: []string{c.host}}, nil
	}
	timeout := min(c.dnsTimeout, c.connectionTimeout)
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	useV4, useV6 := c.families()
	type answer struct {
		family int
		ips    []net.IP
	}
	answers := make(chan answer, 2)
	lookup := func(family int, network string, use bool) {
		var ips []net.IP
		if use {
			// a family without records is not an error here
			ips, _ = c.resolver.LookupIP(ctx, network, c.host)
		}
		answers <- answer{family, ips}
	}
	go lookup(4, "ip4", useV4)
	go lookup(6, "ip6", useV6)
	var v4, v6 []string
	for i := 0; i < 2; i++ {
		select {
		case a := <-answers:
			for _, ip := range a.ips {
				if a.family == 4 {
					v4 = append(v4, ip.String())
				} else {
					v6 = append(v6, ip.String())
				}
			}
		case <-ctx.Done():
			return target{}, c.dnsTimeoutError()
		}
	}
	if len(v4)+len(v6) == 0 {
		addrs, err := c.resolver.LookupIPAddr(ctx, c.host)
		if ctx.Err() != nil {
			return target{}, c.dnsTimeoutError()
		}
		if err != nil {
			return target{}, &Error{Message: lookupErrorText(err, c.host), Code: "EDNS", Command: "CONN", cause: err}
		}
		var known []string
		for _, addr := range addrs {
			ip := addr.IP.String()
			known = append(known, ip)
			switch {
			case ipFamily(ip) == 4 && useV4:
				v4 = append(v4, ip)
			case ipFamily(ip) == 6 && useV6:
				v6 = append(v6, ip)
			}
		}
		if len(v4)+len(v6) == 0 {
			// connecting by name: every address the system knows is tried
			// in the order it gave them
			return target{addresses: alternate(known), perAttempt: attemptTimeout}, nil
		}
	}
	if len(v4)+len(v6) == 1 {
		return target{addresses: append(v4, v6...)}, nil
	}
	c.shuffle(len(v6), func(i, j int) { v6[i], v6[j] = v6[j], v6[i] })
	c.shuffle(len(v4), func(i, j int) { v4[i], v4[j] = v4[j], v4[i] })
	t := target{addresses: alternate(append(v6, v4...)), perAttempt: attemptTimeout}
	if len(v4) == 0 || len(v6) == 0 {
		// with one family every address gets its share of the time left
		share := time.Until(c.connectionDeadline) / time.Duration(len(t.addresses))
		t.perAttempt = max(share.Truncate(time.Millisecond), 10*time.Millisecond)
	}
	return t, nil
}

// dnsTimeoutError is the failure of a lookup that took too long, or the
// context's error when the caller gave up.
func (c *client) dnsTimeoutError() error {
	if err := c.ctx.Err(); err != nil {
		return context.Cause(c.ctx)
	}
	return &Error{Message: "DNS lookup for " + c.host + " timed out", Code: "EDNS", Command: "CONN"}
}

// alternate orders addresses for connecting: the family of the first address
// first, then the families in turn. An address is tried once.
func alternate(addresses []string) []string {
	var first, second []string
	firstFamily := 0
	seen := map[string]bool{}
	for _, address := range addresses {
		family := ipFamily(address)
		if family == 0 || seen[address] {
			continue
		}
		seen[address] = true
		if firstFamily == 0 {
			firstFamily = family
		}
		if family == firstFamily {
			first = append(first, address)
		} else {
			second = append(second, address)
		}
	}
	var out []string
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			out = append(out, first[i])
		}
		if i < len(second) {
			out = append(out, second[i])
		}
	}
	return out
}

// lookupErrorText is Node's text for a failed system lookup.
func lookupErrorText(err error, host string) string {
	code := "EAI_FAIL"
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		switch {
		case dnsErr.IsNotFound:
			code = "ENOTFOUND"
		case dnsErr.IsTimeout || dnsErr.IsTemporary:
			code = "EAI_AGAIN"
		}
	}
	return "getaddrinfo " + code + " " + host
}

// dial connects to the first address of the target that answers.
func (c *client) dial(t target) (net.Conn, error) {
	if c.port < 0 || c.port > 65535 {
		return nil, &Error{
			Message: fmt.Sprintf("Port should be >= 0 and < 65536. Received type number (%d).", c.port),
			Code:    "ECONNECTION", Command: "CONN",
		}
	}
	ctx, cancel := context.WithDeadline(c.ctx, c.connectionDeadline)
	defer cancel()
	port := strconv.Itoa(c.port)
	failed := 0
	for i, address := range t.addresses {
		attempt := ctx
		stop := context.CancelFunc(func() {})
		if i < len(t.addresses)-1 {
			attempt, stop = context.WithTimeout(ctx, t.perAttempt)
		}
		conn, err := c.opts.Dial(attempt, "tcp", net.JoinHostPort(address, port))
		stop()
		if err == nil {
			return conn, nil
		}
		if c.ctx.Err() != nil {
			return nil, context.Cause(c.ctx)
		}
		if ctx.Err() != nil {
			return nil, &Error{Message: "Connection timeout", Code: "ETIMEDOUT", Command: "CONN"}
		}
		if len(t.addresses) == 1 {
			return nil, &Error{Message: socketErrorText("connect", err, address+":"+port), Code: "ESOCKET", Command: "CONN", cause: err}
		}
		failed++
	}
	if failed == 0 {
		// the system knew the name a moment ago and now has no address for it
		return nil, &Error{Message: "getaddrinfo ENOTFOUND " + c.host, Code: "ESOCKET", Command: "CONN"}
	}
	// Node reports the failures of several addresses as one error without
	// a text of its own
	return nil, &Error{Message: "", Code: "ESOCKET", Command: "CONN"}
}

// socketErrorText is Node's text for a failed socket call: the call, the
// name of the error and, for connect, the address. An error that is not one
// of the operating system keeps its own text.
func socketErrorText(call string, err error, address string) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if name, ok := errnoNames[errno]; ok {
			text := call + " " + name
			if address != "" {
				text += " " + address
			}
			return text
		}
	}
	return err.Error()
}

func isTimeout(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded)
}

// isEOF reports whether a read ended because the server closed the
// connection, with or without saying so on the TLS layer.
func isEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// interfaceAddrs are the addresses of one network interface that is up.
type interfaceAddrs struct {
	loopback bool
	ips      []net.IP
}

// defaultFamilies reads the interfaces of the machine and reports which
// address families it can use.
func defaultFamilies() (ipv4, ipv6 bool) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return true, true
	}
	var list []interfaceAddrs
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		entry := interfaceAddrs{loopback: iface.Flags&net.FlagLoopback != 0}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				entry.ips = append(entry.ips, ipnet.IP)
			}
		}
		list = append(list, entry)
	}
	return usableFamilies(list)
}

// usableFamilies reports which address families have an address on an
// interface other than loopback. A machine without any interface address
// counts as having both.
func usableFamilies(interfaces []interfaceAddrs) (ipv4, ipv6 bool) {
	found := false
	for _, iface := range interfaces {
		for _, ip := range iface.ips {
			found = true
			switch {
			case iface.loopback:
			case ip.To4() != nil:
				ipv4 = true
			default:
				ipv6 = true
			}
		}
	}
	if !found {
		return true, true
	}
	return ipv4, ipv6
}

func defaultShuffle(n int, swap func(i, j int)) { rand.Shuffle(n, swap) }
