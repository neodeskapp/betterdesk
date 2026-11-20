// Package console serves the web console on its own port: the JSON API, the
// websockets and the built web app (web-ui/build).
//
// Every request passes one pipeline (pipeline.go) whose steps and order
// are those of the Node console's server.js. Route groups add their routes
// to one of two tables: Early, for the client and device API that sit
// before the request language and the CSRF gate, and Router, for
// everything else (routes.go).
//
// While routes move from the Node console to Go, the Node console runs
// behind this one (CONSOLE_NODE_UPSTREAM). A request then passes the Go
// pipeline only when a Go route has its method and path, and a websocket
// upgrade goes to a Go socket only when one is registered for its path
// (sockets.go, package console/ws). Everything else goes to the Node
// console untouched, and the Node console runs its own pipeline: Go
// answers nothing it has no route for, no 404, no preflight, no file and
// no page. A Go route that declines a request (kit.PassOn) with no later
// Go route to take it hands the request on to the Node console as it
// arrived, body included.
package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/alerts"
	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/authimport"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/bdrelay"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/csrf"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/discovery"
	"github.com/unitronix/betterdesk-server/console/filetransfer"
	"github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/helpmail"
	"github.com/unitronix/betterdesk-server/console/housekeeping"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/jobs"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/listen"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/proxy"
	"github.com/unitronix/betterdesk-server/console/qr"
	"github.com/unitronix/betterdesk-server/console/remoterelay"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/servermgmt"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/statuspush"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/updater"
	"github.com/unitronix/betterdesk-server/console/webui"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/events"
	"github.com/unitronix/betterdesk-server/logging"
)

// Options are what the console needs from the server it runs in.
type Options struct {
	// Config is the console's configuration.
	Config *config.Config
	// Store is the console's database, migrated. The caller closes it
	// after Stop.
	Store *store.DB
	// API is the API server's handler for calls from inside the process
	// (api.Server.InternalHandler).
	API http.Handler
	// Events is the server's event bus. nil turns the event stream of
	// GoAPI off.
	Events *events.Bus
	// Roles is the server's database with the role overrides. nil leaves
	// every role with its built-in permissions.
	Roles access.OverrideStore
	// Version and Commit are the running binary's (main.Version and
	// main.Commit), for the updater.
	Version, Commit string
	// Exit asks the process to end with the exit status that makes its
	// supervisor start it again (DECISIONS A30). The console calls it only
	// when an installer named a supervisor (BETTERDESK_SUPERVISOR): after
	// a confirmed restart, an installed update and a restore. Nil never
	// ends the process.
	Exit func()
	// DataBackup makes the data backup before an update
	// (console/backup's Service). Nil makes none.
	DataBackup updater.DataBackup
}

// Server is the console's HTTP server.
type Server struct {
	cfg      *config.Config
	deps     *kit.Deps
	early    *kit.Router
	router   *kit.Router
	node     *proxy.Proxy
	routing  http.Handler
	pipeline http.Handler
	jobs     *jobs.Registry
	// net are the listeners (listeners.go)
	net listeners
	// authImport says where the old console's auth.db is (Start imports
	// it).
	authImport authimport.Options
}

// New builds the console server: its services, the pipeline and the
// routes of every group that has moved to Go. Nothing runs before Start.
func New(ctx context.Context, opts Options) (*Server, error) {
	cfg := opts.Config
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	s := &Server{cfg: cfg, early: kit.NewRouter(), router: kit.NewRouter(), jobs: jobs.New()}
	if cfg.NodeUpstream != nil {
		s.node = proxy.New(cfg.NodeUpstream, cfg.TrustProxy)
	}
	if cfg.DebugRouting {
		s.routing = proxy.Routing(s.handles, cfg.NodeUpstream != nil)
	}

	if err := os.MkdirAll(cfg.UploadsDir, 0o755); err != nil {
		logging.Logf("console", "WARN: cannot create the uploads folder %s: %v", cfg.UploadsDir, err)
	}
	if err := s.loadCertificates(); err != nil {
		return nil, err
	}
	secret, err := session.LoadSecret(getenv, cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("session secret: %w", err)
	}

	audit := auditlog.New(opts.Store)
	if cfg.WebUIDir == "" && !webui.HasEmbeddedBuild() {
		logging.Logf("console", "WARN: this binary was built without the web app (go run ./cmd/embed-webui before go build) and WEB_UI_DIR is not set: the console answers no pages")
	}
	host := webui.New(webui.Options{
		Embedded:   cfg.WebUIDir == "",
		Dir:        cfg.WebUIDir,
		HTTPS:      cfg.HTTPS,
		Production: cfg.Production,
		Session: func(r *http.Request) (bool, string) {
			user := kit.User(r)
			if user == nil {
				return false, ""
			}
			return true, user.Role
		},
		Secure: func(r *http.Request) bool { return cfg.TrustProxy.Scheme(r) == "https" },
	})
	sessions, err := session.New(ctx, session.Options{
		DB:         opts.Store,
		Secret:     secret,
		HTTPS:      cfg.HTTPS,
		Policy:     session.PolicyFromEnv(getenv),
		TrustProxy: cfg.TrustProxy,
		Audit: func(ctx context.Context, userID int64, action, details, ip string) {
			if err := audit.Write(ctx, userID, action, details, ip); err != nil {
				logging.Logf("console", "WARN: %v", err)
			}
		},
		OnError: func(w http.ResponseWriter, r *http.Request, err error) {
			host.ServerError(w, r, err, http.StatusInternalServerError)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	protector, err := csrf.New(csrf.Options{
		Secret:    secret,
		HTTPS:     cfg.HTTPS,
		SignedIn:  func(r *http.Request) bool { return kit.UserID(r) != 0 },
		BodyToken: csrfBodyToken,
		OnError:   host.CSRFError,
	})
	if err != nil {
		return nil, fmt.Errorf("csrf: %w", err)
	}

	bundle := i18n.New(cfg.LangDir, cfg.DefaultLanguage)
	if cfg.LangDir == "" && cfg.Static != nil {
		// The language files in the binary, read only
		if bundle.FS, err = fs.Sub(cfg.Static, "lang"); err != nil {
			return nil, fmt.Errorf("the language files in the binary: %w", err)
		}
		bundle.Dir = filepath.Join("(built in)", "lang")
	}
	bundle.Logf = func(format string, args ...any) { logging.Logf("console", format, args...) }
	bundle.Load()
	languages := &i18n.Handlers{
		Bundle:       bundle,
		Production:   cfg.Production,
		SecureCookie: cfg.Production || cfg.HTTPSExact,
		PreferredLanguage: func(r *http.Request) string {
			if user := kit.User(r); user != nil {
				return user.PreferredLanguage
			}
			return ""
		},
		Logf: func(format string, args ...any) { logging.Logf("console", "ERROR: "+format, args...) },
	}

	api := goapi.New(opts.API, goapi.WithHost(cfg.APIHost), goapi.WithEventBus(opts.Events))

	s.deps = &kit.Deps{
		Router:   s.router,
		Early:    s.early,
		Config:   cfg,
		Store:    opts.Store,
		Sessions: sessions,
		CSRF:     protector,
		Access:   access.New(opts.Store, access.NewDBRoleSource(opts.Roles), access.Options{DeviceScopeDefault: cfg.DeviceScopeDefault}),
		Limits: limits.NewSet(limits.Options{
			ClientIP: cfg.TrustProxy.ClientIP,
			UserKey:  userKey,
			Getenv:   getenv,
		}),
		I18n:  languages,
		WebUI: host,
		GoAPI: api,
		Audit: audit,
		Log:   kit.NewLogger(cfg.LogLevel, cfg.Production),
		Jobs:  s.jobs,
	}
	s.deps.ServerInfo = serverinfo.New(serverinfo.Options{
		PubKeyPath:  cfg.PubKeyPath,
		APIKeyPath:  cfg.APIKeyPath,
		DBPath:      cfg.DBPath,
		APIURL:      cfg.APIURL,
		GoAPIPort:   cfg.GoAPIPort,
		APIPort:     cfg.APIPort,
		DataDir:     cfg.DataDir,
		EnvPath:     cfg.EnvPath,
		IsDocker:    cfg.Docker,
		Getenv:      getenv,
		ComposePath: cfg.ComposePath,
		GoAPI:       api,
		QR: func(text string) (string, error) {
			return qr.PNGDataURL(text, qr.Options{Width: 256, Margin: 2})
		},
		Logger: s.deps.Log,
	})
	s.deps.Avatars = avatars.New(avatars.Options{DB: opts.Store, Logger: s.deps.Log.Child("Avatar")})
	// The fonts an install of the Node console has go to the new folder
	// once, before the branding lists them
	if err := copyOldFonts(cfg.OldFontsDir, cfg.FontsDir); err != nil {
		logging.Logf("console", "WARN: installed fonts: %v", err)
	}
	// The branding's cache is the one copy of the settings: every write
	// goes through this service. Font downloads go through its outbound
	// guard.
	s.deps.Branding, err = branding.New(branding.Options{
		DB: opts.Store, UploadsDir: cfg.UploadsDir, FontsDir: cfg.FontsDir, FontCacheFile: cfg.FontCacheFile,
		TempDir: cfg.TempDir, ThemesDir: cfg.ThemesDir, PalettesFile: cfg.PalettesFile, FaviconFile: cfg.FaviconFile,
		SupportersFile: cfg.SupportersFile, Files: cfg.Static,
	})
	if err != nil {
		return nil, fmt.Errorf("branding: %w", err)
	}
	s.deps.GuestAccess = guestaccess.New(guestaccess.Options{HTTPS: cfg.HTTPS, GoAPI: api})
	s.deps.FileTransfer = filetransfer.New(filetransfer.Options{Dir: cfg.TransfersDir, Logger: s.deps.Log})
	s.jobs.Add("file transfer cleanup", s.deps.FileTransfer.Run)
	// Under a supervisor a restart is the process's own exit (A30);
	// otherwise the restart runs through the service manager as it did,
	// and nothing ends the process.
	restartServer := s.deps.ServerInfo.RestartServer
	var exit func()
	if supervisor := updater.Supervisor(getenv); supervisor != "" && opts.Exit != nil {
		restartServer, exit = restart.SupervisorRestart(supervisor), opts.Exit
	}
	s.deps.Restart = restart.New(restart.Options{
		DataDir:       cfg.DataDir,
		GoAPI:         api,
		RestartServer: restartServer,
		Rollbacks:     map[string]restart.Rollback{"connection-mode": restart.ConnectionModeRollback(s.deps.ServerInfo)},
		Exit:          exit,
		Logger:        s.deps.Log,
	})
	s.deps.Generator = generator.New(generator.Options{
		DB: opts.Store, ServerInfo: s.deps.ServerInfo, DataDir: cfg.DataDir, ConsoleDir: cfg.ConsoleDir,
		ArtifactDir: cfg.AgentArtifactDir, Embedded: cfg.GeneratorEmbedded, WorkerEnabled: cfg.AgentBuildWorker,
		PollInterval: cfg.AgentBuildPoll, Cooldown: cfg.AgentBuildCooldown, Logger: s.deps.Log,
	})
	s.deps.Updater = updater.New(updater.Options{
		DataDir:    cfg.DataDir,
		ConsoleDir: cfg.ConsoleDir,
		EnvFile:    getenv("BETTERDESK_ENV_FILE"),
		Version:    opts.Version,
		Commit:     opts.Commit,
		Docker:     cfg.Docker,
		Getenv:     getenv,
		DataBackup: opts.DataBackup,
		Setting:    readSetting(opts.Store),
		Logger:     s.deps.Log.Child("UPDATE"),
	})
	s.deps.DeviceGroups = devicegroups.New(devicegroups.Options{DB: opts.Store, Access: s.deps.Access})
	s.deps.Backend = goapi.NewBackend(api, devicegroups.DeviceStore{Groups: s.deps.DeviceGroups})
	s.deps.Mail = mail.New(mail.Options{
		DB:          opts.Store,
		Transport:   &mail.SMTP{},
		Getenv:      getenv,
		TLSVerify:   cfg.SMTPTLSVerify,
		RequireTLS:  cfg.SMTPRequireTLS,
		Port:        cfg.Port,
		HTTPSPort:   cfg.HTTPSPort,
		PanelHost:   s.deps.ServerInfo.ReadPanelPublicHostValue,
		ServesHTTPS: func() bool { return s.net.cert != nil },
		Logger:      s.deps.Log.Child("Email"),
	})
	// Alert mails and evaluations beside a request end with the console:
	// the job below cancels them and waits for them when the jobs stop.
	alertWork, stopAlertWork := context.WithCancel(context.Background())
	s.deps.Alerts = alerts.New(alerts.Options{
		DB:   opts.Store,
		Mail: s.deps.Mail,
		Devices: func(ctx context.Context) ([]*goapi.Object, error) {
			return append(api.GetAllPeers(ctx, false), api.GetAllRemoteTargets(ctx, "", false)...), nil
		},
		Logger:  s.deps.Log.Child("AlertEngine"),
		Context: alertWork,
	})
	s.jobs.Add("alert engine", func(ctx context.Context) {
		<-ctx.Done()
		stopAlertWork()
		s.deps.Alerts.Wait()
	})
	// While a Node console is behind this one it runs the offline sweep
	// itself, so this one must not: every alert would be raised and
	// mailed twice.
	if cfg.NodeUpstream == nil {
		s.jobs.Add("alert sweep", s.deps.Alerts.Run)
	}
	// The same holds for the mail of a help request.
	if cfg.NodeUpstream == nil {
		s.jobs.Add("help request mail", helpmail.New(helpmail.Options{
			DB: opts.Store, Mail: s.deps.Mail, Events: api.Events, Logger: s.deps.Log.Child("HelpRequestEmail"),
		}).Run)
	}
	badParam := func(w http.ResponseWriter, r *http.Request, err error) {
		host.ServerError(w, r, err, http.StatusBadRequest)
	}
	s.early.OnBadParam(badParam)
	s.router.OnBadParam(badParam)

	s.deps.Authn, err = authn.New(authn.Options{
		DB:       opts.Store,
		GoAPI:    api,
		Settings: authn.SettingsFromEnv(getenv, cfg.DataDir, cfg.KeysPath),
		QR: func(text string) (string, error) {
			return qr.PNGDataURL(text, qr.Options{Width: 256, Margin: 2})
		},
		Log: s.deps.Log.Child("AUTH"),
	})
	if err != nil {
		return nil, fmt.Errorf("sign-in service: %w", err)
	}

	s.jobs.Add("session cleanup", sessions.RunCleanup)
	s.deps.Sockets = ws.New(ws.Options{
		Session:    ws.SessionOf(sessions),
		Permission: ws.PermissionOf(s.deps.Access),
		Origins: ws.OriginPolicy{
			Allowed:      ws.ParseAllowedOrigins(getenv("WS_ALLOWED_ORIGINS")),
			AllowMissing: getenv("BD_ALLOW_MISSING_ORIGIN") == "true",
		},
		ClientIP: cfg.TrustProxy.ClientIP,
	})
	s.deps.StatusPush = statuspush.New(statuspush.Options{Events: api.Events, Cascade: s.deps.DeviceGroups.CascadePeerIDChange})
	s.jobs.Add("device status push", s.deps.StatusPush.Run)
	s.deps.Relay = bdrelay.New(bdrelay.Options{VerifyDevice: s.deps.Authn.VerifyDeviceWSAuth})
	s.jobs.Add("bd-relay session cleanup", s.deps.Relay.Run)
	s.deps.RemoteRelay = remoterelay.New(remoterelay.Options{
		VerifyDevice: s.deps.Authn.VerifyDeviceWSAuth,
		DB:           opts.Store,
		CanAccess:    s.deps.DeviceGroups.UserCanAccessDevice,
	})
	// Server management and its terminal. Relative paths and the console's
	// folder are CONSOLE_DIR, the old console's working directory. A
	// restart of this process's own service is its exit under a supervisor
	// (A30, spec updater-installers 4.6).
	s.deps.ServerMgmt = servermgmt.New(servermgmt.Options{
		Getenv: getenv, WorkDir: cfg.ConsoleDir,
		RestartSelf: func() bool {
			return s.deps.Restart.ExitLater(2*time.Second, "[SERVER-MGMT] Restarting after a restart of the server from server management...")
		},
		Terminal: servermgmt.TerminalOptions{
			Audit: func(ctx context.Context, userID int64, action, details, ip string) {
				if err := audit.Write(ctx, userID, action, details, ip); err != nil {
					logging.Logf("console", "WARN: [srv-term] audit log failed: %v", err)
				}
			},
			ClientIP: cfg.TrustProxy.ClientIP,
		},
	})
	// The rest of the old console's start sequence: the hourly clean-up,
	// the stale device sweep, and, unless a Node console behind this one
	// binds their ports itself, LAN discovery and the mDNS announcement.
	s.authImport = authimport.FromEnv(getenv, cfg.DataDir)
	sweeps := housekeeping.New(housekeeping.Options{DB: opts.Store, StalePeers: s.deps.DeviceGroups.CleanupStaleOnlinePeers,
		StatusSyncInterval: getenv("STATUS_SYNC_INTERVAL"), HeartbeatStaleThreshold: getenv("HEARTBEAT_STALE_THRESHOLD")})
	s.jobs.Add("integration housekeeping", sweeps.RunIntegration)
	s.jobs.Add("online status sync", sweeps.RunStatusSync)
	if cfg.NodeUpstream == nil {
		// The jobs start after the listeners and announce the port and
		// scheme of the one that serves the console (listeners.go)
		lan := discovery.FromEnv(getenv, discovery.Settings{PubKeyPath: cfg.PubKeyPath, APIPort: float64(cfg.APIPort), Version: cfg.Version})
		s.jobs.Add("LAN discovery", func(ctx context.Context) { discovery.NewLAN(s.announced(lan)).Run(ctx) })
		// The old console announced itself over mDNS only where someone had
		// installed bonjour-service by hand; no installer or image did. So
		// the announcement runs only when PANEL_MDNS asks for it.
		if mdns := getenv("PANEL_MDNS"); mdns != "" && mdns != "off" && mdns != "0" {
			s.jobs.Add("mDNS announcement", func(ctx context.Context) { discovery.NewMDNS(s.announced(lan)).Run(ctx) })
		}
	}
	registerRoutes(s.deps)
	registerSockets(s.deps)
	s.newClientAPI()
	s.pipeline = s.buildPipeline()
	return s, nil
}

// userKey is the id of the signed-in account as the rate limiters key it.
func userKey(r *http.Request) string {
	if id := kit.UserID(r); id != 0 {
		return fmt.Sprint(id)
	}
	return ""
}

// Router is the table of the console's routes (step 18 of the pipeline).
func (s *Server) Router() *kit.Router { return s.router }

// Early is the table of the client and device API routes, which sit
// before the request language and the CSRF gate (steps 12 and 13).
func (s *Server) Early() *kit.Router { return s.early }

// Deps is what the route groups were registered with.
func (s *Server) Deps() *kit.Deps { return s.deps }

// Jobs is the registry of the console's background work.
func (s *Server) Jobs() *jobs.Registry { return s.jobs }

// ServeHTTP sends a request through the pipeline. An upgrade to a socket
// the Go console has (console/ws) goes to that socket, outside the
// pipeline, as Node's upgrade listener ran outside Express. With the Node
// console behind this one, every other upgrade goes there, and so does a
// request no Go route has the method and path for.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.routing != nil && r.Method == http.MethodGet && r.URL.Path == proxy.RoutingPath {
		s.routing.ServeHTTP(w, r)
		return
	}
	if s.deps.Sockets.Serves(r) {
		s.deps.Sockets.ServeHTTP(w, r)
		return
	}
	if s.node != nil && (ws.IsUpgrade(r) || !s.handles(r)) {
		s.node.ServeHTTP(w, r)
		return
	}
	s.pipeline.ServeHTTP(w, r)
}

// handles reports whether Go answers r: a Go socket for an upgrade, a Go
// route in either stage for anything else. The routing report asks the
// same question.
func (s *Server) handles(r *http.Request) bool {
	if s.routing != nil && r.Method == http.MethodGet && r.URL.Path == proxy.RoutingPath {
		return true
	}
	if ws.IsUpgrade(r) {
		return s.deps.Sockets.Serves(r)
	}
	return s.early.Match(r) || s.router.Match(r)
}

// Start opens the listeners (listeners.go), starts the background jobs and
// serves until ctx ends or Stop is called.
//
// Before it listens it imports the old console's auth.db when there is
// one that was never imported (package authimport; not while the Node
// console is behind this one, which still reads that file), and loads the
// branding settings into their cache. It
// also looks after the first account (see
// authn.Service.EnsureDefaultAdmin), unless the Node console is behind
// this one: that console does the same at its own start, and the file by
// which an installer asks for a new admin password works once, for
// whichever of the two looks first. The Node console is the one that is
// sure to know the password the installer set (web-nodejs/.env), so it is
// left to it.
func (s *Server) Start(ctx context.Context) error {
	if s.cfg.NodeUpstream == nil {
		if _, err := authimport.Run(ctx, s.deps.Store, s.authImport); err != nil {
			return fmt.Errorf("auth.db import: %w", err)
		}
	}
	s.deps.Branding.LoadBranding(ctx)
	if s.cfg.NodeUpstream == nil {
		if _, err := s.deps.Authn.EnsureDefaultAdmin(ctx); err != nil {
			return fmt.Errorf("first account: %w", err)
		}
	}
	err := s.openListeners(ctx)
	if err == nil {
		err = s.startClientAPI(ctx)
	}
	if err != nil {
		s.closeListeners()
		s.net.addr = ""
		if s.cfg.PortUnset && listen.InUse(err) {
			return fmt.Errorf("%w: %w", ErrDefaultPortTaken, err)
		}
		return err
	}
	s.jobs.Start(ctx)
	target := "Go only"
	if s.cfg.NodeUpstream != nil {
		target = "unported routes go to " + s.cfg.NodeUpstream.Host
	}
	logging.Logf("console", "listening on %s (%s), %d Go routes", s.net.addr, target, len(s.early.Routes())+len(s.router.Routes()))
	return nil
}

// ErrDefaultPortTaken is the error of Start when CONSOLE_PORT is not set
// and a port of the console is taken: the server then runs without its
// console (DECISIONS A62), as an install whose older console still holds
// the port needs.
var ErrDefaultPortTaken = errors.New("a port of the console is taken and CONSOLE_PORT is not set")

// Addr is the address the server listens on, once started.
func (s *Server) Addr() string {
	return s.net.addr
}

// Stop closes the listeners, gives open requests ten seconds to finish,
// ends the open websockets and the background jobs and waits for the
// profile picture syncs that sign-ins started. When it returns, nothing
// the server started is still running.
func (s *Server) Stop() {
	s.closeListeners()
	s.deps.Sockets.Close()
	s.jobs.Stop()
	s.deps.Avatars.Wait()
	if s.node != nil {
		s.node.Close()
	}
}

// readSetting reads one row of the console's settings table, for the
// updater's backup retention (db.getSetting). A NULL value counts as no
// row.
func readSetting(db *store.DB) func(ctx context.Context, key string) (string, bool, error) {
	return func(ctx context.Context, key string) (string, bool, error) {
		if db == nil {
			return "", false, nil
		}
		rows, err := db.Query(ctx, "SELECT value FROM settings WHERE key = ?", key)
		if err != nil {
			return "", false, fmt.Errorf("read setting %s: %w", key, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return "", false, rows.Err()
		}
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return "", false, fmt.Errorf("read setting %s: %w", key, err)
		}
		return value.String, value.Valid, rows.Err()
	}
}
