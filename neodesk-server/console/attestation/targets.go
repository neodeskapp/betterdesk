package attestation

import (
	"context"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// Network is where the run finds the server's websocket listeners: the
// signal server's and the relay's, each two ports above the port of its
// service, on the first host that answers.
type Network struct {
	// SignalHostEnv and RelayHostEnv are WS_HBBS_HOST and WS_HBBR_HOST as
	// set, "" when they are not.
	SignalHostEnv, RelayHostEnv string
	// APIURL is the API server's address (BETTERDESK_API_URL, else
	// HBBS_API_URL); its host is a candidate.
	APIURL string
	// ProxySignalHost is the signal host of the websocket proxy
	// (WS_HBBS_HOST, else localhost); "" leaves it out.
	ProxySignalHost string
	// SignalPort and RelayPort are the ports of the signal server and the
	// relay (WS_HBBS_PORT, default 21116; WS_HBBR_PORT, default 21117).
	SignalPort, RelayPort int
	// TLS makes the addresses wss (WS_USE_TLS is "true" in any case).
	TLS bool
	// AllowSelfSigned accepts a certificate no authority signed
	// (ALLOW_SELF_SIGNED_CERTS).
	AllowSelfSigned bool
}

// candidates are the addresses to try (buildWsUrlCandidates).
type candidates struct {
	signal, relay         []string
	signalPort, relayPort int
	hosts                 []string
	scheme                string
}

func (n Network) candidates() candidates {
	signalPort, relayPort := n.SignalPort, n.RelayPort
	if signalPort == 0 {
		signalPort = 21116
	}
	if relayPort == 0 {
		relayPort = 21117
	}
	c := candidates{signalPort: signalPort + 2, relayPort: relayPort + 2, scheme: "ws"}
	if n.TLS {
		c.scheme = "wss"
	}
	add := func(h string) {
		if h == "" {
			return
		}
		for _, have := range c.hosts {
			if have == h {
				return
			}
		}
		c.hosts = append(c.hosts, h)
	}
	add(n.SignalHostEnv)
	add(n.RelayHostEnv)
	add(apiHost(n.APIURL))
	add(n.ProxySignalHost)
	add("127.0.0.1")
	add("localhost")
	for _, h := range c.hosts {
		c.signal = append(c.signal, c.scheme+"://"+h+":"+strconv.Itoa(c.signalPort))
		c.relay = append(c.relay, c.scheme+"://"+h+":"+strconv.Itoa(c.relayPort))
	}
	return c
}

// apiHost is the host name of the API server's address as new URL() reads
// it, "" when it is no URL (parseApiHost).
func apiHost(raw string) string {
	u, ok := ssrfguard.ParseURL(raw)
	if !ok {
		return ""
	}
	return u.Hostname
}

// probe is one address the run tried before the test.
type probe struct {
	url  string
	ok   bool
	kind string
}

// targets are the addresses the run uses (resolveWsTargets).
type targets struct {
	signal, relay         string
	host                  string
	signalPort, relayPort int
	scheme                string
	probes                []probe
	resolved              bool
}

// probeLog is the list of tried addresses as the result holds it.
func (t *targets) probeLog() []any {
	out := make([]any, 0, len(t.probes))
	for _, p := range t.probes {
		o := branding.NewObject()
		o.Set("url", p.url)
		o.Set("ok", p.ok)
		o.Set("kind", p.kind)
		out = append(out, o)
	}
	return out
}

// object is the targets as the result holds them.
func (t *targets) object() *branding.Object {
	o := branding.NewObject()
	o.Set("signal", nullable(t.signal))
	o.Set("relay", nullable(t.relay))
	o.Set("host", t.host)
	o.Set("signalPort", float64(t.signalPort))
	o.Set("relayPort", float64(t.relayPort))
	o.Set("scheme", t.scheme)
	o.Set("probeLog", t.probeLog())
	o.Set("resolved", t.resolved)
	return o
}

// nullable is s, or nil (null) for "".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// resolveTargets tries each candidate of the signal server, then of the
// relay, and keeps the first of each that answers a health check.
func (r *run) resolveTargets(ctx context.Context) (*targets, error) {
	c := r.s.opts.Network.candidates()
	t := &targets{signalPort: c.signalPort, relayPort: c.relayPort, scheme: c.scheme}
	for _, u := range c.signal {
		ok, err := r.probe(ctx, u)
		if err != nil {
			return nil, err
		}
		t.probes = append(t.probes, probe{u, ok, "signal"})
		if ok {
			t.signal = u
			break
		}
	}
	for _, u := range c.relay {
		ok, err := r.probe(ctx, u)
		if err != nil {
			return nil, err
		}
		t.probes = append(t.probes, probe{u, ok, "relay"})
		if ok {
			t.relay = u
			break
		}
	}
	switch {
	case t.signal != "":
		if u, ok := ssrfguard.ParseURL(t.signal); ok {
			t.host = u.Hostname
		}
	case len(c.hosts) > 0:
		t.host = c.hosts[0]
	default:
		t.host = "127.0.0.1"
	}
	t.resolved = t.signal != "" || t.relay != ""
	return t, nil
}

// probe connects to u, sends a health check and waits for any answer
// (probeWsUrl). Only the end of ctx is an error.
func (r *run) probe(ctx context.Context, u string) (bool, error) {
	conn, err := r.s.opts.Dialer.Dial(ctx, u, probeTimeout)
	if err != nil {
		return false, ctx.Err()
	}
	defer conn.Close()
	if err := conn.Send(healthCheck()); err != nil {
		return false, ctx.Err()
	}
	if err := conn.AwaitMessage(ctx, probeTimeout); err != nil {
		return false, ctx.Err()
	}
	return true, nil
}
