package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// TestSupportCallerNamesTheAccountOfAFreshBrowserSession: the server
// vouches for a quick support caller (A271) only for a relay session that
// runs to that device, from the browser viewer of a signed-in console
// session, in its first minute, and once. It names the account; the
// console checks the account.
func TestSupportCallerNamesTheAccountOfAFreshBrowserSession(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	now := time.Unix(1_800_000_000, 0)
	srv.now = func() time.Time { return now }
	fresh := now.Add(-10 * time.Second)
	srv.signinLiveFn = func(uuid string) (relay.LivePair, bool) {
		pairs := map[string]relay.LivePair{
			"sess-a":      {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1", Since: fresh},
			"sess-b":      {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 6, ConsoleSession: "console-2", Since: fresh},
			"sess-other":  {Initiator: webRemoteInitiator, Target: "PEERBBB2", Account: 5, ConsoleSession: "console-1", Since: fresh},
			"sess-app":    {Initiator: "123456789", Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1", Since: fresh},
			"sess-guest":  {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 0, Since: fresh},
			"sess-token":  {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, Since: fresh},
			"sess-old":    {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1", Since: now.Add(-61 * time.Second)},
			"sess-nodate": {Initiator: webRemoteInitiator, Target: "PEERAAA1", Account: 5, ConsoleSession: "console-1"},
		}
		p, ok := pairs[uuid]
		return p, ok
	}
	ask := func(device, session string) (int, string) {
		return asConsole(t, srv, http.MethodPost, "/api/support/caller", `{"device_id":"`+device+`","session":"`+session+`"}`)
	}
	for _, c := range []struct {
		device, session, want, why string
	}{
		{"PEERAAA1", "no-such-session", "session_not_open", "a session that does not run"},
		{"PEERAAA1", "sess-other", "session_not_open", "a session to another device"},
		{"PEERAAA1", "sess-app", "session_not_open", "a session a desktop app opened"},
		{"PEERAAA1", "sess-guest", "session_not_open", "a session of no account"},
		{"PEERAAA1", "sess-token", "session_not_open", "a session no browser session is named for"},
		{"PEERAAA1", "sess-old", "too_late", "a session that met on the relay over a minute ago"},
		{"PEERAAA1", "sess-nodate", "too_late", "a session without a start"},
	} {
		if status, body := ask(c.device, c.session); status != http.StatusConflict || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s, want 409 %s", c.why, status, body, c.want)
		}
	}
	if status, body := ask("PEERAAA1", ""); status != http.StatusBadRequest {
		t.Errorf("no session: %d %s, want 400", status, body)
	}
	status, body := ask("PEERAAA1", "sess-a")
	if status != http.StatusOK || body != `{"account":5}` {
		t.Fatalf("a fresh browser session: %d %s, want 200 account 5", status, body)
	}
	if status, body := ask("PEERAAA1", "sess-a"); status != http.StatusConflict || !strings.Contains(body, "already_used") {
		t.Errorf("the same session again: %d %s, want 409 already_used", status, body)
	}
	if status, body := ask("PEERAAA1", "sess-b"); status != http.StatusOK || body != `{"account":6}` {
		t.Errorf("another session: %d %s, want 200 account 6", status, body)
	}
	// Only the console in this process asks; the API port answers 404
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/support/caller", `{"device_id":"PEERAAA1","session":"sess-a"}`,
		map[string]string{"X-API-Key": testAPIKey}); status != http.StatusNotFound {
		t.Errorf("on the listener: %d %s, want 404", status, body)
	}
}
