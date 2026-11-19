// Package guest holds the console's routes for guest access links: an
// operator makes a link for some devices, lists the links and takes one
// back; the holder of a link, who has no account, asks whether it is good
// and which devices it reaches, and opens a desktop tunnel to a mesh
// agent with it.
//
// The API server keeps the links. The console checks that an operator
// only makes links for devices in the operator's device scope.
package guest

import (
	"context"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// deviceScope is the set of devices an account may reach
// (*access.DeviceScope).
type deviceScope interface {
	Unlimited() bool
	Has(id string) bool
}

// scopeFunc gives the device scope of an account among devices
// (deviceGroupService.getDeviceScopeForUser).
type scopeFunc func(ctx context.Context, user *access.ScopeUser, devices []access.Device) (deviceScope, error)

type handlers struct {
	api    *goapi.Client
	guests *guestaccess.Service
	scope  scopeFunc
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	register(d, func(ctx context.Context, user *access.ScopeUser, devices []access.Device) (deviceScope, error) {
		return d.Access.DeviceScopeForUser(ctx, user, devices)
	})
}

func register(d *kit.Deps, scope scopeFunc) {
	h := &handlers{api: d.GoAPI, guests: d.GuestAccess, scope: scope}
	create := d.RequirePermission("guest.create")
	page := d.Limits.RDClientPage.Middleware

	r := d.Router
	// For operators
	r.Post("/api/guest/access-links", h.create, d.RequireAuth, create)
	r.Get("/api/guest/access-links", h.list, d.RequireAuth, create)
	r.Delete("/api/guest/access-links/{id}", h.revoke, d.RequireAuth, create)
	// For the holder of a link, with or without a session
	r.Get("/api/guest/access-links/validate", h.validate, page)
	r.Get("/api/guest/access-links/peers", h.peers, page)
	r.Post("/api/guest/mesh/devices/{id}/desktop", h.desktop, page)
}

// failure is the answer {"error": text} of this group.
type failure struct {
	Error string `json:"error"`
}

// create answers POST /api/guest/access-links with the body {peer_ids,
// ttl_minutes, view_only, label, max_uses}. Every peer id is taken as
// text. The account must have all of the devices in its device scope.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	list, _ := body.Get("peer_ids").Array()
	if len(list) == 0 {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "peer_ids required"})
		return
	}
	peerIDs := make([]any, len(list))
	devices := make([]access.Device, len(list))
	for i, item := range list {
		value, err := goapi.Parse(item.Raw())
		text, isText := guestaccess.JSText(value)
		if err != nil || !isText {
			kit.JSON(w, r, http.StatusInternalServerError, failure{Error: guestaccess.ErrNotText.Error()})
			return
		}
		peerIDs[i] = text
		devices[i] = access.Device{ID: text}
	}

	scope, err := h.scope(r.Context(), scopeUser(r), devices)
	if err != nil {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: err.Error()})
		return
	}
	if !scope.Unlimited() {
		for _, device := range devices {
			if !scope.Has(device.ID) {
				kit.JSON(w, r, http.StatusForbidden, failure{Error: "One or more devices are outside your device scope"})
				return
			}
		}
	}

	link := goapi.NewObject("peer_ids", peerIDs)
	if ttl, defined := bodyValue(r, "ttl_minutes"); defined {
		link.Set("ttl_minutes", ttl)
	}
	link.Set("view_only", body.Get("view_only").Truthy())
	link.Set("label", "")
	if label, _ := bodyValue(r, "label"); goapi.Truthy(label) {
		link.Set("label", label)
	}
	link.Set("max_uses", float64(0))
	if uses, _ := bodyValue(r, "max_uses"); goapi.Truthy(uses) {
		link.Set("max_uses", uses)
	}
	goapipath.ProxyToGo(h.api, w, r, http.MethodPost, goapipath.Fixed("/guest/access-links"), goapipath.Body(link))
}

// scopeUser is the signed-in account for the device scope.
func scopeUser(r *http.Request) *access.ScopeUser {
	user := kit.User(r)
	if user == nil {
		return nil
	}
	return &access.ScopeUser{ID: user.ID, Username: user.Username, Role: user.Role}
}

// bodyValue is req.body[key] as a parsed JSON value. defined is false
// when the body does not have the key.
func bodyValue(r *http.Request, key string) (value any, defined bool) {
	raw := kit.Body(r).Get(key).Raw()
	if raw == nil {
		return nil, false
	}
	value, err := goapi.Parse(raw)
	if err != nil {
		return nil, false
	}
	return value, true
}

// list answers GET /api/guest/access-links with the links that are still
// good.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	goapipath.ProxyToGo(h.api, w, r, http.MethodGet, goapipath.Fixed("/guest/access-links"))
}

// revoke answers DELETE /api/guest/access-links/{id}. A link that was
// taken back also ends the sessions that run on it.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	resp, err := h.api.Delete(r.Context(), "/guest/access-links/"+goapi.EncodeURIComponent(id))
	if err != nil {
		h.failed(w, r, err)
		return
	}
	h.guests.Revoke(id)
	goapipath.WriteJSON(w, r, statusOf(resp), resp.Data())
}

// statusOf is the API server's status, 200 when it has none.
func statusOf(resp *goapi.Response) int {
	if resp.Status == 0 {
		return http.StatusOK
	}
	return resp.Status
}

// failed answers a call that failed: the API server's status and body,
// or 500 and the error's text where there is no answer or no body.
func (h *handlers) failed(w http.ResponseWriter, r *http.Request, err error) {
	message := err.Error()
	if failed := goapi.AsError(err); failed != nil {
		message = failed.Message
	}
	status, data := goapipath.FailureOf(err, message)
	goapipath.WriteJSON(w, r, status, data)
}

// trim is text.trim(): without the white space JavaScript removes.
func trim(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		switch r {
		case 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}

// search is the request's query written out again
// (new URLSearchParams(req.query)), with a "?" in front when it is not
// empty.
func search(q *reqquery.Query) (string, error) {
	text, ok := q.Search()
	if !ok {
		return "", guestaccess.ErrNotText
	}
	if text == "" {
		return "", nil
	}
	return "?" + text, nil
}

// validate answers GET /api/guest/access-links/validate, which needs no
// session: the link's token is the credential. The whole query goes to
// the API server (token, peer_id).
func (h *handlers) validate(w http.ResponseWriter, r *http.Request) {
	goapipath.ProxyToGo(h.api, w, r, http.MethodGet, func() (string, error) {
		query, err := search(reqquery.Of(r))
		return "/guest/access-links/validate" + query, err
	})
}

// The answers to a token that cannot be turned into text, the same as
// the mesh desktop route gives.
var (
	badGuestToken = failure{Error: "Invalid guest token"}
	badShareToken = failure{Error: "Invalid share token"}
)

// token is the request's guest token. A token that cannot be turned into
// text is answered with 400 here, and ok is false.
func (h *handlers) token(w http.ResponseWriter, r *http.Request) (token string, ok bool) {
	token, err := h.guests.Token(r)
	if err != nil {
		kit.JSON(w, r, http.StatusBadRequest, badGuestToken)
		return "", false
	}
	return token, true
}

// peers answers GET /api/guest/access-links/peers: the devices a link
// reaches, for the guest page. The token comes from ?guest=, ?t= or the
// guest cookie; a good link is stored in the cookie.
func (h *handlers) peers(w http.ResponseWriter, r *http.Request) {
	token, ok := h.token(w, r)
	if !ok {
		return
	}
	if token == "" {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "token required"})
		return
	}
	resp, err := h.api.Get(r.Context(), "/guest/access-links/peers", goapi.Query("token", token))
	if err != nil {
		h.failed(w, r, err)
		return
	}
	data := resp.Data()
	if !goapi.Truthy(data) {
		data = goapi.NewObject()
	}
	if expires := goapi.Dig(data, "expires_at"); goapi.Truthy(goapi.Dig(data, "valid")) && goapi.Truthy(expires) {
		h.guests.SetCookie(w, token, expires)
	}
	goapipath.WriteJSON(w, r, statusOf(resp), data)
}

// desktop answers POST /api/guest/mesh/devices/{id}/desktop, which opens
// a desktop tunnel to a mesh agent for someone with a link: a guest link
// (?guest=, ?t= or the guest cookie) or a share link (?mesh_share=). The
// API server says whether the link is good for this device, and the
// request goes on with its query. With a guest link the query goes on
// without "t", with the checked token as "guest", so the API server
// checks the link again and writes the tunnel to its creator, and with
// view_only=1 when the link only allows watching.
func (h *handlers) desktop(w http.ResponseWriter, r *http.Request) {
	peerID := r.PathValue("id")
	guest, ok := h.token(w, r)
	if !ok {
		return
	}
	query := reqquery.Of(r)
	share := ""
	if value := query.Get("mesh_share"); value.Truthy() {
		text, ok := value.Text()
		if !ok {
			kit.JSON(w, r, http.StatusBadRequest, badShareToken)
			return
		}
		share = trim(text)
	}
	path := "/mesh/devices/" + goapi.EncodeURIComponent(peerID) + "/desktop"

	if guest != "" {
		grant, err := h.guests.AttachGrant(r.Context(), r, peerID)
		if err != nil {
			kit.JSON(w, r, http.StatusBadRequest, badGuestToken)
			return
		}
		if grant == nil || !guestaccess.PeerAllowed(grant.Data, peerID) {
			kit.JSON(w, r, http.StatusForbidden, failure{Error: "Invalid or expired guest link"})
			return
		}
		goapipath.ProxyToGo(h.api, w, r, http.MethodPost, func() (string, error) {
			query.Delete("t")
			query.Set("guest", guest)
			if grant.ViewOnly() {
				query.Set("view_only", "1")
			}
			text, err := search(query)
			return path + text, err
		})
		return
	}

	if share != "" {
		resp, err := h.api.Get(r.Context(), "/mesh/share/validate", goapi.Query("token", share), goapi.Query("peer_id", peerID))
		if err != nil || !goapi.Truthy(goapi.Dig(resp.Data(), "valid")) {
			kit.JSON(w, r, http.StatusForbidden, failure{Error: "Invalid or expired share link"})
			return
		}
		goapipath.ProxyToGo(h.api, w, r, http.MethodPost, func() (string, error) {
			text, err := search(query)
			return path + text, err
		})
		return
	}

	kit.JSON(w, r, http.StatusUnauthorized, failure{Error: "guest token or mesh_share required"})
}
