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
		// The app reaches the console over HTTPS (through the trusted proxy)
		bearer := []string{"Authorization", "Bearer " + token, "X-Forwarded-Proto", "https"}
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

// supportOnDevice opens a quick support session of the operator that the
// person allowed on device, with the app's requests over https or not.
func supportOnDevice(t *testing.T, w *world, device string, https bool) (app *browser, header []string) {
	t.Helper()
	made := w.browser("operator").do(http.MethodPost, "/api/support/sessions", `{"name":"Ana"}`).json(t)
	app = w.app()
	token := str(app.do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`).json(t), "session_token")
	header = []string{"Authorization", "Bearer " + token}
	if https {
		header = append(header, "X-Forwarded-Proto", "https")
	}
	app.do(http.MethodPost, "/api/support/session/device", `{"device_id":"`+device+`","hostname":"ana-laptop"}`, header...)
	app.do(http.MethodPost, "/api/support/session/consent", `{"allowed":true}`, header...)
	return app, header
}

// TestQuickSupportVouchesOnlyOverHTTPS: accepting by click needs HTTPS
// (A273). A quick support app that reaches the console over plain HTTP
// gets no vouch, even for the helper's own relay session, and the refusal
// does not spend the session.
func TestQuickSupportVouchesOnlyOverHTTPS(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	const device = "523456782"
	app, header := supportOnDevice(t, w, device, false)
	relaySession := "plain-" + t.Name()
	liveRelay(t, relaySession, "panel-web-remote", device, 5, "console-1")
	res := app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+relaySession+`"}`, header...)
	if res.status != http.StatusConflict || str(res.json(t), "reason") != "not_https" {
		t.Fatalf("over plain HTTP: %d %s, want 409 not_https", res.status, res.body)
	}
	// The refusal did not spend the relay session: over HTTPS it vouches
	res = app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+relaySession+`"}`, append(header, "X-Forwarded-Proto", "https")...)
	if res.status != http.StatusOK {
		t.Fatalf("over HTTPS after a plain refusal: %d %s, want 200", res.status, res.body)
	}
}

// TestTheViewerLearnsWhyThereIsNoAllow: the helper's viewer asks what the
// quick support app was told about its relay session (A273): nothing yet,
// that the person has not clicked Allow yet, that the app is on plain
// HTTP, or that it was vouched for; and whether the app reaches the
// console over HTTPS. Only an account with a quick support session on the
// device may ask.
func TestTheViewerLearnsWhyThereIsNoAllow(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	operator := w.browser("operator")
	state := func(device, relaySession string) map[string]any {
		t.Helper()
		res := operator.do(http.MethodPost, "/api/support/devices/"+device+"/caller", `{"session":"`+relaySession+`"}`)
		if res.status != http.StatusOK {
			t.Fatalf("caller state of %s: %d %s", device, res.status, res.body)
		}
		return res.json(t)
	}
	suffix := strings.ReplaceAll(t.Name(), "/", "-")

	// Before Allow: the app was told the person has not allowed it yet
	const early = "523456783"
	made := operator.do(http.MethodPost, "/api/support/sessions", `{"name":"Ana"}`).json(t)
	app := w.app()
	token := str(app.do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`).json(t), "session_token")
	header := []string{"Authorization", "Bearer " + token, "X-Forwarded-Proto", "https"}
	app.do(http.MethodPost, "/api/support/session/device", `{"device_id":"`+early+`","hostname":"ana-laptop"}`, header...)
	earlySession := "early-" + suffix
	liveRelay(t, earlySession, "panel-web-remote", early, 5, "console-1")
	if got := state(early, earlySession); str(got, "state") != "not_asked" || got["app_https"] != true {
		t.Fatalf("before the app asked: %v, want not_asked over HTTPS", got)
	}
	app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+earlySession+`"}`, header...)
	if got := state(early, earlySession); str(got, "state") != "not_allowed_yet" {
		t.Fatalf("before Allow: %v, want not_allowed_yet", got)
	}

	// Vouched
	const fine = "523456784"
	app, header = supportOnDevice(t, w, fine, true)
	fineSession := "fine-" + suffix
	liveRelay(t, fineSession, "panel-web-remote", fine, 5, "console-1")
	app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+fineSession+`"}`, header...)
	if got := state(fine, fineSession); str(got, "state") != "vouched" {
		t.Fatalf("vouched: %v", got)
	}
	// Another device's relay session says nothing about this one
	if got := state(fine, earlySession); str(got, "state") != "not_asked" {
		t.Fatalf("another device's relay session: %v, want not_asked", got)
	}

	// Plain HTTP: refused for it, and the viewer sees the app is not on HTTPS
	const plain = "523456785"
	app, header = supportOnDevice(t, w, plain, false)
	plainSession := "plainview-" + suffix
	if got := state(plain, plainSession); str(got, "state") != "not_asked" || got["app_https"] != false {
		t.Fatalf("an app on plain HTTP that did not ask: %v, want not_asked and app_https false", got)
	}
	app.do(http.MethodPost, "/api/support/session/caller", `{"session":"`+plainSession+`"}`, header...)
	if got := state(plain, plainSession); str(got, "state") != "not_https" {
		t.Fatalf("an app on plain HTTP that asked: %v, want not_https", got)
	}

	// Another account has no quick support session on the device
	if res := w.browser("admin").do(http.MethodPost, "/api/support/devices/"+fine+"/caller", `{"session":"`+fineSession+`"}`); res.status != http.StatusNotFound {
		t.Fatalf("another account: %d %s, want 404", res.status, res.body)
	}
	if res := operator.do(http.MethodPost, "/api/support/devices/"+fine+"/caller", `{}`); res.status != http.StatusBadRequest {
		t.Fatalf("no relay session: %d %s, want 400", res.status, res.body)
	}
}
