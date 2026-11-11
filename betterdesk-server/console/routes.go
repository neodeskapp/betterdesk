package console

import (
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/activity"
	authroutes "github.com/unitronix/betterdesk-server/console/routes/auth"
	"github.com/unitronix/betterdesk-server/console/routes/automation"
	"github.com/unitronix/betterdesk-server/console/routes/avatars"
	"github.com/unitronix/betterdesk-server/console/routes/cdap"
	"github.com/unitronix/betterdesk-server/console/routes/cdapstudio"
	"github.com/unitronix/betterdesk-server/console/routes/chat"
	consoleroutes "github.com/unitronix/betterdesk-server/console/routes/console"
	"github.com/unitronix/betterdesk-server/console/routes/crossplatform"
	"github.com/unitronix/betterdesk-server/console/routes/dashboard"
	"github.com/unitronix/betterdesk-server/console/routes/dataguard"
	"github.com/unitronix/betterdesk-server/console/routes/desktop"
	"github.com/unitronix/betterdesk-server/console/routes/devices"
	"github.com/unitronix/betterdesk-server/console/routes/filetransfer"
	"github.com/unitronix/betterdesk-server/console/routes/fleet"
	"github.com/unitronix/betterdesk-server/console/routes/folders"
	"github.com/unitronix/betterdesk-server/console/routes/guest"
	"github.com/unitronix/betterdesk-server/console/routes/health"
	i18nroutes "github.com/unitronix/betterdesk-server/console/routes/i18n"
	"github.com/unitronix/betterdesk-server/console/routes/inventory"
	"github.com/unitronix/betterdesk-server/console/routes/keys"
	"github.com/unitronix/betterdesk-server/console/routes/languages"
	"github.com/unitronix/betterdesk-server/console/routes/meshcentral"
	"github.com/unitronix/betterdesk-server/console/routes/network"
	"github.com/unitronix/betterdesk-server/console/routes/organizations"
	"github.com/unitronix/betterdesk-server/console/routes/pairing"
	"github.com/unitronix/betterdesk-server/console/routes/panelaccess"
	"github.com/unitronix/betterdesk-server/console/routes/permissions"
	"github.com/unitronix/betterdesk-server/console/routes/phase45"
	"github.com/unitronix/betterdesk-server/console/routes/policies"
	"github.com/unitronix/betterdesk-server/console/routes/reports"
	"github.com/unitronix/betterdesk-server/console/routes/resourcecontrol"
	"github.com/unitronix/betterdesk-server/console/routes/rustdeskapi"
	"github.com/unitronix/betterdesk-server/console/routes/scaling"
	"github.com/unitronix/betterdesk-server/console/routes/securityaudit"
	"github.com/unitronix/betterdesk-server/console/routes/settings"
	"github.com/unitronix/betterdesk-server/console/routes/system"
	"github.com/unitronix/betterdesk-server/console/routes/tenants"
	"github.com/unitronix/betterdesk-server/console/routes/tickets"
	"github.com/unitronix/betterdesk-server/console/routes/tokens"
	"github.com/unitronix/betterdesk-server/console/routes/toolkit"
	"github.com/unitronix/betterdesk-server/console/routes/trends"
	"github.com/unitronix/betterdesk-server/console/routes/uishell"
	"github.com/unitronix/betterdesk-server/console/routes/users"
)

// registerRoutes adds the route groups that have moved to Go, one line
// per group and mount, in the order of web-nodejs/routes/index.js: the
// tables answer with the first route that fits, so the order of the
// groups decides between two routes for one address.
//
// Groups of the early stage (routes/rustdesk-api.routes.js, then
// routes/bd-api.routes.js at /api/bd) register on d.Early and go first.
func registerRoutes(d *kit.Deps) {
	rustdeskapi.Register(d)   // routes/rustdesk-api.routes.js (server.js), early stage
	health.Register(d)        // routes/index.js: GET /health
	authroutes.Register(d)    // routes/auth.routes.js
	consoleroutes.Register(d) // routes/console.routes.js
	dashboard.Register(d)     // routes/dashboard.routes.js
	devices.Register(d)       // routes/devices.routes.js, without the routes that reach into an agent (bdRelay)
	keys.Register(d)          // routes/keys.routes.js
	settings.Register(d)      // routes/settings.routes.js: the general settings and the branding reads
	avatars.Register(d)       // routes/avatars.routes.js
	users.Register(d)         // routes/users.routes.js
	panelaccess.Register(d)   // routes/panel-access.routes.js
	uishell.Register(d)       // routes/ui-shell.routes.js
	folders.Register(d)       // routes/folders.routes.js
	guest.Register(d)         // routes/guest.routes.js
	i18nroutes.Register(d)    // routes/i18n.routes.js at /api/i18n
	// routes/dataguard.routes.js at /api/dataguard and at /api/bd
	// routes/inventory.routes.js at /api/inventory, without the device routes (inventory, telemetry, inventory/:id), which are all the group answers at /api/bd
	inventory.Register(d.At("/api/inventory"))
	// routes/tickets.routes.js at /api/tickets
	tickets.Register(d.At("/api/tickets"))
	// routes/activity.routes.js at /api/activity, without the device route /activity, which is all the group answers at /api/bd
	activity.Register(d.At("/api/activity"))
	// routes/automation.routes.js at /api/automation, without the device routes (commands, commands/:id/result), which are all the group answers at /api/bd
	automation.Register(d.At("/api/automation"))
	// routes/fileTransfer.routes.js at /api/files and at /api/bd, with the device routes file-transfer
	filetransfer.Register(d.At("/api/files"))
	filetransfer.Register(d.At("/api/bd"))
	// routes/network.routes.js at /api/network
	network.Register(d.At("/api/network"))
	// routes/dataguard.routes.js at /api/dataguard and at /api/bd, without the device routes dlp-policies and dlp-events
	dataguard.Register(d.At("/api/dataguard"))
	dataguard.Register(d.At("/api/bd"))
	trends.Register(d) // routes/trends.routes.js, in front of the reports group
	// routes/reports.routes.js at /api/reports
	reports.Register(d.At("/api/reports"))
	// routes/tenants.routes.js at /api/tenants
	tenants.Register(d.At("/api/tenants"))
	cdap.Register(d)                          // routes/cdap.routes.js
	meshcentral.Register(d)                   // routes/meshcentral.routes.js
	chat.Register(d)                          // routes/chat.routes.js
	tokens.Register(d)                        // routes/tokens.routes.js
	desktop.Register(d.At("/api/desktop"))    // routes/desktop.routes.js at /api/desktop
	organizations.Register(d)                 // routes/organizations.routes.js
	policies.Register(d)                      // routes/policies.routes.js
	policies.Register(d.At("/api/bd"))        // its second mount: /api/bd/api/panel/policies/...
	fleet.Register(d)                         // routes/fleet.routes.js
	fleet.Register(d.At("/api/bd"))           // its second mount: /api/bd/api/panel/fleet/...
	scaling.Register(d)                       // routes/scaling.routes.js
	scaling.Register(d.At("/api/bd"))         // routes/scaling.routes.js at /api/bd
	crossplatform.Register(d)                 // routes/cross-platform.routes.js
	toolkit.Register(d)                       // routes/toolkit.routes.js
	securityaudit.Register(d)                 // routes/security-audit.routes.js
	languages.Register(d)                     // routes/languages.routes.js
	resourcecontrol.Register(d)               // routes/resource-control.routes.js
	resourcecontrol.Register(d.At("/api/bd")) // its second mount: /api/bd/api/panel/resource-control/...
	system.Register(d)                        // routes/system.routes.js
	cdapstudio.Register(d)                    // routes/cdap-studio.routes.js
	permissions.Register(d)                   // routes/permissions.routes.js
	phase45.Register(d)                       // routes/phase4_5.routes.js
	pairing.Register(d)                       // routes/pairing.routes.js
}
