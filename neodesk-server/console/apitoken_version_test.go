package console

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
)

// The answer to a request with an API token names the server's version
// (X-NeoDesk-Version), so the command line can say when it is older than
// the server it talks to. A browser's answers don't carry it.
func TestATokensAnswersNameTheServersVersion(t *testing.T) {
	w := newWorld(t, setup{configure: func(c *config.Config) { c.Version = "3.6.0-rc.5" }})
	value, _ := w.makeToken("super_admin", `{"name":"v","permissions":["audit.view"],"expires_in_days":1}`)
	if res := w.bearer(value).get("/api/panel/api-tokens/current"); res.status != 200 || res.header.Get("X-NeoDesk-Version") != "3.6.0-rc.5" {
		t.Errorf("a token's answer: %d, X-NeoDesk-Version %q", res.status, res.header.Get("X-NeoDesk-Version"))
	}
	if res := w.agent().signIn("super_admin").get("/api/panel/api-tokens"); res.header.Get("X-NeoDesk-Version") != "" {
		t.Errorf("a browser's answer names the version: %q", res.header.Get("X-NeoDesk-Version"))
	}
}
