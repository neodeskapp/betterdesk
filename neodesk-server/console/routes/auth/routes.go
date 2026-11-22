package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
)

// housekeepingInterval is how often expired access tokens and old sign-in
// attempts are deleted.
const housekeepingInterval = time.Hour

type handlers struct {
	d        *kit.Deps
	log      *kit.Logger
	authn    *authn.Service
	sessions *session.Manager
	// guestCookie is the cookie of a guest link. A sign-in clears it.
	guestCookie string
	now         func() time.Time
	// appVersion is the product version, read once at start.
	appVersion string
	// syncPicture brings the account's profile picture in line with the
	// "picture" of a single sign-on. It returns at once and works in the
	// background. nil skips it.
	syncPicture func(userID int64, exchange *goapi.Object)
}

func newHandlers(d *kit.Deps) *handlers {
	h := &handlers{
		d:           d,
		log:         d.Log.Child("AUTH"),
		authn:       d.Authn,
		sessions:    d.Sessions,
		guestCookie: "bd.guest",
		now:         time.Now,
		appVersion:  d.Config.Version,
	}
	if d.Config.HTTPS {
		h.guestCookie = "betterdesk.guest"
	}
	if d.Avatars != nil {
		h.syncPicture = func(userID int64, exchange *goapi.Object) { d.Avatars.StartOIDCSync(userID, exchange) }
	}
	return h
}

// Register adds the group's routes, and the hourly clean-up of what
// sign-in leaves behind.
func Register(d *kit.Deps) {
	h := newHandlers(d)
	r := d.Router
	r.Post("/api/auth/login", h.login, d.Limits.Login.Middleware)
	r.Post("/api/auth/logout", h.logout)
	r.Get("/api/auth/verify", h.verify, d.RequireAuth)
	r.Get("/api/auth/sessions", h.sessionList, d.RequireAuth)
	r.Post("/api/auth/password", h.changePassword, d.RequireAuth, d.Limits.PasswordChange.Middleware)
	r.Get("/logout", h.logoutPage)
	r.Post("/logout", h.logoutForm)
	r.Get("/api/auth/oidc/status", h.oidcStatus)
	r.Get("/api/auth/oidc/authorize", h.oidcAuthorize)
	r.Get("/api/auth/oidc/callback", h.oidcCallback)
	r.Get("/api/oidc/callback", h.oidcCallback)
	r.Get("/api/auth/oidc/session", h.oidcSession)
	r.Post("/api/auth/totp/verify", h.totpVerify, d.Limits.TOTPVerify.Middleware)
	r.Post("/api/auth/totp/setup", h.totpSetup, d.RequireAuth)
	r.Post("/api/auth/totp/enable", h.totpEnable, d.RequireAuth)
	r.Post("/api/auth/totp/disable", h.totpDisable, d.RequireAuth)
	r.Get("/api/auth/totp/status", h.totpStatus, d.RequireAuth)
	r.Post("/api/auth/totp/recovery-codes", h.totpRecoveryCodes, d.RequireAuth, d.Limits.TOTPVerify.Middleware)
	r.Post("/api/auth/totp/required/setup", h.requiredSetup, d.Limits.TOTPVerify.Middleware)
	r.Post("/api/auth/totp/required/enable", h.requiredEnable, d.Limits.TOTPVerify.Middleware)
	r.Get("/api/auth/login-page", h.loginPage)

	// The Node console runs the same clean-up while it is still there. Both
	// delete by a rule on time, so a second run finds nothing left to do.
	d.Jobs.Add("sign-in housekeeping", func(ctx context.Context) { h.housekeeping(ctx, housekeepingInterval) })
}

// housekeeping deletes access tokens that are expired or revoked and
// sign-in attempts older than a day, once every interval until ctx ends.
func (h *handlers) housekeeping(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.authn.CleanupHousekeeping(ctx)
		}
	}
}

// serverError answers 500 with the request language's text for a server
// error, which is what most failures of these routes answer.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

// audit writes an audit entry for an account (0 for none) with the
// client's address.
func (h *handlers) audit(r *http.Request, userID int64, action, details string) error {
	return h.d.Audit.Write(r.Context(), userID, action, details, kit.ClientIP(r))
}
