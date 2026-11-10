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
