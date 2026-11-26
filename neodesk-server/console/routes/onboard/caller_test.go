package onboard_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// liveRelay makes a relay session live in the shared registry, as signal
// and the relay do once both sides arrived: from initiator to device, for
// a console account and its browser session ("" for none).
func liveRelay(t *testing.T, uuid, initiator, device string, account int64, consoleSession string) {
	t.Helper()
	if !relay.AuthorizeRelayPairForSession(uuid, initiator, device, account, consoleSession) || !relay.ClaimRelayPair(uuid) || !relay.ClaimRelayPair(uuid) {
		t.Fatalf("relay session %s did not go live", uuid)
	}
}

// TestQuickSupportVouchesOnlyForASignedInHelper: the quick support app asks
// whether the connection that came without the password is a signed-in
// helper's browser viewer (A271). The server says so, with the helper's
// name as the console knows it, only for a live relay session to the
// session's device from a browser viewer of an account that may connect
// to it, after the person allowed quick support, and once per relay
// session; it writes the audit entry about the helper. Every other caller
// is not vouched for, and the app asks for the password.
func TestQuickSupportVouchesOnlyForASignedInHelper(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		suffix := strings.ReplaceAll(t.Name(), "/", "-")
		operator := w.browser("operator")
		made := operator.do(http.MethodPost, "/api/support/sessions", `{"name":"Ana"}`).json(t)
		app := w.app()
		token := str(app.do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`).json(t), "session_token")
		bearer := []string{"Authorization", "Bearer " + token}
		const device = "523456781"
		ask := func(session string) answer {
			t.Helper()
			return app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+session+`"}`, bearer...)
		}
		notVouched := func(why string, res answer, reason string) {
			t.Helper()
			if res.status != http.StatusConflict || str(res.json(t), "error") != "not_vouched" || str(res.json(t), "reason") != reason {
				t.Errorf("%s: %d %s, want 409 not_vouched %s", why, res.status, res.body, reason)
			}
		}

		app.do(http.MethodPost, "/api/support/session/device", `{"device_id":"`+device+`","hostname":"ana-laptop"}`, bearer...)
		good := "good-" + suffix
		liveRelay(t, good, "panel-web-remote", device, 5, "console-1")
		// Before the person allowed quick support nobody is vouched for
		notVouched("before Allow", ask(good), "session_state")
		app.do(http.MethodPost, "/api/support/session/consent", `{"allowed":true}`, bearer...)

		forged := "forged-" + suffix
		other := "other-" + suffix
		liveRelay(t, other, "panel-web-remote", "999999991", 5, "console-1")
		desktop := "desktop-" + suffix
		liveRelay(t, desktop, "828416401", device, 0, "")
		viewer := "viewer-" + suffix
		liveRelay(t, viewer, "panel-web-remote", device, 6, "console-6")
		notVouched("a relay session nobody opened", ask(forged), "session_not_open")
		notVouched("a relay session to another device", ask(other), "session_not_open")
		notVouched("a desktop app's relay session", ask(desktop), "session_not_open")
		notVouched("an account that may not connect", ask(viewer), "no_permission")
		if res := app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+good+`"}`); res.status != http.StatusUnauthorized {
			t.Errorf("without the session token: %d %s, want 401", res.status, res.body)
		}

		res := ask(good)
		if res.status != http.StatusOK {
			t.Fatalf("the helper's session: %d %s", res.status, res.body)
		}
		got := res.json(t)
		if got["vouched"] != true || str(got, "helper") != "Olga Operator" || str(got, "device_id") != device || str(got, "session") != good {
			t.Fatalf("the helper's session: %s", res.body)
		}
		// Once per relay session: a replay gets nothing
		notVouched("the same relay session again", ask(good), "already_used")

		var n int
		var who, about int64
		if err := sdb.QueryRow(context.Background(), `SELECT COUNT(*), COALESCE(MAX(user_id), 0), COALESCE(MAX(target_user_id), 0) FROM audit_log WHERE action = 'quick_support_caller'`).Scan(&n, &who, &about); err != nil {
			t.Fatal(err)
		}
		if n != 1 || who != 0 || about != 5 {
			t.Errorf("audit: %d entries by %d about %d, want 1 by nobody about account 5", n, who, about)
		}
		if err := sdb.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action = 'quick_support_caller_refused' AND target_user_id = 6`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the refused account's entry: %d %v, want 1", n, err)
		}
	})
}
