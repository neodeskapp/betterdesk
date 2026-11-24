// Package config reads the console's settings from the environment, with
// the names, defaults and parsing the old console had in config/config.js.
//
// # Settings
//
//	Variable                 Default               Meaning
//	CONSOLE_PORT             5000                  Port the Go console listens on. 0: the Go console does not start.
//	HOST                     127.0.0.1             Address the console listens on.
//	CONSOLE_DEBUG_ROUTING    (off)                 On adds GET /__bd/routing for the contract suite.
//	TRUST_PROXY              (off)                 Which proxies may speak for the client: off, on (one hop), a hop count,
//	                                               loopback, linklocal, uniquelocal, addresses or ranges.
//	NODE_ENV                 development           "production" hides error detail and reads the web app's files once.
//	DOCKER                   (off)                 On, or a /.dockerenv file, means a container: default folders
//	                                               are /app and /app/data.
//	CONSOLE_DIR              (searched)            Folder that holds the console's state: its .env and data/. Searched
//	                                               in this order for a .env, or the lang/en.json an install of the Node
//	                                               console left: /app in a container, /opt/NeoDeskConsole,
//	                                               /opt/neodesk, /var/lib/neodesk (C:\NeoDeskConsole on
//	                                               Windows); the first of them when none has either.
//	CONSOLE_STATIC_DIR       (the files in the     A folder on disk with the console's own files in the shape of
//	                         binary)               console/static/files (lang, public, protos, themes, config), read
//	                                               in place of the files in the binary, for development.
//	CONSOLE_LANG_DIR         (lang of the above)   Folder of the <code>.json language files on disk.
//	DATA_DIR                 /app/data in a        Where the console writes: .session_secret, uploads, fonts.
//	                         container, else
//	                         CONSOLE_DIR/data
//	WEB_UI_DIR               (the build in the     A build of the web app on disk, for development. Without it the
//	                         binary)               console serves the build in the binary (cmd/embed-webui); a binary
//	                                               built without that step has no web app.
//	HTTPS_ENABLED            off                   On: cookies are called neodesk.sid and __csrf
//	                                               and are Secure, the security headers are those of HTTPS. With a
//	                                               certificate the console listens with TLS on HTTPS_PORT; without one
//	                                               it stays on plain HTTP and keeps all of that (package listen).
//	HTTPS_PORT               5443                  Port of the TLS listener in HTTPS mode. Below 1024 without the right
//	                                               to bind it: 5443 (CONSOLE_PORT likewise becomes 5000).
//	HTTP_REDIRECT_HTTPS      on                    On: in HTTPS mode CONSOLE_PORT answers every
//	                                               request with 307 to the same target on HTTPS_PORT.
//	SSL_CERT_PATH            (none)                Certificate (PEM) of the console and the client API port.
//	SSL_KEY_PATH             (none)                Its private key. Both or neither.
//	SSL_CA_PATH              (none)                Optional bundle of the certificates that issued it, for the chain.
//	HSTS_ENABLED             (auto)                On: always send Strict-Transport-Security in HTTPS mode.
//	                                               Off: never. Otherwise only without self-signed certificates.
//	ALLOW_SELF_SIGNED_CERTS  off                   On turns the automatic HSTS off. It does not touch mail.
//	SMTP_TLS_VERIFY          on                    Off: the mail server's certificate is not checked
//	                                               when the saved SMTP settings have no tlsVerify of their own.
//	SMTP_REQUIRE_TLS         on                    Off: the SMTP login may go out without TLS, for
//	                                               a mail server on a trusted network that offers no STARTTLS.
//	HTTP_REDIRECT_HTTPS      on                    On: the link to the console in a mail
//	                                               starts with https, else with http. Nothing is redirected by it.
//	DEFAULT_LANGUAGE         en                    Language of a request that names none.
//	DEVICE_SCOPE_DEFAULT     open                  "restricted" hides devices nobody was given from non-admin roles.
//	LOG_LEVEL                warn in production,   error, warn, info or debug.
//	                         else info
//	NEODESK_API_URL       http://127.0.0.1:21114/api  Its host and port are the Host of calls into the API server.
//	(or HBBS_API_URL)                              Without it the server's own API port is used.
//	                                               Its host is also the host clients are given when nothing else
//	                                               names one, and its scheme decides between http and https there.
//	GO_API_PORT              port of the API       Port of the server's API in the API address given to clients. Without
//	                         address, else 21114   a port in the address: 443 for https, 80 for any other scheme.
//	API_PORT                 21121                 The client API port.
//	API_ENABLED              on                    On: the console listens on the client API port
//	                                               and reports it as the port clients sign in on. Set to the empty text
//	                                               it is off (LoadEnv; Load counts empty as unset).
//	API_HOST                 0.0.0.0               Address of the client API port.
//	RUSTDESK_API_PROXY       on                    On: the client API port passes the client API
//	                                               on to the server's API; otherwise it answers it from the console's
//	                                               routes. Set to the empty text it is off, as API_ENABLED.
//	RUSTDESK_API_TLS         auto                  Off or http: the client API port stays on plain HTTP. Any other
//	                                               value: TLS when the certificate loads, whatever HTTPS_ENABLED says.
//	KEYS_PATH                (searched)            Folder the server writes its keys to. Else RUSTDESK_DIR, else
//	(or RUSTDESK_DIR,                              RUSTDESK_PATH, else /opt/rustdesk in a container, else the first of
//	RUSTDESK_PATH)                                 /opt/neodesk and /opt/rustdesk that has id_ed25519 (C:\NeoDesk
//	                                               and C:\RustDesk on Windows), else the first of the two.
//	PUB_KEY_PATH             KEYS_PATH/id_ed25519.pub  The server's public key file.
//	API_KEY_PATH             KEYS_PATH/.api_key    The API key file.
//	DB_PATH                  KEYS_PATH/db_v2.sqlite3  The database file as the old console named it. Only reported.
//	WS_HBBS_HOST             localhost             Where the console reaches the signal server. The server status
//	WS_HBBS_PORT             21116                 reports the port and the ports next to it (NAT test, websockets).
//	WS_HBBR_HOST             localhost             Where the console reaches the relay.
//	WS_HBBR_PORT             21117
//	NEODESK_CLIENT_GENERATOR_MODE  download     "embedded" builds the support client from source on this machine
//	                                               instead of using the downloaded templates.
//	AGENT_BUILD_WORKER       (on)                  Off keeps the support client build worker from starting.
//	AGENT_BUILD_POLL_MS      5000                  How often the build worker looks for a queued build. Zero, a
//	                                               negative number or no number: every millisecond.
//	AGENT_BUILD_COOLDOWN_MS  1000                  Pause after a build. Zero, negative or no number: none.
//	AGENT_ARTIFACT_DIR       DATA_DIR/agent-builds  Where the finished installers are kept.
//
// The support client generator reads its other settings from the
// environment when it needs them, with /etc/neodesk/build.env
// (NEODESK_BUILD_ENV_FILE) filling the names the environment lacks:
// NEODESK_CLIENT_REPO, NEODESK_CLIENT_RELEASE_TAG, NEODESK_CLIENT_REF,
// NEODESK_CLIENT_ALLOWED_REPOS, NEODESK_GITHUB_TOKEN (or GITHUB_TOKEN),
// NEODESK_CUSTOM_CLIENT_SIGNING_SEED, NEODESK_CLIENT_BUILD_COMMAND,
// NEODESK_CLIENT_PROVIDER_TARGETS_JSON, NEODESK_CLIENT_PROVIDER_TARGETS_FILE,
// NEODESK_CLIENT_GITHUB_ACTIONS, NEODESK_CLIENT_BUILD_WORKFLOW,
// NEODESK_CLIENT_BUILD_TIMEOUT_MS and the toolchain paths of the local
// builder (package generator).
//
// On and off are 1/0, true/false, yes/no, y/n and on/off in any letter
// case, with space around them, as the server reads its own switches
// (envvar.Switch). Another value leaves a switch that is on by default
// on, except HTTP_REDIRECT_HTTPS, API_ENABLED and RUSTDESK_API_PROXY,
// which only an on value keeps on once they are set.
//
// The numbers are read as the old console read them: digits up to the
// first other character, and the default for zero or no number. Two files
// have fixed places: the old console's own .env in CONSOLE_DIR, and
// docker-compose.yml in the folder above it. So has the folder for the
// files of file transfers: CONSOLE_DIR/data/transfers, which is not below
// DATA_DIR unless DATA_DIR is CONSOLE_DIR/data (the default outside a
// container, and /app/data in the container images).
//
// The branding keeps its state in these places:
//
//	DATA_DIR/uploads                           uploaded pictures, served as /uploads/
//	DATA_DIR/fonts                             installed fonts, served as /fonts/. At the first start without
//	                                           it, the fonts of CONSOLE_DIR/public/fonts, where the Node console
//	                                           installed them, are copied into it.
//	CONSOLE_DIR/data/google-fonts-cache.json   optional list of more font families for the search
//	CONSOLE_DIR/data/tmp                       an uploaded font before it is installed
//
// The console's own files ship in the binary (package static), or come
// from CONSOLE_STATIC_DIR: the language files, public/ (favicon.svg and
// img/, served at the root), protos/ (served as /protos/), the built-in
// themes and config/theme-palettes.json.
// CONSOLE_DIR/wallpapers, served as /wallpapers/, holds no files that
// ship; it stays on disk.
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
//	                                               NEODESK_AUTH_AUTOCREATE. It gets DATA_DIR and the keys folder
//	                                               from this package.
//	SMTP_HOST                (none)                The mail server when no SMTP settings are saved (package mail),
//	SMTP_PORT                587                   read at every use: SMTP_PORT, SMTP_SECURE ("true"), SMTP_USER,
//	...                                            SMTP_PASS, SMTP_FROM (neodesk@localhost), SMTP_ALERT_EMAIL.
//	PANEL_PUBLIC_URL         (none)                The console's address in mails. Without both: http or https,
//	(or PUBLIC_URL)                                HOST (127.0.0.1 for 0.0.0.0) and the console's port.
//
// # The env file
//
// The old console loaded web-nodejs/.env before anything else. The server
// loads the file NEODESK_ENV_FILE names as the first step of main, with
// the same rule: a variable that is set to more than "" keeps its value
// (envfile.go). Values are read as dotenv files are read: 'single' and
// "double" quotes (with \n, \r, \t, \" and \\ in double quotes) and a
// " # comment" after a value (serverinfo.EnvValue). PORT is not read for
// the console: CONSOLE_PORT is, and the ID server reads SIGNAL_PORT.
package config
