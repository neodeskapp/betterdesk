package impersonation

import (
	"context"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The answer to what an impersonation may not do: always 403 with this
// code.
const (
	CodeReadOnly = "impersonation_read_only"
	// HeaderViewing is the header the web app sends on every request while
	// it shows the console as another account. A request with it that is
	// no impersonation (the impersonation ended meanwhile) is answered with
	// 409 impersonation_ended and does not run as the admin.
	HeaderViewing = "X-ND-Impersonating"
)

// The audit actions.
const (
	ActionStarted  = "impersonation_started"
	ActionExtended = "impersonation_extended"
	ActionStopped  = "impersonation_stopped"
	ActionEnded    = "impersonation_ended"
	ActionRequest  = "impersonation_request"
)

// The routes an impersonation may always use, whatever their method: its
// own status, making it longer, stopping it. Signing out is handled apart
// (it signs the admin out).
var allowed = map[string]bool{
	"GET /api/impersonation":         true,
	"POST /api/impersonation/stop":   true,
	"POST /api/impersonation/extend": true,
}

// signOuts are the sign-out routes: during an impersonation they end it
// and sign the admin out.
var signOuts = map[string]bool{
	"POST /api/auth/logout": true,
	"POST /logout":          true,
}

// deniedPermissions are the permissions whose routes an impersonation may
// not use even to read: they start remote work on a device or show a
// secret (saved sign-in, connection passwords, keys). A route that names
// one of them, alone or as one of several, is refused.
var deniedPermissions = map[string]bool{
	auth.PermDeviceConnect:       true,
	auth.PermDeviceSigninUse:     true,
	auth.PermDeviceSigninManage:  true,
	auth.PermRemoteTargetConnect: true,
	auth.PermServerKeys:          true,
	auth.PermAgentTerminal:       true,
	auth.PermAgentFiles:          true,
	auth.PermCDAPCommand:         true,
	auth.PermCDAPTerminal:        true,
	auth.PermCDAPFiles:           true,
	auth.PermMeshTerminal:        true,
	auth.PermMeshFiles:           true,
	auth.PermMeshPower:           true,
}

// deniedRoutes are reads an impersonation may not make, by the route's
// pattern: the person's own credentials and devices, secrets the route
// shows without one of the permissions above, what asks a device for
// something, and the sign-in flows.
var deniedRoutes = map[string]string{
	// The person's own signed-in devices and sign-in history
	"/api/users/me/sessions":        "signed-in devices",
	"/api/auth/sessions":            "signed-in devices",
	"/api/users/me/security-events": "sign-in history",
	// The person's API tokens
	"/api/panel/api-tokens":             "API tokens",
	"/api/panel/api-tokens/current":     "API tokens",
	"/api/panel/api-tokens/permissions": "API tokens",
	"/api/users/{id}/api-tokens":        "API tokens",
	// Saved sign-in and what asks the device (the agent's tools)
	"/api/devices/{id}/signin":    "saved sign-in",
	"/api/devices/{id}/clipboard": "the device's clipboard",
	"/api/devices/{id}/processes": "asks the device",
	"/api/devices/{id}/services":  "asks the device",
	"/api/devices/{id}/events":    "asks the device",
	"/api/devices/{id}/activity":  "asks the device",
	// Secrets
	"/api/remote-targets/{id}/credentials": "credentials",
	"/api/panel/org/{id}/peer-credentials": "credentials",
	"/api/keys/api":                        "the API key",
	"/api/telemetry/key":                   "a key",
	"/api/peer-key/{id}":                   "a key",
	"/api/panel/tokens":                    "device tokens",
	"/api/panel/pairing/codes":             "pairing codes",
	"/api/guest/access-links":              "guest links",
	"/api/mesh/download.msh":               "the agent's settings",
	"/api/settings/backup":                 "a backup with the server's secrets",
	"/api/settings/advanced/files":         "the server's files",
	"/api/settings/advanced/files/{id}":    "the server's files",
	"/api/server-management/files":         "the server's files",
	"/api/server-management/files/read":    "the server's files",
	"/api/files/file-transfer/{id}":        "file transfers",
	"/api/nd/file-transfer/{id}":           "file transfers",
	"/api/nd/session/{id}":                 "remote sessions",
	"/api/support/session":                 "remote sessions",
	"/api/remote/login-info":               "remote sessions",
	"/api/remote/guest-info":               "remote sessions",
	"/api/remote/viewer/{deviceId}":        "remote sessions",
	"/remote-dashboard":                    "remote sessions",
	"/remote-desktop/{deviceId}":           "remote sessions",
	"/remote-cdap/{deviceId}":              "remote sessions",
	"/api/auth/oidc/authorize":             "signing in",
	"/api/auth/oidc/callback":              "signing in",
	"/api/oidc/callback":                   "signing in",
	"/api/auth/oidc/session":               "signing in",
	"/api/settings/management/config":      "the server's settings file",
}

// deniedPrefixes are path beginnings refused the same way, for reads that
// no route of a table answers (the remote client's pages).
var deniedPrefixes = []string{"/api/remote/", "/remote/session/"}

// readOnlySockets are the websockets an impersonation may open: they only
// push what the account may see. Every other socket sends, controls or
// relays something and is refused.
var readOnlySockets = map[string]bool{
	"/ws/device-status": true,
	"/ws/panel-events":  true,
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// verdict is what the gate does with a request of an impersonation.
type verdict int

const (
	pass verdict = iota
	refused
	signOut
)

// decide is the gate's rule for a request of an impersonation, with why
// for a refusal.
func (g *gate) decide(r *http.Request) (verdict, string) {
	path := strings.ToLower(strings.TrimSuffix(httpx.RawPath(r), "/"))
	if path == "" {
		path = "/"
	}
	key := r.Method + " " + path
	if allowed[key] {
		return pass, ""
	}
	if signOuts[key] {
		return signOut, ""
	}
	if !safeMethod(r.Method) {
		return refused, "changes data"
	}
	// Every route that may answer counts: the first one can pass the
	// request on to the next, and the early table to the other one
	for _, table := range g.tables {
		for _, route := range table.FindAll(r) {
			for _, need := range route.Permissions {
				for _, p := range strings.Split(need, " or ") {
					if deniedPermissions[strings.TrimSpace(p)] {
						return refused, "needs " + p
					}
				}
			}
			if why, denied := deniedRoutes[route.Path]; denied {
				return refused, orText(why, "not while viewing as another account")
			}
		}
	}
	if why, denied := deniedRoutes[path]; denied {
		return refused, orText(why, "not while viewing as another account")
	}
	for _, prefix := range deniedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return refused, "remote sessions"
		}
	}
	return pass, ""
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

type gate struct {
	s      *Service
	tables []*kit.Router
}

// Gate is the step of the request pipeline that holds an impersonation to
// read-only. It runs after the session and before the panel access guard
// and either route table, so every route is behind it and every refusal
// has its one code. For a request of an impersonation it
//
//   - checks again that the admin may still view the console as the
//     account (MayStillView), and ends the impersonation when not: an API
//     call is answered with 409 impersonation_ended, a page loads again as
//     the admin;
//   - refuses with 403 impersonation_read_only every write, and every
//     read of a route that starts remote work or shows a secret, but its
//     own routes; sign-out ends it and signs the admin out;
//   - writes an audit entry for each API request, the admin's, about the
//     account viewed.
//
// tables are the route tables, the early one first, as the pipeline asks
// them.
func (s *Service) Gate(tables ...*kit.Router) kit.Middleware {
	g := &gate{s: s, tables: tables}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			imp := kit.Impersonation(r)
			if imp == nil {
				if r.Header.Get(HeaderViewing) == "1" && isAPI(r) {
					httpx.WriteJSON(w, r, http.StatusConflict, refusal{Error: session.ImpersonationEndedText, Code: session.ImpersonationEndedCode})
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			g.serve(w, r, imp, next)
		})
	}
}

type refusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

func isAPI(r *http.Request) bool {
	return strings.HasPrefix(r.URL.EscapedPath(), "/api/") ||
		strings.Contains(strings.Join(r.Header.Values("Accept"), ", "), "application/json")
}

func (g *gate) serve(w http.ResponseWriter, r *http.Request, imp *session.Impersonation, next http.Handler) {
	user := kit.User(r)
	ctx := r.Context()
	why, err := g.s.MayStillView(ctx, imp.AdminID, user.ID, user.Role, imp.ExpiresAt)
	if err != nil {
		g.s.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	if why != nil {
		g.end(w, r, imp, user, why)
		return
	}
	v, reason := g.decide(r)
	if isAPI(r) {
		details := r.Method + " " + httpx.RawPath(r)
		if v == refused {
			details += " (refused: " + reason + ")"
		}
		g.s.audit(ctx, imp.AdminID, user.ID, ActionRequest, details, kit.ClientIP(r))
	}
	switch v {
	case refused:
		// One answer for every refusal, a page included
		text := "You are viewing NeoDesk as " + user.Username + ". You can look, but not change anything, connect to devices or see secrets."
		httpx.WriteJSON(w, r, http.StatusForbidden, refusal{Error: text, Code: CodeReadOnly})
		return
	case signOut:
		admin := session.Impersonator(r)
		g.s.d.Sessions.EndImpersonation(w, r, session.ReasonImpersonationStopped)
		g.s.audit(ctx, imp.AdminID, user.ID, ActionStopped, "Stopped viewing NeoDesk as "+user.Username+" by signing out", kit.ClientIP(r))
		if admin == nil {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, session.WithSession(r, admin))
		return
	}
	next.ServeHTTP(w, r)
}

// end ends an impersonation whose check failed and answers: 409 with the
// code impersonation_ended for an API call, the same page again (now as
// the admin) for a page.
func (g *gate) end(w http.ResponseWriter, r *http.Request, imp *session.Impersonation, user *session.User, why *Refusal) {
	g.s.d.Sessions.EndImpersonation(w, r, session.ReasonImpersonationEnded)
	g.s.audit(r.Context(), imp.AdminID, user.ID, ActionEnded, "Stopped viewing NeoDesk as "+user.Username+": "+why.Text("", nil), kit.ClientIP(r))
	if isAPI(r) {
		httpx.WriteJSON(w, r, http.StatusConflict, refusal{Error: why.Text("", g.bundle()), Code: session.ImpersonationEndedCode})
		return
	}
	httpx.Redirect(w, r, http.StatusFound, kit.OriginalURL(r))
}

func (g *gate) bundle() *i18n.Bundle {
	if g.s.d.I18n == nil {
		return nil
	}
	return g.s.d.I18n.Bundle
}

// audit writes an entry of the admin about the account viewed. A failed
// entry is logged; the request goes on.
func (s *Service) audit(ctx context.Context, adminID, targetID int64, action, details, ip string) {
	if err := s.d.Audit.WriteAbout(context.WithoutCancel(ctx), adminID, targetID, action, details, ip); err != nil {
		logging.Logf("console", "WARN: impersonation audit entry: %v", err)
	}
}

// Audit writes an entry of the admin about the account viewed, for the
// routes of the impersonation.
func (s *Service) Audit(r *http.Request, adminID, targetID int64, action, details string) {
	s.audit(r.Context(), adminID, targetID, action, details, kit.ClientIP(r))
}

// RefuseUpgrade refuses a websocket upgrade of a browser that views the
// console as another account, for every socket but those that only push
// what the account may see (the device status and the panel events). It
// reports whether it answered. pattern is the registered path of the
// socket. A refused upgrade gets an audit entry when the impersonation is
// live.
func (s *Service) RefuseUpgrade(w http.ResponseWriter, r *http.Request, pattern string) bool {
	if s.d.Sessions.ImpersonationCookieID(r) == "" || readOnlySockets[pattern] {
		return false
	}
	if sess, err := s.d.Sessions.Load(r); err == nil && sess != nil {
		if imp := sess.Impersonation(); imp != nil {
			s.audit(r.Context(), imp.AdminID, sess.UserID(), ActionRequest, "socket "+httpx.RawPath(r)+" (refused: opens a connection)", s.d.Config.TrustProxy.ClientIP(r))
		}
	}
	httpx.WriteJSON(w, r, http.StatusForbidden, refusal{Error: "You are viewing NeoDesk as another account. Connections can't be opened.", Code: CodeReadOnly})
	return true
}
