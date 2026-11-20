package cdap_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/cdap"
)

// The value a cancel of the gateway switch writes back is the setting's
// value when the API server named one as text, else "N"; the API
// server's whole answer is never taken for it (BUGS RC5).
func TestToggleReturnsToTheSettingsText(t *testing.T) {
	api := &configServer{save: http.StatusOK}
	dataDir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(dataDir, ".restart-transaction.json")
	w := proxytest.NewWorld(t, func(d *kit.Deps) {
		d.GoAPI = goapi.New(api)
		d.Restart = restart.New(restart.Options{DataDir: dataDir, GoAPI: d.GoAPI})
		cdap.Register(d)
	})
	body := `{"enabled":true}`
	for _, c := range []struct{ read, want string }{
		{`{"key":"cdap_enabled","value":"Y"}`, `"Y"`},
		{`{"key":"cdap_enabled","value":""}`, `""`},
		{`{"key":"cdap_enabled"}`, `"N"`},
		{`{"key":"cdap_enabled","value":true}`, `"N"`},
		{`{"value":{"nested":[1]}}`, `"N"`},
		{`"Y"`, `"N"`},
		{`null`, `"N"`},
	} {
		_ = os.Remove(statePath)
		api.read = c.read
		rec, _ := w.Do(proxytest.Request{Method: "POST", Path: "/api/cdap/toggle", Session: account(1, "admin"), Type: "application/json", Body: &body}, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("read %s: toggle %d %s", c.read, rec.Code, rec.Body)
			continue
		}
		state, err := os.ReadFile(statePath)
		want := `"rollback": {` + "\n" + `        "type": "go-config",` + "\n" + `        "key": "cdap_enabled",` + "\n" + `        "value": ` + c.want + "\n"
		if err != nil || !strings.Contains(string(state), want) {
			t.Errorf("read %s: the transaction holds\n%s\nwant the rollback value %s", c.read, state, c.want)
		}
	}
}
