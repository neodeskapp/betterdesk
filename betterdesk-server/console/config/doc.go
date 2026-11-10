// Package config reads the console's settings from the environment, with
// the names, defaults and parsing the old console had in config/config.js.
//
// # Settings
//
//	Variable                 Default               Meaning
//	CONSOLE_PORT             (off)                 Port the Go console listens on. Unset or 0: the Go console does not start.
//	HOST                     0.0.0.0               Address the console listens on.
//	CONSOLE_NODE_UPSTREAM    (none)                http(s) address of the Node console while routes move. With it, Go
//	                                               answers only the routes it has and forwards everything else.
//	CONSOLE_DEBUG_ROUTING    (off)                 "1" adds GET /__bd/routing for the contract suite.
//	TRUST_PROXY              (off)                 Which proxies may speak for the client: false, true, a hop count,
//	                                               loopback, linklocal, uniquelocal, addresses or ranges.
//	NODE_ENV                 development           "production" hides error detail and reads the web app's files once.
//	DOCKER                   (off)                 "true", or a /.dockerenv file, means a container: default folders
//	                                               are /app and /app/data.
//	CONSOLE_DIR              (searched)            Folder of the old console's files (lang, public, protos, wallpapers).
//	                                               Searched in this order for lang/en.json: /app in a container,
//	                                               ../web-nodejs next to the server's binary, ../web-nodejs next to the
//	                                               working directory, /opt/BetterDeskConsole, /opt/betterdesk,
//	                                               /var/lib/betterdesk (C:\BetterDeskConsole on Windows).
//	CONSOLE_LANG_DIR         CONSOLE_DIR/lang      Folder of the <code>.json language files.
//	DATA_DIR                 /app/data in a        Where the console writes: .session_secret, uploads.
//	                         container, else
//	                         CONSOLE_DIR/data
//	WEB_UI_DIR               web-ui/build beside   Build of the web app. Without it: CONSOLE_DIR/../web-ui/build, or
//	                         CONSOLE_DIR           CONSOLE_DIR/web-ui/build when only that one has an index.html.
//	HTTPS_ENABLED            false                 "true" in any letter case: cookies are called betterdesk.sid and __csrf
//	                                               and are Secure, the security headers are those of HTTPS.
//	HSTS_ENABLED             (auto)                true, 1, on: always send Strict-Transport-Security in HTTPS mode.
//	                                               false, 0, off: never. Otherwise only without self-signed certificates.
//	ALLOW_SELF_SIGNED_CERTS  false                 "true" turns the automatic HSTS off.
//	DEFAULT_LANGUAGE         en                    Language of a request that names none.
//	DEVICE_SCOPE_DEFAULT     open                  "restricted" hides devices nobody was given from non-admin roles.
//	LOG_LEVEL                warn in production,   error, warn, info or debug.
//	                         else info
//	BETTERDESK_API_URL       http://127.0.0.1:21114/api  Its host and port are the Host of calls into the API server.
//	(or HBBS_API_URL)                              Without it the server's own API port is used.
//	                                               Its host is also the host clients are given when nothing else
//	                                               names one, and its scheme decides between http and https there.
//	GO_API_PORT              port of the API       Port of the server's API in the API address given to clients. Without
//	                         address, else 21114   a port in the address: 443 for https, 80 for any other scheme.
//	API_PORT                 21121                 The client API port.
//	API_ENABLED              true                  "true" in any letter case: the client API port is reported as the
//	                                               port clients sign in on. Empty counts as not set.
//	KEYS_PATH                (searched)            Folder the server writes its keys to. Else RUSTDESK_DIR, else
//	(or RUSTDESK_DIR,                              RUSTDESK_PATH, else /opt/rustdesk in a container, else the first of
//	RUSTDESK_PATH)                                 /opt/betterdesk and /opt/rustdesk that has id_ed25519 (C:\BetterDesk
//	                                               and C:\RustDesk on Windows), else the first of the two.
//	PUB_KEY_PATH             KEYS_PATH/id_ed25519.pub  The server's public key file.
//	API_KEY_PATH             KEYS_PATH/.api_key    The API key file.
//	DB_PATH                  KEYS_PATH/db_v2.sqlite3  The database file as the old console named it. Only reported.
//	WS_HBBS_HOST             localhost             Where the console reaches the signal server. The server status
//	WS_HBBS_PORT             21116                 reports the port and the ports next to it (NAT test, websockets).
//	WS_HBBR_HOST             localhost             Where the console reaches the relay.
//	WS_HBBR_PORT             21117
//
// The numbers are read as the old console read them: digits up to the
// first other character, and the default for zero or no number. Two files
// have fixed places: the old console's own .env in CONSOLE_DIR, and
// docker-compose.yml in the folder above it. So has the folder for the
// files of file transfers: CONSOLE_DIR/data/transfers, which is not below
// DATA_DIR unless DATA_DIR is CONSOLE_DIR/data (the default outside a
// container, and /app/data in the container images).
//
// The branding's files have fixed places in CONSOLE_DIR as well, whatever
// DATA_DIR says (only its uploaded pictures go to DATA_DIR/uploads):
//
//	CONSOLE_DIR/public/fonts                   installed fonts, served as /fonts/
//	CONSOLE_DIR/data/google-fonts-cache.json   optional list of more font families for the search
//	CONSOLE_DIR/data/tmp                       an uploaded font before it is installed
//	CONSOLE_DIR/themes                         the built-in themes (*.json)
//	CONSOLE_DIR/config/theme-palettes.json     the built-in palettes; the console does not start without it
//	CONSOLE_DIR/public/favicon.svg             the default favicon
//	CONSOLE_DIR/config/supporters.json         the supporters list of the Help panel
//
// Read by the packages that own them, through Config.Getenv:
//
//	SESSION_SECRET           file or generated     Signs the session cookie and the CSRF token. Without it the secret
//	                                               is read from DATA_DIR/.session_secret or made and saved there
//	                                               (session.LoadSecret).
//	SESSION_IDLE_TIMEOUT     30 minutes            session.PolicyFromEnv, in milliseconds.
//	SESSION_MAX_AGE          24 hours
//	SESSION_IDLE_WARN_MS     1 minute
//	SESSION_MAX_PER_USER     1
//	RATE_LIMIT_WINDOW_MS     60000                 limits.NewSet lists the rate limit settings:
//	RATE_LIMIT_MAX           100                   LOGIN_RATE_LIMIT_MAX, PANEL_READ_RATE_LIMIT_MAX,
//	...                                            WIDGET_RATE_LIMIT_MAX, PANEL_PREFERENCE_RATE_LIMIT_MAX,
//	                                               RDCLIENT_PAGE_RATE_LIMIT_MAX, UPLOAD_RATE_LIMIT_MAX,
//	                                               FILE_ACCESS_RATE_LIMIT_MAX.
//	DEFAULT_ADMIN_USERNAME   admin                 authn.SettingsFromEnv lists the sign-in settings: the first account
//	DEFAULT_ADMIN_PASSWORD   file or generated     (also FORCE_PASSWORD_UPDATE, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS,
//	...                                            AUTH_DB_PATH, DB_TYPE, DATABASE_URL, SQLITE_AUTH_DB_MODE), the
//	                                               lockouts (API_MAX_FAILED_ATTEMPTS, API_LOCKOUT_MINUTES,
//	                                               API_IP_RATE_LIMIT, API_ATTEMPT_WINDOW), API_TOKEN_EXPIRY_DAYS and
//	                                               BETTERDESK_AUTH_AUTOCREATE. It gets DATA_DIR and the keys folder
//	                                               from this package.
//
// # Not read yet
//
// The old console loaded web-nodejs/.env before anything else, and the
// installers write the console's settings there. The Go console reads the
// server's environment only, so an install has to hand these settings to
// the server's service until the installers are changed. The certificate
// paths (SSL_CERT_PATH, SSL_KEY_PATH, HTTPS_PORT, HTTP_REDIRECT_HTTPS) are
// not read either: the Go console listens on plain HTTP on one port.
package config
