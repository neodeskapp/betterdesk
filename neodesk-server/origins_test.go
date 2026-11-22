package main

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
)

// TestOriginNoticesSayWhatTheSocketsAccept: without an allow list the
// start says which browser pages the WebSocket ports take: the signal and
// relay ports only pages from localhost, the API's event stream only pages
// from its own host. No notice claims that they accept every origin, and
// an allow list ends its notice (BUGS L227).
func TestOriginNoticesSayWhatTheSocketsAccept(t *testing.T) {
	notices := strings.Join(wsOriginNotices(config.DefaultConfig()), "\n")
	for _, want := range []string{
		"WS_ALLOWED_ORIGINS is not set: the signal and relay WebSocket ports take browser pages from localhost only",
		"API_WS_ALLOWED_ORIGINS is not set: the API event stream takes browser pages from its own host only",
	} {
		if !strings.Contains(notices, want) {
			t.Errorf("notices %q lack %q", notices, want)
		}
	}
	if strings.Contains(notices, "all origins") {
		t.Errorf("notices %q claim that every origin is accepted", notices)
	}

	cfg := config.DefaultConfig()
	cfg.AllowedWSOrigins = "https://desk.example.com"
	cfg.APIAllowedWSOrigins = "https://desk.example.com"
	if got := wsOriginNotices(cfg); len(got) != 0 {
		t.Errorf("notices with allow lists: %q", got)
	}
}
