package auth

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/attestation"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// configuredAppName is the name the old console's configuration gave
// itself, the sign-in page's appName when the branding has none.
const configuredAppName = "BetterDesk Console"

// loginPageAnswer is the answer of GET /api/auth/login-page, with the keys
// in the order the web app has always received them.
type loginPageAnswer struct {
	Success    bool   `json:"success"`
	CSRFToken  string `json:"csrfToken"`
	AppName    string `json:"appName"`
	AppVersion string `json:"appVersion"`
	// DesktopMode is set by the old desktop mode's cookie, which still
	// asks for the compact sign-in.
	DesktopMode bool `json:"desktopMode"`
	// SignedIn says whether this browser has a console session: error
	// pages then keep the console shell.
	SignedIn    bool             `json:"signedIn"`
	Branding    *branding.Object `json:"branding"`
	Languages   []*i18n.Object   `json:"languages"`
	Attestation *branding.Object `json:"attestation"`
}

// loginPage answers GET /api/auth/login-page: what the sign-in page shows
// before anyone is signed in. The CSRF token for the sign-in requests,
// name, version and branding of the console, the languages and the
// capacity badge. No user data.
func (h *handlers) loginPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s := h.d.Branding
	s.Refresh(ctx)
	badge := attestation.SignInBadge(ctx, attestation.StoreSettings(h.d.Store), s.GetBranding().Text("appName"), func(key string) string { return kit.T(r, key, nil) })
	languages, err := languageList(h.d.I18n.Bundle)
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	desktop, _ := kit.Cookie(r, "betterdesk_desktop_mode")
	user := kit.User(r)
	answer := loginPageAnswer{
		Success:     true,
		CSRFToken:   kit.CSRFToken(r),
		AppName:     s.AppName(configuredAppName),
		AppVersion:  h.appVersion,
		DesktopMode: desktop == "true",
		SignedIn:    kit.UserID(r) != 0 && (user == nil || user.Role != "pro"),
		Branding:    s.SignInBranding(),
		Languages:   languages,
		Attestation: badge,
	}
	w.Header().Set("Cache-Control", "no-store")
	kit.JSON(w, r, http.StatusOK, answer)
}

// productVersion is the product's version as the old console read it at
// start: the VERSION file above the console's folder, the one in it, its
// package.json, else 0.0.0.
func productVersion(d *kit.Deps) string {
	dir := d.Config.ConsoleDir
	return serverinfo.ReadProductVersion(serverinfo.OSFS{}, filepath.Join(dir, ".."), dir, serverinfo.DefaultVersionFallback)
}

// languageList is every language as the sign-in page lists them: code,
// name, native name, flag and whether it is written right to left, sorted
// by the native name. It fails where sorting the list failed in
// JavaScript, on a name that is no text.
func languageList(bundle *i18n.Bundle) ([]*i18n.Object, error) {
	entries, err := bundle.AvailableList()
	if err != nil {
		return nil, fmt.Errorf("list the languages: %w", err)
	}
	out := make([]*i18n.Object, 0, len(entries))
	for _, entry := range entries {
		l := i18n.NewObject()
		for _, key := range []string{"code", "name", "native", "flag"} {
			// The bundle gives every language a code, and a name, native
			// name and flag that are not empty
			if v, ok := entry.Get(key); ok {
				l.Set(key, v)
			}
		}
		rtl, _ := entry.Get("rtl")
		l.Set("rtl", rtl == true)
		out = append(out, l)
	}
	return out, nil
}
