package onboard_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestQuickSupportNotesWhatThePersonAllowed: while a quick support session
// runs on a device, the server keeps a note of it with what the person
// allowed on the consent screen besides the screen, the mouse and the
// keyboard; the signal server turns everything else off for the session.
// The note goes when the session ends (L337).
func TestQuickSupportNotesWhatThePersonAllowed(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		made := w.browser("operator").do(http.MethodPost, "/api/support/sessions", `{"name":"Ana"}`).json(t)
		app := w.app()
		token := str(app.do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`).json(t), "session_token")
		bearer := []string{"Authorization", "Bearer " + token}
		step := func(path, body string) {
			t.Helper()
			if res := app.do(http.MethodPost, path, body, bearer...); res.status != http.StatusOK {
				t.Fatalf("%s: %d %s", path, res.status, res.body)
			}
		}
		note := func() (bool, []string) {
			t.Helper()
			raw, err := w.gdb.GetConfig("quick_support_device_523456789")
			if err != nil || raw == "" {
				return false, nil
			}
			var n struct {
				Grants []string `json:"grants"`
			}
			if err := json.Unmarshal([]byte(raw), &n); err != nil {
				t.Fatalf("note %q: %v", raw, err)
			}
			return true, n.Grants
		}

		step("/api/support/session/device", `{"device_id":"523456789","hostname":"ana-laptop"}`)
		if ok, grants := note(); !ok || len(grants) != 0 {
			t.Fatalf("while the person is asked: %v %v, want a note without grants", ok, grants)
		}
		step("/api/support/session/consent", `{"allowed":true,"grants":["clipboard","something-else"]}`)
		if ok, grants := note(); !ok || len(grants) != 1 || grants[0] != "clipboard" {
			t.Fatalf("after Allow with the clipboard: %v %v", ok, grants)
		}
		step("/api/support/session/end", `{}`)
		if ok, _ := note(); ok {
			t.Fatal("the note outlived the session")
		}
	})
}
