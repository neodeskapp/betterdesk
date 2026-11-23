package branding

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard"
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

// redirector answers every request with a redirect to where names, and
// counts the requests.
type redirector struct {
	where    func(url string) string
	requests []string
}

func (r *redirector) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, req.URL.String())
	if next := r.where(req.URL.String()); next != "" {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {next}}, Body: http.NoBody, Request: req}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("/* no fonts */")), Request: req}, nil
}

// TestFontDownloadStopsAtTheRedirectLimit: the font download follows at
// most five redirects, and only to Google Fonts over https, so a loop
// ends at once and a redirect cannot send the console elsewhere (BUGS
// BR16).
func TestFontDownloadStopsAtTheRedirectLimit(t *testing.T) {
	for _, c := range []struct {
		name     string
		where    func(string) string
		want     string
		requests int
	}{
		{"a loop", func(u string) string { return u }, "Too many redirects", 6},
		{"plain http", func(u string) string { return strings.Replace(u, "https:", "http:", 1) }, "Redirect away from Google Fonts refused", 1},
		{"another host", func(string) string { return "https://fonts.example.org/css" }, "Redirect away from Google Fonts refused", 1},
		{"a host that ends like Google's", func(string) string { return "https://evil-fonts.googleapis.com.example/css" }, "Redirect away from Google Fonts refused", 1},
		{"five to Google", func(u string) string {
			if n := strings.Count(u, "/x"); n < 5 {
				return "https://fonts.gstatic.com/x" + strings.Repeat("/x", n)
			}
			return ""
		}, "", 6},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &redirector{where: c.where}
			opts := fixtureOptions(t.TempDir())
			opts.HTTP = r
			s, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.DownloadFont(context.Background(), "Inter", Undefined)
			if Message(err) != c.want || len(r.requests) != c.requests {
				t.Errorf("error %q after %d requests, want %q after %d: %v", Message(err), len(r.requests), c.want, c.requests, r.requests)
			}
		})
	}
}
