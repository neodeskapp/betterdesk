package toolkit

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

var macPattern = regexp.MustCompile(`^[0-9A-F]{12}$`)

type wolData struct {
	MAC       string `json:"mac"`
	Broadcast jsText `json:"broadcast"`
}

// wol answers POST /api/toolkit/wol with the body {"mac":"aa:bb:cc:dd:ee:ff",
// "broadcast":"192.168.1.255"}: it sends the wake-on-LAN packet for the
// address to UDP port 9 of the broadcast address, 255.255.255.255 when
// none is given. The broadcast address is not checked by the guard.
func (h *handlers) wol(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	mac, ok := bodyText(body, "mac")
	if !ok || mac == "" {
		kit.Fail(w, r, http.StatusBadRequest, "MAC address is required")
		return
	}
	// Upper case as JavaScript makes it: the ligature ff becomes FF
	cleaned := strings.ToUpper(strings.NewReplacer(":", "", "-", "", "ﬀ", "FF").Replace(mac))
	if !macPattern.MatchString(cleaned) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid MAC address format")
		return
	}
	address := make([]byte, 6)
	for i := range address {
		b, _ := strconv.ParseUint(cleaned[2*i:2*i+2], 16, 8)
		address[i] = byte(b)
	}
	// The magic packet: six bytes FF, then the address sixteen times
	packet := make([]byte, 0, 102)
	packet = append(packet, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	for i := 0; i < 16; i++ {
		packet = append(packet, address...)
	}
	broadcast := "255.255.255.255"
	if given, ok := bodyText(body, "broadcast"); ok && given != "" {
		broadcast = given
	}
	if err := h.sendMagic(r.Context(), packet, broadcast); err != nil {
		kit.JSON(w, r, http.StatusOK, failed{Error: err.Error()})
		return
	}
	pairs := make([]string, 6)
	for i := range pairs {
		pairs[i] = cleaned[2*i : 2*i+2]
	}
	kit.OK(w, r, wolData{MAC: strings.Join(pairs, ":"), Broadcast: jsText(broadcast)})
}

const wolPort = 9

// sendMagic sends the packet to port 9 of a broadcast address, which may
// be a name. The messages of a failure are Node's:
//
//	an IPv6 address                 send EINVAL <address>:9
//	the name does not resolve       getaddrinfo ENOTFOUND <name>
//	the packet cannot be sent       send <errno> <address>:9
func (h *handlers) sendMagic(ctx context.Context, packet []byte, broadcast string) error {
	target := broadcast
	switch ssrfguard.IsIP(broadcast) {
	case 6:
		return &connectError{"send EINVAL " + broadcast + ":" + strconv.Itoa(wolPort)}
	case 0:
		found, err := h.net.Lookup(ctx, broadcast)
		if err != nil {
			return err
		}
		target = ""
		for _, address := range found {
			if ssrfguard.IsIPv4(address) {
				target = address
				break
			}
		}
		if target == "" {
			return &ssrfguard.LookupError{Code: "ENOTFOUND", Host: broadcast}
		}
	}
	if err := h.net.SendUDP(ctx, packet, &net.UDPAddr{IP: net.ParseIP(target), Port: wolPort}); err != nil {
		return &connectError{"send " + errnoName(err) + " " + target + ":" + strconv.Itoa(wolPort)}
	}
	return nil
}

// sendUDP sends one packet from a new IPv4 socket. Go lets UDP sockets
// send to broadcast addresses without being asked.
func sendUDP(ctx context.Context, packet []byte, to *net.UDPAddr) error {
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.WriteTo(packet, to)
	return err
}

var domainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

// recordsTimeout is how long the record lookups of one request may take.
const recordsTimeout = 15 * time.Second

type whoisData struct {
	Domain string     `json:"domain"`
	DNS    dnsRecords `json:"dns"`
}

type dnsRecords struct {
	A    []string   `json:"A"`
	AAAA []string   `json:"AAAA"`
	MX   []mxRecord `json:"MX"`
	TXT  []jsText   `json:"TXT"`
	NS   []string   `json:"NS"`
}

type mxRecord struct {
	Priority uint16 `json:"priority"`
	Exchange string `json:"exchange"`
}

// lowerDomain lowers what can become part of a domain name: the ASCII
// capitals and the Kelvin sign, whose lower case is k. Every other
// character stays and fails the pattern.
func lowerDomain(s string) string {
	return strings.Map(func(c rune) rune {
		switch {
		case c >= 'A' && c <= 'Z':
			return c + 'a' - 'A'
		case c == 0x212A:
			return 'k'
		}
		return c
	}, s)
}

// whois answers POST /api/toolkit/whois with the body {"domain":
// "example.com"}: the A, AAAA, MX, TXT and NS records of the domain. A
// kind of record that cannot be read is an empty list. The domain must
// pass the guard like the host of a certificate check.
func (h *handlers) whois(w http.ResponseWriter, r *http.Request) {
	domain, ok := bodyText(kit.Body(r), "domain")
	if !ok || domain == "" {
		kit.Fail(w, r, http.StatusBadRequest, "Domain is required")
		return
	}
	clean := lowerDomain(cleanTarget(domain))
	if clean == "" || len(clean) > 253 || !domainPattern.MatchString(clean) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid domain")
		return
	}
	if _, err := h.guard.SafeResolvedHost(r.Context(), clean, ssrfguard.Options{AllowPrivate: true}); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, refusalText(err, "Invalid domain"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), recordsTimeout)
	defer cancel()
	records := dnsRecords{A: []string{}, AAAA: []string{}, MX: []mxRecord{}, TXT: []jsText{}, NS: []string{}}
	// To the record lookups an address is a name like any other, and one
	// that has no address records
	literal := ssrfguard.IsIP(clean) != 0
	var wg sync.WaitGroup
	run := func(lookup func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lookup()
		}()
	}
	addresses := func(network string, into *[]string) {
		if literal {
			return
		}
		if found, err := h.net.Records.LookupIP(ctx, network, clean); err == nil {
			for _, ip := range found {
				*into = append(*into, ip.String())
			}
		}
	}
	run(func() { addresses("ip4", &records.A) })
	run(func() { addresses("ip6", &records.AAAA) })
	run(func() {
		if found, err := h.net.Records.LookupMX(ctx, clean); err == nil {
			for _, mx := range found {
				records.MX = append(records.MX, mxRecord{Priority: mx.Pref, Exchange: strings.TrimSuffix(mx.Host, ".")})
			}
		}
	})
	run(func() {
		if found, err := h.net.Records.LookupTXT(ctx, clean); err == nil {
			for _, txt := range found {
				records.TXT = append(records.TXT, jsText(txt))
			}
		}
	})
	run(func() {
		if found, err := h.net.Records.LookupNS(ctx, clean); err == nil {
			for _, ns := range found {
				records.NS = append(records.NS, strings.TrimSuffix(ns.Host, "."))
			}
		}
	})
	wg.Wait()
	kit.OK(w, r, whoisData{Domain: clean, DNS: records})
}
