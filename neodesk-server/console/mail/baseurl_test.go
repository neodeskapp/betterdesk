package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The link in a help request mail goes to the address people reach the
// console at: PANEL_PUBLIC_URL or PUBLIC_URL, else the panel's public host
// with https only when the console serves HTTPS. Without one the mail has
// no link. The old console built it from the listen address, with https
// whenever HTTP_REDIRECT_HTTPS was on, so a default install mailed
// https://127.0.0.1:5000, which nobody who got the mail could open
// (BUGS EM8).
func TestTheHelpLinkUsesThePublicAddress(t *testing.T) {
	for _, c := range []struct {
		name      string
		env       map[string]string
		panelHost string
		https     bool
		want      string
	}{
		{name: "nothing set", want: ""},
		{name: "a public URL", env: map[string]string{"PANEL_PUBLIC_URL": "https://panel.example.com/"}, panelHost: "other.example.com", want: "https://panel.example.com"},
		{name: "PUBLIC_URL", env: map[string]string{"PUBLIC_URL": "https://public.example.com"}, want: "https://public.example.com"},
		{name: "a public host over HTTPS", panelHost: "panel.example.com", https: true, want: "https://panel.example.com:5443"},
		{name: "a public host over HTTP", panelHost: "panel.example.com", want: "http://panel.example.com:5000"},
		{name: "a public host written as a URL", panelHost: " https://Panel.Example.com/console ", https: true, want: "https://panel.example.com:5443"},
		{name: "an IPv6 address", panelHost: "[2001:db8::1]", want: "http://[2001:db8::1]:5000"},
		{name: "no host", panelHost: "panel <b>.example.com", want: ""},
		{name: "blank", panelHost: "  ", want: ""},
	} {
		env := c.env
		s := New(Options{
			Getenv: func(name string) string { return env[name] }, Port: 5000, HTTPSPort: 5443,
			PanelHost: func() string { return c.panelHost }, ServesHTTPS: func() bool { return c.https },
		})
		if got := s.GetPanelBaseURL(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// The default ports are left out
	for https, want := range map[bool]string{true: "https://panel.example.com", false: "http://panel.example.com"} {
		s := New(Options{Port: 80, HTTPSPort: 443, PanelHost: func() string { return "panel.example.com" }, ServesHTTPS: func() bool { return https }})
		if got := s.GetPanelBaseURL(); got != want {
			t.Errorf("default port, https %v: %q, want %q", https, got, want)
		}
	}

	// The desktop sign-in through the console takes the host the app
	// reached the server at when nothing names the console's address, and
	// only a host name or an IP address
	for _, c := range []struct {
		name, env, panelHost, host, want string
	}{
		{name: "nothing set", host: "desk.example", want: "http://desk.example:5000"},
		{name: "an IPv4 address", host: "192.0.2.7", want: "http://192.0.2.7:5000"},
		{name: "an IPv6 address", host: "2001:db8::1", want: "http://[2001:db8::1]:5000"},
		{name: "upper case", host: "Desk.Example", want: "http://desk.example:5000"},
		{name: "no host", host: "", want: ""},
		{name: "not a host name", host: "desk.example/evil", want: ""},
		{name: "a user part", host: "me@evil.example", want: ""},
		{name: "a public URL wins", env: "https://panel.example.com/", host: "desk.example", want: "https://panel.example.com"},
		{name: "a public host wins", panelHost: "panel.example.com", host: "desk.example", want: "http://panel.example.com:5000"},
	} {
		env := map[string]string{"PANEL_PUBLIC_URL": c.env}
		s := New(Options{Getenv: func(name string) string { return env[name] }, Port: 5000, HTTPSPort: 5443, PanelHost: func() string { return c.panelHost }})
		if got := s.PanelBaseURLFor(c.host); got != c.want {
			t.Errorf("for a host, %s: %q, want %q", c.name, got, c.want)
		}
	}

	// Without an address the help request mail has no link
	db := storetest.Migrated(t)
	storeConfig(t, db, fullConfig)
	s, transport, _ := newTestService(db, nil, Options{Port: 5000})
	if ok, err := s.SendHelpRequestEmail(context.Background(), HelpRequestEmail{To: []string{"a@example.org"}, HelpRequest: HelpRequest{Hostname: jsbind.String("PC-01"), Message: jsbind.String("Need help")}}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	m := transport.sends[0].mail
	if strings.Contains(*m.HTML, "<a ") || strings.Contains(*m.HTML, "help-requests") || strings.Contains(*m.Text, "http") || len(m.Headers) != 1 {
		t.Errorf("a mail without an address has a link: text %q, headers %v", *m.Text, m.Headers)
	}
	if !strings.Contains(*m.Text, "An admin can change who gets these mails") || strings.Contains(*m.Text, "link below") {
		t.Errorf("a mail without an address points at a link: %q", *m.Text)
	}
}
