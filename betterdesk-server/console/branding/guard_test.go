package branding

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// The font download connects only to addresses the outbound guard lets
// through, and to the address it checked. Lookups and connections are
// made up here; nothing leaves the test (BUGS BR16).
func TestFontDownloadGoesThroughTheGuard(t *testing.T) {
	refused := errors.New("connection refused by the test")
	cases := []struct {
		name      string
		addresses []string
		lookupErr error
		want      string
		dials     string
	}{
		{"loopback", []string{"127.0.0.1"}, nil, "Hostname fonts.googleapis.com resolves to blocked address 127.0.0.1", ""},
		{"private", []string{"142.250.0.1", "10.0.0.5"}, nil, "Hostname fonts.googleapis.com resolves to blocked address 10.0.0.5", ""},
		{"link-local", []string{"169.254.169.254"}, nil, "Hostname fonts.googleapis.com resolves to blocked address 169.254.169.254", ""},
		{"no records", nil, nil, "No DNS records for fonts.googleapis.com", ""},
		{"lookup fails", nil, &ssrfguard.LookupError{Code: "ENOTFOUND", Host: "fonts.googleapis.com"}, "getaddrinfo ENOTFOUND fonts.googleapis.com", ""},
		{"public", []string{"142.250.0.1", "142.250.0.2"}, nil, refused.Error(), "142.250.0.1:443 142.250.0.2:443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			dials := ""
			opts := fixtureOptions(t.TempDir())
			opts.DB = nil
			opts.Lookup = func(_ context.Context, host string) ([]string, error) {
				if host != "fonts.googleapis.com" {
					t.Errorf("looked up %q", host)
				}
				return c.addresses, c.lookupErr
			}
			opts.Dial = func(_ context.Context, network, address string) (net.Conn, error) {
				mu.Lock()
				defer mu.Unlock()
				if dials != "" {
					dials += " "
				}
				dials += address
				return nil, refused
			}
			s, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.DownloadFont(context.Background(), "Inter", Undefined)
			if Message(err) != c.want {
				t.Errorf("error %q, want %q", Message(err), c.want)
			}
			if dials != c.dials {
				t.Errorf("dialled %q, want %q", dials, c.dials)
			}
		})
	}
}

// A host the guard refuses by name, or an address literal it refuses, is
// never looked up.
func TestGuardedDialRefusesNamesAndLiterals(t *testing.T) {
	dial := guardedDial(func(context.Context, string) ([]string, error) {
		t.Error("looked up")
		return nil, nil
	}, func(context.Context, string, string) (net.Conn, error) {
		t.Error("dialled")
		return nil, errors.New("no")
	})
	for address, want := range map[string]string{
		"localhost:443":               "Blocked hostname: localhost",
		"metadata.google.internal:80": "Blocked hostname: metadata.google.internal",
		"127.0.0.1:443":               "Blocked IP address: 127.0.0.1",
		"[::1]:443":                   "Blocked IP address: ::1",
		"192.168.1.10:443":            "Blocked IP address: 192.168.1.10",
		"no-port":                     "address no-port: missing port in address",
	} {
		if _, err := dial(context.Background(), "tcp", address); err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %s", address, err, want)
		}
	}
}
