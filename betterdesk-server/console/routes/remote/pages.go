package remote

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// pageInfo is what the remote client's pages without a session may show:
// the console's name and logo, the languages and a CSRF token for the
// sign-in form. No user data.
type pageInfo struct {
	AppName    any              `json:"appName"`
	AppVersion string           `json:"appVersion"`
	Branding   *branding.Object `json:"branding"`
	Languages  []*i18n.Object   `json:"languages"`
	CSRFToken  string           `json:"csrfToken"`
}

func (h *handlers) pageInfo(r *http.Request) pageInfo {
	b := h.src.branding(r.Context())
	or := func(key string, fallback any) any {
		if v := branding.Field(b, key); branding.Truthy(v) {
			return v
		}
		return fallback
	}
	logo := branding.NewObject()
	logo.Set("logoType", or("logoType", nil))
	for _, key := range []string{"logoIcon", "logoSvg", "logoUrl", "logoText", "logoTextAccent"} {
		logo.Set(key, or(key, ""))
	}
	return pageInfo{
		AppName:    or("appName", "BetterDesk"),
		AppVersion: h.src.appVersion,
		Branding:   logo,
		Languages:  h.languages(),
		CSRFToken:  kit.CSRFToken(r),
	}
}

// languages is every language as the remote pages list them: code, name,
// native name (the name when there is none), flag and whether it is
// written right to left. A list that cannot be read is empty.
func (h *handlers) languages() []*i18n.Object {
	out := []*i18n.Object{}
	entries, err := h.src.languages()
	if err != nil {
		return out
	}
	for _, entry := range entries {
		get := func(key string) any {
			v, _ := entry.Get(key)
			return v
		}
		l := i18n.NewObject()
		l.Set("code", get("code"))
		l.Set("name", get("name"))
		l.Set("native", orValue(get("native"), get("name")))
		l.Set("flag", orValue(get("flag"), ""))
		l.Set("rtl", goapi.Truthy(get("rtl")))
		out = append(out, l)
	}
	return out
}

// loginInfo is the answer of GET /api/remote/login-info.
type loginInfo struct {
	Success   bool   `json:"success"`
	SignedIn  bool   `json:"signedIn"`
	ReturnURL string `json:"returnUrl"`
	pageInfo
}

// loginInfo answers GET /api/remote/login-info?return=<path>, the data of
// the /remote/login page: where to go after sign-in (a path under /remote
// the return rules allow, else /remote) and whether this browser is
// signed in with device.connect already, which the page sends straight on.
func (h *handlers) loginInfo(w http.ResponseWriter, r *http.Request) {
	user := kit.User(r)
	signedIn := kit.UserID(r) != 0 && (user == nil || user.Role != "pro") && kit.HasPermission(r, "device.connect")
	w.Header().Set("Cache-Control", "no-store")
	requested := ""
	if text, ok := reqquery.Of(r).Get("return").IsText(); ok {
		requested = text
	}
	target, ok := access.NormalizeRdClientReturnURL(requested)
	if !ok {
		target = "/remote"
	}
	kit.JSON(w, r, http.StatusOK, loginInfo{Success: true, SignedIn: signedIn, ReturnURL: target, pageInfo: h.pageInfo(r)})
}

// guestFailure is a refused guest page.
type guestFailure struct {
	Success bool     `json:"success"`
	Code    string   `json:"code"`
	Error   any      `json:"error"`
	Page    pageInfo `json:"page"`
}

// guestPage is the answer to a good guest link.
type guestPage struct {
	Success bool      `json:"success"`
	Token   string    `json:"token"`
	Guest   guestInfo `json:"guest"`
	Page    pageInfo  `json:"page"`
}

type guestInfo struct {
	ViewOnly  bool `json:"view_only"`
	ExpiresAt any  `json:"expires_at"`
	Label     any  `json:"label"`
	Devices   any  `json:"devices"`
}

// guestInfo answers GET /api/remote/guest-info?t=<token>, the data of the
// /remote/guest page: the link's token (from the query or the guest
// cookie) must be good on the API server, which also names its devices;
// a good one is stored in the guest cookie.
func (h *handlers) guestInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	page := h.pageInfo(r)
	token, err := h.d.GuestAccess.Token(r)
	if err != nil {
		abort()
	}
	if token == "" {
		kit.JSON(w, r, http.StatusBadRequest, guestFailure{Code: "missing_token", Error: kit.T(r, "guest_access.missing_token", nil), Page: page})
		return
	}
	expired := func(reason any) {
		if !goapi.Truthy(reason) {
			reason = kit.T(r, "guest_access.expired", nil)
		}
		kit.JSON(w, r, http.StatusForbidden, guestFailure{Code: "invalid", Error: reason, Page: page})
	}
	resp, err := h.d.GoAPI.Get(r.Context(), "/guest/access-links/peers", goapi.Query("token", token))
	if err != nil {
		var reason any
		if e := goapi.AsError(err); e != nil {
			reason = goapi.Dig(e.Data(), "error")
		}
		h.log.Warn("Guest guest-info peers/API error:", err.Error())
		expired(reason)
		return
	}
	data := resp.Data()
	if !goapi.Truthy(data) {
		data = goapi.NewObject()
	}
	if !goapi.Truthy(goapi.Dig(data, "valid")) {
		expired(goapi.Dig(data, "error"))
		return
	}
	h.d.GuestAccess.SetCookie(w, token, goapi.Dig(data, "expires_at"))
	devices, ok := goapi.Dig(data, "devices").([]any)
	if !ok {
		devices = []any{}
	}
	kit.JSON(w, r, http.StatusOK, guestPage{
		Success: true,
		Token:   token,
		Guest: guestInfo{
			ViewOnly:  goapi.Truthy(goapi.Dig(data, "view_only")),
			ExpiresAt: orValue(goapi.Dig(data, "expires_at"), ""),
			Label:     orValue(goapi.Dig(data, "label"), ""),
			Devices:   devices,
		},
		Page: page,
	})
}

// desk answers GET /api/remote/desk, which the remote dashboard asks
// first: it needs device.connect, and it drops a guest cookie left from a
// guest link, so the console session is used from here on.
func (h *handlers) desk(w http.ResponseWriter, r *http.Request) {
	h.d.GuestAccess.ClearCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	kit.OK(w, r, nil)
}

// dashboard answers the old address of the remote dashboard with a
// permanent redirect to /remote.
func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	httpx.Redirect(w, r, http.StatusMovedPermanently, "/remote")
}

// cdapRedirect answers the old address /remote-cdap/:deviceId: the viewer
// with the CDAP transport, or the device list for an id that is no device
// id. The viewer's data checks the access.
func (h *handlers) cdapRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	if id != "" && deviceIDPattern.MatchString(id) {
		kit.Redirect(w, r, "/remote/"+goapi.EncodeURIComponent(id)+"?transport=cdap")
		return
	}
	kit.Redirect(w, r, "/devices")
}

// desktopRedirect answers the old address /remote-desktop/:deviceId: the
// viewer, or the device list for an id that is no device id.
func (h *handlers) desktopRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deviceId")
	if id != "" && deviceIDPattern.MatchString(id) {
		kit.Redirect(w, r, "/remote/"+goapi.EncodeURIComponent(id))
		return
	}
	kit.Redirect(w, r, "/devices")
}
