package console

import (
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/cdapstudio"
	"github.com/unitronix/betterdesk-server/console/routes/chat"
	consoleroutes "github.com/unitronix/betterdesk-server/console/routes/console"
	"github.com/unitronix/betterdesk-server/console/routes/dashboard"
	"github.com/unitronix/betterdesk-server/console/routes/health"
	i18nroutes "github.com/unitronix/betterdesk-server/console/routes/i18n"
	"github.com/unitronix/betterdesk-server/console/routes/keys"
	"github.com/unitronix/betterdesk-server/console/routes/languages"
	"github.com/unitronix/betterdesk-server/console/routes/meshcentral"
	"github.com/unitronix/betterdesk-server/console/routes/pairing"
	"github.com/unitronix/betterdesk-server/console/routes/uishell"
)

// registerRoutes adds the route groups that have moved to Go, one line
// per group and mount, in the order of web-nodejs/routes/index.js: the
// tables answer with the first route that fits, so the order of the
// groups decides between two routes for one address.
//
// Groups of the early stage (routes/rustdesk-api.routes.js, then
// routes/bd-api.routes.js at /api/bd) register on d.Early and go first.
func registerRoutes(d *kit.Deps) {
	health.Register(d)        // routes/index.js: GET /health
	consoleroutes.Register(d) // routes/console.routes.js: the strings endpoint
	dashboard.Register(d)     // routes/dashboard.routes.js
	keys.Register(d)          // routes/keys.routes.js
	uishell.Register(d)       // routes/ui-shell.routes.js
	i18nroutes.Register(d)    // routes/i18n.routes.js at /api/i18n
	meshcentral.Register(d)   // routes/meshcentral.routes.js
	chat.Register(d)          // routes/chat.routes.js
	languages.Register(d)     // routes/languages.routes.js
	cdapstudio.Register(d)    // routes/cdap-studio.routes.js
	pairing.Register(d)       // routes/pairing.routes.js
}
