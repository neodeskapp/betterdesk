// Package uishell keeps a browser's choice between the console's two
// shells, UX 3.5 and the UX 3.6 beta, in the bd_ui_shell cookie.
package uishell

import (
	"net/http"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

const (
	shellCookie = "bd_ui_shell"
	cookieAge   = 365 * 24 * time.Hour
	defaultName = "ux35"
)

// supported lists the shells a browser can choose.
var supported = []string{"ux35", "ux36"}

type handlers struct {
	// secure says whether the cookie of an answer to r is Secure
	// (session.Manager.SecureCookie).
	secure func(r *http.Request) bool
	now    func() time.Time
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{secure: d.Sessions.SecureCookie, now: time.Now}
	r := d.Router
	// The choice's JSON is read after the sign-in check, up to 2 KB
	r.Post("/api/ui-shell", h.set, d.RequireAuth, middleware.RouteJSON(middleware.BodyOptions{Limit: 2 << 10, OnError: func(w http.ResponseWriter, r *http.Request, status int, err error) {
		d.WebUI.ServerError(w, r, err, status)
	}}))
	r.Get("/api/ui-shell", h.get, d.RequireAuth)
}

type choice struct {
	Shell     string   `json:"shell"`
	Supported []string `json:"supported,omitempty"`
}

// set answers POST /api/ui-shell with the body {"shell":"ux35"|"ux36"}:
// it stores the choice in the cookie for a year.
func (h *handlers) set(w http.ResponseWriter, r *http.Request) {
	text, _ := kit.Body(r).String("shell")
	shell, ok := normalize(text)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "Unsupported shell. Allowed: "+strings.Join(supported, ", "))
		return
	}
	kit.SetCookie(w, shellCookie, shell, kit.CookieOptions{MaxAge: cookieAge, SameSite: "lax", Secure: h.secure(r), Now: h.now()})
	kit.OK(w, r, choice{Shell: shell})
}

// get answers GET /api/ui-shell: the shell the cookie names, ux35 when it
// names none, and the shells there are.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	text, _ := kit.Cookie(r, shellCookie)
	shell, ok := normalize(text)
	if !ok {
		shell = defaultName
	}
	kit.OK(w, r, choice{Shell: shell, Supported: supported})
}

// normalize reads a shell name: white space around it and the case of its
// letters do not count.
func normalize(value string) (string, bool) {
	name := strings.ToLower(strings.TrimFunc(value, isSpace))
	for _, shell := range supported {
		if name == shell {
			return shell, true
		}
	}
	return "", false
}

// isSpace reports the white space JavaScript's trim removes.
func isSpace(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r == ' ', r == 0xA0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200A:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}
