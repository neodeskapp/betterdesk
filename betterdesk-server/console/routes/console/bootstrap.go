package console

import (
	"fmt"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
)

// bootstrapAnswer is the answer of GET /api/console/bootstrap, with the
// keys in the order the web app has always received them.
type bootstrapAnswer struct {
	Success       bool                         `json:"success"`
	User          bootstrapUser                `json:"user"`
	Permissions   []string                     `json:"permissions"`
	Access        *access.BootstrapAccess      `json:"access"`
	IsSuperAdmin  bool                         `json:"isSuperAdmin"`
	EmergencyMode bool                         `json:"emergencyMode"`
	Nav           []access.BootstrapNavSection `json:"nav"`
	Lang          bootstrapLang                `json:"lang"`
	Branding      *branding.Object             `json:"branding"`
	Palettes      any                          `json:"palettes"`
	SessionPolicy session.Policy               `json:"sessionPolicy"`
	CSRFToken     string                       `json:"csrfToken"`
	AppVersion    string                       `json:"appVersion"`
}

type bootstrapUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	// PreferredLanguage is null when the account has none.
	PreferredLanguage *string `json:"preferredLanguage"`
	// AvatarVersion is null without a picture: the app shows initials.
	AvatarVersion store.NullString `json:"avatarVersion"`
}

type bootstrapLang struct {
	Code       string `json:"code"`
	Dir        string `json:"dir"`
	StringsURL string `json:"stringsUrl"`
	// Available is every language, sorted by its own name.
	Available []*i18n.Object `json:"available"`
}

// bootstrap answers GET /api/console/bootstrap: everything the web app
// needs to render for the signed-in account in one answer. The
// permissions and the navigation only decide what the app shows; every
// request is still checked on its own.
func (h *handlers) bootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h.d.Branding.Refresh(ctx)
	languages, err := languageList(h.d.I18n.Bundle)
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	user := kit.User(r)
	c := kit.Access(r)
	superAdmin := access.IsSuperAdminRole(user.Role)
	nav := access.BuildNav(access.NavInput{
		Can:          c.HasPermission,
		IsSuperAdmin: superAdmin,
		Role:         user.Role,
		T:            func(key string) string { return kit.T(r, key, nil) },
		Allows:       access.PageAllowed(c.Policy),
	})
	boot := h.publicBoot(r)
	lang, _ := boot.Get("lang")
	dir, _ := boot.Get("dir")
	stringsURL, _ := boot.Get("stringsUrl")
	palettes, _ := boot.Get("palettes")

	answer := bootstrapAnswer{
		Success: true,
		User: bootstrapUser{
			ID:            user.ID,
			Username:      user.Username,
			Role:          user.Role,
			AvatarVersion: h.d.Avatars.GetAvatarVersion(ctx, kit.UserID(r)),
		},
		Permissions:   c.GrantedUIPermissions(),
		Access:        access.AccessFor(c.Policy),
		IsSuperAdmin:  superAdmin,
		EmergencyMode: sessionFlag(kit.Session(r), "emergencyMode"),
		Nav:           nav.Bootstrap(),
		Lang:          bootstrapLang{Code: lang.(string), Dir: dir.(string), StringsURL: stringsURL.(string), Available: languages},
		Branding:      h.d.Branding.BootstrapBranding(h.appName),
		Palettes:      palettes,
		SessionPolicy: h.d.Sessions.Policy(),
		CSRFToken:     kit.CSRFToken(r),
		AppVersion:    h.appVersion,
	}
	if user.PreferredLanguage != "" {
		answer.User.PreferredLanguage = &user.PreferredLanguage
	}
	w.Header().Set("Cache-Control", "no-store")
	kit.JSON(w, r, http.StatusOK, answer)
}

// sessionFlag is !!req.session[key].
func sessionFlag(s *session.Session, key string) bool {
	var value any
	if !s.Get(key, &value) {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}

// languageList is every language as the web app lists them: code, name,
// native name, flag and whether it is written right to left, sorted by
// the native name. It fails where sorting the list failed in JavaScript,
// on a name that is no text.
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
