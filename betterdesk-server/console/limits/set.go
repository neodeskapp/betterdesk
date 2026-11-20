package limits

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Options are what the limiters need from the rest of the console.
type Options struct {
	// ClientIP is the client's address: req.ip of the Node console, which
	// is httpx.TrustProxy.ClientIP with the console's TRUST_PROXY.
	ClientIP func(r *http.Request) string
	// UserKey is the id of the signed-in user as text (session.userId,
	// else session.user.id), "" when nobody is signed in.
	UserKey func(r *http.Request) string
	// Getenv reads a setting. nil means every limiter has its default.
	Getenv func(name string) string
	// Now is the clock. nil means time.Now.
	Now func() time.Time
}

// message is the body of a 429 answer of the console.
type message struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// pollPaths are read by the panel's widgets every few seconds. Their
// requests count against Widget and not against API.
var pollPaths = []string{
	"/api/stats",
	"/api/server/status",
	"/api/server/bandwidth",
	"/api/devices",
	"/api/audit/conn",
	"/api/dashboard/client-config",
	"/api/dashboard/activity",
	"/api/registrations/count",
	"/api/network/targets",
	"/api/tickets/stats",
	"/api/cdap/devices",
	"/api/users",
	"/api/audit-log",
	"/api/system/info",
	"/api/docker/containers",
	"/api/settings/restart-status",
	"/api/folders",
	"/api/tags",
	"/api/device-groups",
	"/api/bd/notifications",
}

// pollPrefixes are read-only groups of routes that count like poll paths.
var pollPrefixes = []string{"/api/dashboard/", "/api/panel/"}

// readPaths are the settings reads a settings page loads together. Their
// requests count against PanelRead and not against API.
var readPaths = []string{
	"/api/settings/audit",
	"/api/settings/info",
	"/api/settings/restart/pending",
	"/api/settings/device-scope",
	"/api/settings/connection-mode",
	"/api/settings/public-endpoints",
	"/api/settings/branding",
	"/api/settings/appearance",
	"/api/settings/branding/backgrounds",
	"/api/settings/branding/export",
	"/api/settings/branding/profiles",
	"/api/settings/themes",
	"/api/settings/fonts",
	"/api/settings/fonts/local",
	"/api/settings/backup/stats",
	"/api/settings/updates/channel",
	"/api/settings/updates/last-result",
	"/api/settings/updates/server-binary/status",
	"/api/settings/backup/retention",
	"/api/settings/enrollment",
	"/api/settings/client-sessions",
	"/api/settings/ldap",
	"/api/settings/oidc",
	"/api/settings/email/smtp",
}

var avatarPaths = regexp.MustCompile(`^/api/users/(avatars|[0-9]+/avatar)$`)

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func isRead(r *http.Request) bool {
	method := strings.ToUpper(r.Method)
	return method == http.MethodGet || method == http.MethodHead
}

// IsPanelPoll reports a GET or HEAD of a poll path or below a poll
// prefix. apiPath is the path in its /api/... form.
func IsPanelPoll(r *http.Request, apiPath string) bool {
	if !isRead(r) {
		return false
	}
	if contains(pollPaths, apiPath) {
		return true
	}
	for _, prefix := range pollPrefixes {
		if strings.HasPrefix(apiPath, prefix) {
			return true
		}
	}
	return false
}

// IsPanelRead reports a GET or HEAD of a settings read path or of a
// profile picture.
func IsPanelRead(r *http.Request, apiPath string) bool {
	return isRead(r) && (contains(readPaths, apiPath) || avatarPaths.MatchString(apiPath))
}

// IsPanelPreferenceWrite reports the save of the desktop layout.
func IsPanelPreferenceWrite(r *http.Request, apiPath string) bool {
	return strings.ToUpper(r.Method) == http.MethodPost && apiPath == "/api/desktop/layout"
}

// Set holds the console's limiters. limits.md lists each with its window,
// maximum, key and where it applies.
type Set struct {
	// API is the general limit of the API per address.
	API *Limiter
	// SessionRead is the budget of one signed-in person's reads.
	SessionRead *Limiter
	// Widget is the limit of the panel's poll paths per address.
	Widget *Limiter
	// PanelRead is the limit of the settings read paths.
	PanelRead *Limiter
	// PanelPreference is the limit of desktop layout saves.
	PanelPreference *Limiter
	// RDClientPage is the limit of the remote viewer's pages.
	RDClientPage *Limiter
	// Login is the limit of sign-in attempts.
	Login *Limiter
	// TOTPVerify is the limit of second-factor codes per address, apart
	// from the password step's.
	TOTPVerify *Limiter
	// PasswordChange is the limit of password changes.
	PasswordChange *Limiter
	// Upload is the limit of uploads and ticket writes.
	Upload *Limiter
	// FileAccess is the limit of file downloads.
	FileAccess *Limiter
	// Register is the limit of device registrations (POST /api/bd/register).
	Register *Limiter

	opts Options
}

// NewSet builds the limiters from the environment:
//
//	RATE_LIMIT_WINDOW_MS             60000  window of API, SessionRead, PanelRead, Login, TOTPVerify
//	RATE_LIMIT_MAX                   100    API
//	LOGIN_RATE_LIMIT_MAX             5      Login and TOTPVerify
//	PANEL_READ_RATE_LIMIT_MAX        600    SessionRead and PanelRead
//	WIDGET_RATE_LIMIT_MAX            600    Widget
//	PANEL_PREFERENCE_RATE_LIMIT_MAX  30     PanelPreference
//	RDCLIENT_PAGE_RATE_LIMIT_MAX     120    RDClientPage
//	UPLOAD_RATE_LIMIT_MAX            30     Upload
//	FILE_ACCESS_RATE_LIMIT_MAX       120    FileAccess
//	BD_REGISTER_RATE_LIMIT_MAX       20     Register
//
// A value that is not a number, or zero, gives the default; text after
// the digits is ignored ("7.9" and "7abc" are 7).
func NewSet(opts Options) *Set {
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.UserKey == nil {
		opts.UserKey = func(*http.Request) string { return "" }
	}
	if opts.ClientIP == nil {
		opts.ClientIP = httpx.SocketIP
	}
	env := func(name string, fallback int64) int64 { return parseIntOr(opts.Getenv(name), fallback) }
	const minute = 60 * 1000
	window := env("RATE_LIMIT_WINDOW_MS", minute)
	panelReads := env("PANEL_READ_RATE_LIMIT_MAX", 600)

	s := &Set{opts: opts}
	signedInRead := func(r *http.Request) bool { return isRead(r) && opts.UserKey(r) != "" }
	limiter := func(name string, windowMs, maximum int64, text string, key func(*http.Request) string, skip func(*http.Request, string) bool) *Limiter {
		return New(Config{Name: name, WindowMs: windowMs, Max: maximum, Message: message{Error: text}, Key: key, Skip: skip, Now: opts.Now})
	}

	s.API = limiter("apiLimiter", window, env("RATE_LIMIT_MAX", 100), "Too many requests. Please try again later.", s.addressKey,
		func(r *http.Request, apiPath string) bool {
			return signedInRead(r) || IsPanelPoll(r, apiPath) || IsPanelRead(r, apiPath) || IsPanelPreferenceWrite(r, apiPath)
		})
	s.SessionRead = limiter("sessionReadLimiter", window, panelReads, "Too many requests. Please slow down.",
		func(r *http.Request) string { return "session-read:" + opts.UserKey(r) },
		func(r *http.Request, _ string) bool { return !signedInRead(r) })
	s.Widget = limiter("widgetLimiter", minute, env("WIDGET_RATE_LIMIT_MAX", 600), "Too many widget requests. Please slow down.", s.addressKey,
		func(r *http.Request, _ string) bool { return signedInRead(r) })
	s.PanelRead = limiter("panelReadLimiter", window, panelReads, "Too many panel read requests. Please slow down.",
		func(r *http.Request) string { return s.userOrAddressKey("panel-read:", r) },
		func(r *http.Request, _ string) bool { return !isRead(r) })
	s.PanelPreference = limiter("panelPreferenceLimiter", minute, env("PANEL_PREFERENCE_RATE_LIMIT_MAX", 30), "Too many layout save requests. Please slow down.",
		func(r *http.Request) string { return s.userOrAddressKey("pref:", r) }, nil)
	s.RDClientPage = limiter("rdClientPageLimiter", minute, env("RDCLIENT_PAGE_RATE_LIMIT_MAX", 120), "Too many remote page requests. Please slow down.", s.addressKey, nil)
	s.Login = limiter("loginLimiter", window, env("LOGIN_RATE_LIMIT_MAX", 5), "Too many login attempts. Please try again in a minute.", s.addressKey, nil)
	s.TOTPVerify = limiter("totpVerifyLimiter", window, env("LOGIN_RATE_LIMIT_MAX", 5), "Too many verification attempts. Please try again in a minute.", s.addressKey, nil)
	s.PasswordChange = limiter("passwordChangeLimiter", 5*minute, 3, "Too many password change attempts. Please try again later.", opts.ClientIP, nil)
	s.Upload = limiter("uploadLimiter", minute, env("UPLOAD_RATE_LIMIT_MAX", 30), "Too many upload requests. Please try again later.", s.addressKey, nil)
	s.FileAccess = limiter("fileAccessLimiter", minute, env("FILE_ACCESS_RATE_LIMIT_MAX", 120), "Too many file requests. Please try again later.", s.addressKey, nil)
	s.Register = limiter("registerLimiter", minute, env("BD_REGISTER_RATE_LIMIT_MAX", 20), "Too many registration requests. Please slow down.", s.addressKey, nil)
	return s
}

// addressKey is the client's address, else the X-Forwarded-For header as
// sent, else "unknown".
func (s *Set) addressKey(r *http.Request) string {
	if ip := s.opts.ClientIP(r); ip != "" {
		return ip
	}
	if forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ", "); forwarded != "" {
		return forwarded
	}
	return "unknown"
}

func (s *Set) userOrAddressKey(prefix string, r *http.Request) string {
	if user := s.opts.UserKey(r); user != "" {
		return prefix + user
	}
	return s.addressKey(r)
}

// All lists the limiters.
func (s *Set) All() []*Limiter {
	return []*Limiter{s.API, s.SessionRead, s.Widget, s.PanelRead, s.PanelPreference, s.RDClientPage, s.Login, s.TOTPVerify, s.PasswordChange, s.Upload, s.FileAccess, s.Register}
}

type mount struct {
	path    string
	limiter *Limiter
}

// AppLevel is the limiters every request passes before the routes, in
// this order:
//
//  1. SessionRead under /api/
//  2. Widget under each poll path
//  3. PanelRead under each settings read path
//  4. Widget under /api/panel and /api/dashboard
//  5. PanelPreference under /api/desktop/layout
//  6. API under /api/
//
// "Under" is the path itself and everything below it, in any letter case.
// The skips of the limiters read the path as the client sent it, so
// /api//devices is no poll path.
// Each limiter counts a request once, also when the request is under two
// of its mounts (a path below two read paths) or passes it again on its
// route (API on /api/logs/recent). The first limiter that refuses answers
// 429 and the later ones do not count. The client sees the RateLimit
// headers of the last limiter that counted.
func (s *Set) AppLevel() func(http.Handler) http.Handler {
	mounts := []mount{{"/api/", s.SessionRead}}
	for _, p := range pollPaths {
		mounts = append(mounts, mount{p, s.Widget})
	}
	for _, p := range readPaths {
		mounts = append(mounts, mount{p, s.PanelRead})
	}
	mounts = append(mounts, mount{"/api/panel", s.Widget}, mount{"/api/dashboard", s.Widget}, mount{"/api/desktop/layout", s.PanelPreference}, mount{"/api/", s.API})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := httpx.RawPath(r)
			if _, _, ok := httpx.StripMount(raw, "/api/"); !ok {
				next.ServeHTTP(w, r)
				return
			}
			r = countOnce(r)
			for _, m := range mounts {
				if _, _, ok := httpx.StripMount(raw, m.path); !ok {
					continue
				}
				if !m.limiter.allow(w, r, raw) {
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
