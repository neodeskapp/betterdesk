package meshcentral

import (
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// desktop answers POST /api/mesh/devices/{id}/desktop, which opens a
// desktop tunnel to a mesh agent. Two kinds of callers use it:
//
//   - Someone with a link, signed in or not: ?mesh_share=<token> for a
//     share link, ?guest=<token> or ?t=<token> for a guest link. The API
//     server says whether the token is good for this device, and the
//     request goes on with the token and the flags of the tunnel
//     (linkParameters), each one text: a list or an object among them
//     is 400. A guest link's token goes on
//     as guest, without t, so the API server checks the link again and
//     writes the tunnel to the link's creator; a link that only allows
//     watching sets view_only=1, whatever the query says.
//   - Someone signed in with device.connect. Of the query only the
//     tunnel's flags record and relay_base go on (sessionParameters),
//     each one text: a list or an object among them is 400.
//
// A share token wins over a guest token; the guest token is then not
// checked here. A token that cannot be turned into text is refused with
// 400, as the guest routes refuse it, and so is a device id "." or "..".
func (h *handlers) desktop(w http.ResponseWriter, r *http.Request) {
	if !validDevice(w, r) {
		return
	}
	id := r.PathValue("id")
	query := requestQuery(r)
	share, shareIsText := trimmedText(query.get("mesh_share"))
	if !shareIsText {
		kit.JSON(w, r, http.StatusBadRequest, apiFailure{Error: "Invalid share token"})
		return
	}
	guest, guestIsText := trimmedText(orValue(query.get("guest"), query.get("t")))
	if !guestIsText {
		kit.JSON(w, r, http.StatusBadRequest, apiFailure{Error: "Invalid guest token"})
		return
	}
	if share == "" && guest == "" {
		h.desktopForSession.ServeHTTP(w, r)
		return
	}
	if _, bad := query.only(linkParameters...); bad != "" {
		badParameter(w, r, bad)
		return
	}

	const badShare, badGuest = "Invalid or expired share link", "Invalid or expired guest link"
	token := []goapi.CallOption{goapi.Query("token", share), goapi.Query("peer_id", id)}
	if share != "" {
		resp, err := h.api.Get(r.Context(), "/mesh/share/validate", token...)
		if err != nil || !goapi.Truthy(goapi.Dig(resp.Data(), "valid")) {
			kit.JSON(w, r, http.StatusForbidden, apiFailure{Error: badShare})
			return
		}
	} else {
		token[0] = goapi.Query("token", guest)
		resp, err := h.api.Get(r.Context(), "/guest/access-links/validate", token...)
		if err != nil {
			kit.JSON(w, r, http.StatusForbidden, apiFailure{Error: badShare})
			return
		}
		grant := resp.Data()
		if !goapi.Truthy(goapi.Dig(grant, "valid")) {
			kit.JSON(w, r, http.StatusForbidden, apiFailure{Error: badGuest})
			return
		}
		query.delete("t")
		query.set("guest", guest)
		if goapi.Truthy(goapi.Dig(grant, "view_only")) {
			query.set("view_only", "1")
		}
	}
	search, _ := query.only(linkParameters...)
	h.pass(w, r, http.MethodPost, withSearch(devicePath(id, "desktop"), search))
}

// linkParameters are the parameters of a desktop request with a link that
// the API server reads: the link's token and the flags of the tunnel.
var linkParameters = []string{"mesh_share", "guest", "view_only", "record", "relay_base"}

// sessionParameters are the parameters of a signed-in account's desktop
// request that the API server reads: server recording and the address
// the relay is reached at.
var sessionParameters = []string{"record", "relay_base"}

// desktopTunnel opens the tunnel for a signed-in account.
func (h *handlers) desktopTunnel(w http.ResponseWriter, r *http.Request) {
	search, bad := requestQuery(r).only(sessionParameters...)
	if bad != "" {
		badParameter(w, r, bad)
		return
	}
	h.pass(w, r, http.MethodPost, withSearch(devicePath(r.PathValue("id"), "desktop"), search))
}

// trimmedText is the value as text without the white space around it,
// and "" for a missing or empty value. ok is false when the value
// cannot be turned into text.
func trimmedText(v any) (text string, ok bool) {
	if !truthy(v) {
		return "", true
	}
	text, ok = toText(v)
	return strings.TrimFunc(text, isSpace), ok
}

// orValue is a || b.
func orValue(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
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
