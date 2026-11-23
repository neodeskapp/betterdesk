package middleware

import (
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// SecurityOptions say in which mode the console runs.
type SecurityOptions struct {
	// HTTPS is the console's HTTPS mode (HTTPS_ENABLED=true).
	HTTPS bool
	// HSTS sends Strict-Transport-Security. It only has an effect in
	// HTTPS mode; HSTSEnabled gives the console's rule.
	HSTS bool
}

// HSTSEnabled is the console's rule for Strict-Transport-Security: never
// without HTTPS; with HSTS_ENABLED false, 0 or off never; with true, 1 or
// on always; otherwise only when certificates are not self-signed, so a
// LAN install that switches back to HTTP is not locked out for a year.
func HSTSEnabled(https bool, hstsEnv string, allowSelfSigned bool) bool {
	if !https {
		return false
	}
	switch strings.ToLower(hstsEnv) {
	case "false", "0", "off":
		return false
	case "true", "1", "on":
		return true
	}
	return !allowSelfSigned
}

const (
	policyStart = "default-src 'self';script-src 'self';script-src-attr 'none';style-src 'self' 'unsafe-inline';" +
		"font-src 'self';img-src 'self' data: blob:;media-src 'self' blob:;connect-src 'self' "
	policyEnd = ";frame-src 'none';object-src 'none';worker-src 'self' blob:;base-uri 'self';form-action 'self';frame-ancestors 'none'"

	permissionsPolicy = "geolocation=(), microphone=(self), camera=(), payment=(), usb=(), accelerometer=(), gyroscope=(), magnetometer=()"
)

// SecurityHeaders sets the headers every answer of the console carries: a
// strict Content-Security-Policy for what the server answers itself (the
// web app's pages replace it with their build's), the framing, sniffing
// and referrer rules, and Cache-Control: no-store, or one hour for the
// paths under /css/, /img/ and /fonts/. Later steps may replace the cache
// rule; files keep it.
func SecurityHeaders(opts SecurityOptions) func(http.Handler) http.Handler {
	policy := policyStart + "ws:" + policyEnd
	if opts.HTTPS {
		policy = policyStart + "wss:" + policyEnd + ";upgrade-insecure-requests"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", policy)
			if opts.HTTPS {
				h.Set("Cross-Origin-Opener-Policy", "same-origin")
				h.Set("Origin-Agent-Cluster", "?1")
				if opts.HSTS {
					h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
				}
			}
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-DNS-Prefetch-Control", "off")
			h.Set("X-Download-Options", "noopen")
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Permitted-Cross-Domain-Policies", "none")
			h.Set("X-XSS-Protection", "0")
			h.Set("Permissions-Policy", permissionsPolicy)
			h.Del("X-Powered-By")

			h.Set("Cache-Control", "no-store")
			p := httpx.RawPath(r)
			if strings.HasPrefix(p, "/css/") || strings.HasPrefix(p, "/img/") || strings.HasPrefix(p, "/fonts/") {
				h.Set("Cache-Control", "public, max-age=3600")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// desktopOrigins are the origins of the desktop client's webview:
// tauri://localhost on macOS and Linux, https://tauri.localhost on
// Windows. No other origin gets CORS.
var desktopOrigins = map[string]bool{
	"tauri://localhost":       true,
	"https://tauri.localhost": true,
}

// noContentTag is the entity tag of the text "No Content". The 204 to
// OPTIONS carries it, although it has no body.
const noContentTag = `W/"a-bAsFyilMr4Ra1hIU5PyoyFRunpI"`

// CORS lets the desktop client's webview call the API: for a request under
// /api/ whose Origin is one of desktopOrigins it adds the Access-Control
// headers. Every OPTIONS request under /api/ ends here with 204, whatever
// its origin.
//
// No origin gets Access-Control-Allow-Credentials, so no page of another
// origin reads an answer to a request that carried the console's cookies,
// or sends such a request when it needs a preflight. The webview calls
// the API without cookies (with a bearer token or nothing). There
// is no setting for a development origin either, since none is needed:
// the console serves the web app itself, and the web app's development
// server forwards the API to the console (web-ui/vite.config.ts), so the
// page and the API share an origin in both.
func CORS() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, _, ok := httpx.StripMount(httpx.RawPath(r), "/api/"); !ok {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			if origin := strings.Join(r.Header.Values("Origin"), ", "); desktopOrigins[origin] {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-CSRF-Token")
				h.Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				if h.Get("ETag") == "" {
					h.Set("ETag", noContentTag)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
