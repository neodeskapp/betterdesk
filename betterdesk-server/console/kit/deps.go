package kit

import (
	"context"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/alerts"
	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/csrf"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/filetransfer"
	"github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// Jobs is where a group adds background work that runs for as long as
// the console does (package console/jobs). run must return when its
// context ends.
type Jobs interface {
	Add(name string, run func(ctx context.Context))
}

// Deps is what a route group gets in Register: the two route tables, the
// console's services, and the guards as methods (see guards.go).
type Deps struct {
	// Router is the table of the console's routes (step 18 of the
	// pipeline): behind the session, the panel access guard, the rate
	// limiters, the request language, the CSRF gate and the JSON
	// content-type rule.
	Router *Router
	// Early is the table of the RustDesk client API and the device API
	// under /api/bd (steps 12 and 13): behind the session, the guard and
	// the limiters, but before the request language and the CSRF gate. Its
	// answers are never compressed. A handler there that calls PassOn
	// leaves the request to Router.
	Early *Router

	// Config is the console's configuration.
	Config *config.Config
	// Store is the console's database.
	Store *store.DB
	// Sessions is the session manager (sign-in, sign-out, session list).
	Sessions *session.Manager
	// Authn is the sign-in service: passwords, the second factor, lockouts,
	// access tokens and device tokens (services/authService.js).
	Authn *authn.Service
	// CSRF hands out and checks CSRF tokens. The pipeline already checks
	// every write that reaches Router.
	CSRF *csrf.Protector
	// Access decides permissions, panel access and device scope.
	Access *access.Service
	// Limits holds the rate limiters: d.Limits.Upload.Middleware goes on
	// a route like uploadLimiter did.
	Limits *limits.Set
	// I18n holds the languages (I18n.Bundle) and the language routes.
	I18n *i18n.Handlers
	// WebUI serves the web app and its error pages.
	WebUI *webui.Host
	// GoAPI is the client for the API server in this process
	// (services/betterdeskApi.js).
	GoAPI *goapi.Client
	// ServerInfo answers how clients reach this server: the keys, the
	// addresses, the client configuration (services/keyService.js,
	// services/clientConfigHost.js and the services next to them).
	ServerInfo *serverinfo.Service
	// Avatars keeps the profile pictures of console users
	// (services/avatarService.js, lib/avatarImage.js).
	Avatars *avatars.Service
	// GuestAccess reads and checks the tokens of guest access links
	// (middleware/guestAccess.js).
	GuestAccess *guestaccess.Service
	// FileTransfer keeps the file transfers between the console and
	// devices (services/fileTransferService.js).
	FileTransfer *filetransfer.Service
	// Restart collects the saved settings that need a restart of the
	// server and runs that restart (services/restartCoordinator.js).
	Restart *restart.Service
	// DeviceGroups holds folders, device groups and user groups and
	// answers which devices an account may see (services/deviceGroupService.js
	// with its dbAdapter methods).
	DeviceGroups *devicegroups.Service
	// Backend is the device layer: the API server's devices with what the
	// console's tables add to them (services/serverBackend.js).
	Backend *goapi.Backend
	// Branding is the console's own look: the appearance settings, the
	// stylesheet made from them, profiles, themes, fonts and the supporters
	// list (services/brandingService.js, fontService.js,
	// supportersService.js). A route that reads the settings calls
	// Branding.Refresh first.
	Branding *branding.Service
	// Mail is the mail service: the SMTP settings, the alert and help
	// request mails, the log of notifications sent
	// (services/emailService.js, lib/smtpSettingsHandlers.js).
	Mail *mail.Service
	// Alerts is the alert rules engine (services/alertRulesEngine.js).
	Alerts *alerts.Engine
	// Generator is the support client generator: bundles, the template
	// module, the client builder and the build queue
	// (services/agentBundleService.js, supportGeneratorModule.js,
	// clientBuilderService.js, clientTemplateWorker.js).
	Generator *generator.Service
	// Audit writes the audit log. LogAction is the usual way to it.
	Audit *auditlog.Log
	// Log is the console's logger; a group takes d.Log.Child("TICKETS").
	Log *Logger
	// Jobs takes background work that stops with the console.
	Jobs Jobs
}

// At returns the same Deps with both route tables mounted at prefix, for
// a group that the old console mounted there:
//
//	inventory.Register(d.At("/api/inventory"))
//	inventory.Register(d.At("/api/bd"))
//
// The group writes its patterns below the mount ("/", "/{id}").
func (d *Deps) At(prefix string) *Deps {
	mounted := *d
	if d.Router != nil {
		mounted.Router = d.Router.Mount(prefix)
	}
	if d.Early != nil {
		mounted.Early = d.Early.Mount(prefix)
	}
	return &mounted
}

// LogAction writes an audit log entry for the signed-in account and the
// client's address: db.logAction(req.session.userId, action, details,
// req.ip). Most routes ignore the error, some answer 500 on it; the old
// route says which.
func (d *Deps) LogAction(r *http.Request, action, details string) error {
	return d.Audit.Write(r.Context(), UserID(r), action, details, ClientIP(r))
}
