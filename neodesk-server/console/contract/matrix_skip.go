package contract

// SkipReason is why a route gets no placeholder request. These are the only
// reasons: a route that is merely slow, or that changes data other requests
// read, stays in the matrix.
type SkipReason string

const (
	// SkipServer: the route stops, restarts, updates or restores the
	// server or one of its services.
	SkipServer SkipReason = "stops, restarts, updates or restores the server or its services"
	// SkipShell: the route runs shell commands or opens a terminal.
	SkipShell SkipReason = "runs shell commands or a terminal"
	// SkipSession: the route ends or changes the test accounts' own
	// sessions, passwords or second factor.
	SkipSession SkipReason = "ends or changes the test accounts' sessions or passwords"
	// SkipLock: the route locks accounts out.
	SkipLock SkipReason = "locks accounts"
	// SkipEmail: the route sends email or talks to a mail server.
	SkipEmail SkipReason = "sends email"
	// SkipInternet: the route calls out to the internet.
	SkipInternet SkipReason = "calls out to the internet"
	// SkipCheckout: the route writes into the source folder the console
	// runs from. The test instances share that folder (the worktree), so
	// the write would reach the other instances and the repository.
	SkipCheckout SkipReason = "writes into the source folder the test instances share"
	// SkipLoad: the route puts the server under load for minutes, which
	// changes what the requests after it get.
	SkipLoad SkipReason = "puts the server under load for minutes"
	// SkipPages: the route is the web app's pages. They get cases from the
	// route list of the web app's build.
	SkipPages SkipReason = "serves the web app's pages, which get their own cases"
)

// Skip is a route the matrix leaves out. Each needs hand-written cases
// (TestEveryRouteIsCovered lists the ones that have none).
type Skip struct {
	// ID is the route's ID in testdata/routes.json.
	ID     string
	Reason SkipReason
	// Why says what the handler does, for the reader.
	Why string
}

// matrixSkips is the skip list. It was made by reading every handler, with
// this rule:
//
//   - A route whose job is to stop, restart, update or restore the server,
//     to run a command on it, to end or change the caller's session,
//     password or second factor, or to send mail is left out even when it
//     refuses an empty body: one changed validation line would turn the
//     placeholder into the real thing.
//   - A route that calls out to the internet, writes into the source folder
//     or loads the server is left out when the placeholder request (or an
//     earlier one of the matrix) gets it that far. A route that needs a
//     host or a file in the body and refuses {} stays in.
//
// Kept in on purpose: the read-only status routes that run a fixed command
// (ps, df, journalctl, docker ps, systemctl list-units, go version) with
// nothing from the request in it, and the routes that pass a command to a
// device, which the placeholder device does not exist for.
var matrixSkips = []Skip{
	// Sessions, passwords, second factor (routes/auth.routes.js).
	{"POST /api/auth/logout", SkipSession, "Ends the caller's session, whatever the body."},
	{"GET /logout", SkipSession, "Ends the caller's session and redirects to /login."},
	{"POST /api/auth/password", SkipSession, "Changes the caller's password. An empty body is refused, but it still counts against the limit of three per five minutes it shares with creating users and resetting passwords."},
	{"POST /api/auth/totp/setup", SkipSession, "Writes a new TOTP secret to the caller's account, with an empty body too."},
	{"POST /api/auth/totp/enable", SkipSession, "Switches two-factor sign-in on for the caller."},
	{"POST /api/auth/totp/disable", SkipSession, "Switches two-factor sign-in off for the caller."},

	// Restart, update, restore, service control (routes/settings.routes.js,
	// routes/server-management.routes.js).
	{"POST /api/settings/restart/confirm", SkipServer, "Carries out a pending restart of the server."},
	{"POST /api/settings/advanced/restart", SkipServer, "Restarts the service a configuration file belongs to."},
	{"POST /api/settings/connection-mode/restart", SkipServer, "Runs docker compose up for the server and ends the console process when the caller has a pending restart; PUT /api/settings/connection-mode with {} creates one."},
	{"POST /api/settings/restore", SkipServer, "Restores the console from an uploaded backup."},
	{"POST /api/settings/updates/install", SkipServer, "Installs a console update from GitHub."},
	{"POST /api/settings/updates/restore", SkipServer, "Puts an update backup back and ends the process."},
	{"POST /api/settings/updates/install-go", SkipServer, "Needs no body: downloads the Go toolchain from go.dev, unpacks and runs it."},
	{"POST /api/settings/updates/server-binary/rebuild", SkipServer, "Needs no body: overwrites neodesk-server in the source folder with the GitHub version, builds it and restarts the server."},
	{"POST /api/server-management/services/:name/:action", SkipServer, "Starts, stops or restarts a system service."},

	// Commands on the server.
	{"POST /api/system/exec", SkipShell, "Runs the command in the body in a shell on the server."},
	{"PUT /api/panel/billing/clock/settings", SkipShell, "Accepts {} (every field has a default), rewrites web-nodejs/.env and runs systemctl through a shell."},

	// Mail.
	{"POST /api/settings/email/smtp/test", SkipEmail, "Connects to the stored mail server to test it."},
	{"POST /api/automation/smtp/test", SkipEmail, "Connects to the stored mail server to test it."},

	// Calls to the internet that happen without anything in the request.
	{"GET /api/settings/updates/check", SkipInternet, "Asks api.github.com for the newest commit."},
	{"GET /api/settings/updates/last-result", SkipInternet, "Asks api.github.com for the newest commit before it reads the stored result."},
	{"GET /api/settings/updates/preflight", SkipInternet, "Ends with a request to api.github.com when the console runs from source."},
	{"POST /api/panel/billing/timesync/check", SkipInternet, "Makes the Go server query the public NTP servers."},
	{"POST /api/agent-releases/sync", SkipInternet, "Fetches the release list from api.github.com and downloads every asset."},
	{"POST /api/generator/module/install", SkipInternet, "Downloads the client templates from GitHub once the terms are accepted, which POST /api/generator/module/accept-terms with {} does."},

	// Writes into the shared source folder.
	{"PUT /api/settings/connection-mode", SkipCheckout, "With {} it rewrites docker-compose.yml in the repository root and registers a restart."},
	{"PUT /api/settings/client-sessions", SkipCheckout, "With {} it rewrites web-nodejs/.env (API_TOKEN_EXPIRY_DAYS)."},
	{"PUT /api/settings/public-endpoints", SkipCheckout, "With {} it rewrites web-nodejs/.env with empty public addresses."},

	// The web app's pages.
	{"GET /*", SkipPages, "Answers every GET nothing else takes with a page of the web app, a redirect to sign-in or an error page."},

	// Load.
	{"POST /api/server-attestation/run", SkipLoad, "Starts a load test with up to 500 connections that runs for 25 seconds to 10 minutes; the next caller gets 409 while it runs."},
}

func skipIndex() map[string]Skip {
	index := make(map[string]Skip, len(matrixSkips))
	for _, skip := range matrixSkips {
		index[skip.ID] = skip
	}
	return index
}
