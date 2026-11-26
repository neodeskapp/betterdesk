package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session/cookie"
)

// A sign-in from a browser the account has not used before mails the
// person (securitymail.NewSignIn). The browser is recognised by a random
// value in a long-lived cookie, which the console sets at sign-in; only
// its hash is stored, per account (authn.NoteBrowser). The cookie is set
// and the browser stored only while the notice can go out: the console can
// send mail, the account has an address and the notice is switched on.

// browserCookie is the name of the cookie.
const browserCookie = "nd.browser"

// browserCookieLife is how long the browser keeps it: the longest a
// browser allows.
const browserCookieLife = 400 * 24 * time.Hour

var browserValue = regexp.MustCompile(`^[0-9a-f]{32}$`)

// noteBrowser records the browser of a finished sign-in and sends the
// notice when the browser is new to the account. A failure changes
// nothing about the sign-in.
func (h *handlers) noteBrowser(w http.ResponseWriter, r *http.Request, userID int64, meta session.Meta) {
	ctx := r.Context()
	if !h.notices.Ready(ctx, userID, securitymail.KeyNewSignIn) {
		return
	}
	value, _ := cookie.Get(r, browserCookie)
	if !browserValue.MatchString(value) {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return
		}
		value = hex.EncodeToString(raw)
	}
	cookie.Set(w, browserCookie, value, cookie.Attributes{
		MaxAge: browserCookieLife, Path: "/", HTTPOnly: true, Secure: h.d.Sessions.SecureCookie(r), SameSite: "lax",
	})
	isNew, err := h.authn.NoteBrowser(ctx, userID, value)
	if err != nil {
		h.log.Warn("Noting the browser of a sign-in failed:", err)
		return
	}
	if isNew {
		h.notices.NewSignIn(ctx, userID, securitymail.SignIn{At: h.now(), IP: meta.IP, Browser: browserName(meta.UserAgent)})
	}
}

// browserName is "Firefox on Windows", as the signed-in devices list has
// it, or what of it is known.
func browserName(userAgent string) string {
	browser, system := session.DescribeUserAgent(userAgent)
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	}
	return strings.TrimSpace(system)
}

// signedInNotes is what every finished sign-in does besides its session:
// the guest cookie goes, and the browser is noted.
func (h *handlers) signedInNotes(w http.ResponseWriter, r *http.Request, userID int64, meta session.Meta) {
	h.clearGuestCookie(w, r)
	h.noteBrowser(w, r, userID, meta)
}
