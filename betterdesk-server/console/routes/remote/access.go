package remote

import (
	"context"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// grants are what the access check of the viewer found besides a session:
// a guest link (req.guestGrant, req.guestToken) and a share link of a
// mesh device (req.meshShareGrant).
type grants struct {
	guest      *guestaccess.Grant
	meshShare  any
	hasShare   bool
	guestToken string
}

type grantsKey struct{}

// grantsOf is what requireRemoteAccess found for the request.
func grantsOf(r *http.Request) *grants {
	if g, ok := r.Context().Value(grantsKey{}).(*grants); ok {
		return g
	}
	return &grants{}
}

// abort ends a request where the old console's process stopped (an
// object with a member toString where it needed text, BUGS.md MC1): the
// connection is closed without an answer.
func abort() {
	panic(http.ErrAbortHandler)
}

// tokenFailure is the answer to a guest or share token that cannot be
// turned into text: 400, as the guest and MeshCentral routes answer it.
type tokenFailure struct {
	Error string `json:"error"`
}

var (
	badGuestToken = tokenFailure{Error: "Invalid guest token"}
	badShareToken = tokenFailure{Error: "Invalid share token"}
)

// guestDenied is the answer to an explicit guest link that does not
// reach the device.
type guestDenied struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Error   string `json:"error"`
}

// requireRemoteAccess lets a request for the viewer through when one of
// these allows it, in this order:
//
//   - a session whose account has device.connect (remote_target.connect
//     for an rt_ target): the remote client's guard decides;
//   - for an rt_ target nothing else: the guard decides;
//   - a guest link (?guest=, ?t= or the guest cookie) good for this
//     device; it is stored in the guest cookie. A link from the query
//     that is not good answers 403 guest_denied, one from the cookie only
//     is ignored;
//   - a share link of a mesh device (?mesh_share=) the API server finds
//     good for this device;
//   - else the remote client's guard with device.connect.
//
// A guest token (query or cookie) or share token that cannot be turned
// into text is refused with 400.
func (h *handlers) requireRemoteAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("deviceId")
		target := strings.HasPrefix(deviceID, "rt_")
		permission := "device.connect"
		if target {
			permission = "remote_target.connect"
		}
		if kit.UserID(r) != 0 && kit.HasPermission(r, permission) {
			h.d.RequireRdClientAuth(permission)(next).ServeHTTP(w, r)
			return
		}
		if target {
			h.d.RequireRdClientAuth(permission)(next).ServeHTTP(w, r)
			return
		}

		g := &grants{}
		r = r.WithContext(context.WithValue(r.Context(), grantsKey{}, g))
		guests := h.d.GuestAccess
		queryToken, err := guests.TokenFromQuery(r)
		if err != nil {
			kit.JSON(w, r, http.StatusBadRequest, badGuestToken)
			return
		}
		guestToken, err := guests.Token(r)
		if err != nil {
			kit.JSON(w, r, http.StatusBadRequest, badGuestToken)
			return
		}
		if guestToken != "" && deviceID != "" {
			grant, err := guests.AttachGrant(r.Context(), r, deviceID)
			if err != nil {
				kit.JSON(w, r, http.StatusBadRequest, badGuestToken)
				return
			}
			if grant != nil {
				g.guest, g.guestToken = grant, grant.Token
				if guestaccess.PeerAllowed(grant.Data, deviceID) {
					guests.SetCookie(w, guestToken, goapi.Dig(grant.Data, "expires_at"))
					next.ServeHTTP(w, r)
					return
				}
			}
			if queryToken != "" {
				h.log.Info("Guest remote deny (explicit query, invalid/expired/not allowed):", deviceID)
				kit.JSON(w, r, http.StatusForbidden, guestDenied{
					Code:  "guest_denied",
					Error: kit.T(r, "guest_access.device_denied", nil),
				})
				return
			}
			h.log.Debug("Stale guest cookie ignored; falling through to panel auth")
		}

		share, ok := queryText(reqquery.Of(r), "mesh_share", "")
		if !ok {
			kit.JSON(w, r, http.StatusBadRequest, badShareToken)
			return
		}
		if share = jsbind.Trim(share); share != "" && deviceID != "" {
			resp, err := h.d.GoAPI.Get(r.Context(), "/mesh/share/validate", goapi.Query("token", share), goapi.Query("peer_id", deviceID))
			if err == nil {
				data := resp.Data()
				if !goapi.Truthy(data) {
					data = goapi.NewObject()
				}
				if goapi.Truthy(goapi.Dig(data, "valid")) {
					g.meshShare, g.hasShare = data, true
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		h.d.RequireRdClientAuth("device.connect")(next).ServeHTTP(w, r)
	})
}
