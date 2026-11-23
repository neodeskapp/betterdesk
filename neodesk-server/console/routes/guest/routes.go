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
	"math"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/guestaccess"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/reqquery"
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

// deviceFunc is the device of an id as the device layer has it, with its
// folder and tags, nil when there is none (goapi.Backend.GetDeviceByID).
type deviceFunc func(ctx context.Context, id string) *goapi.Object

type handlers struct {
	api    *goapi.Client
	guests *guestaccess.Service
	scope  scopeFunc
	device deviceFunc
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	register(d, func(ctx context.Context, user *access.ScopeUser, devices []access.Device) (deviceScope, error) {
		return d.Access.DeviceScopeForUser(ctx, user, devices)
	}, func(ctx context.Context, id string) *goapi.Object {
		return d.Backend.GetDeviceByID(ctx, id, false)
	})
}

func register(d *kit.Deps, scope scopeFunc, device deviceFunc) {
	h := &handlers{api: d.GoAPI, guests: d.GuestAccess, scope: scope, device: device}
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
// text; ttl_minutes, label and max_uses are checked here (linkTypes). The
// account must have all of the devices in its device scope, which is
// computed over the devices with their folders and tags; for an account
// with a limit an id that is no device is outside. A view_only link is
// refused unless every device is a mesh or CDAP device (watchable): a
// RustDesk session cannot be kept to watching by the server.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	list, _ := body.Get("peer_ids").Array()
	if len(list) == 0 {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "peer_ids required"})
		return
	}
	peerIDs := make([]any, len(list))
	ids := make([]string, len(list))
	for i, item := range list {
		value, err := goapi.Parse(item.Raw())
		text, isText := guestaccess.JSText(value)
		if err != nil || !isText {
			kit.JSON(w, r, http.StatusInternalServerError, failure{Error: guestaccess.ErrNotText.Error()})
			return
		}
		peerIDs[i], ids[i] = text, text
	}
	ttl, hasTTL := bodyValue(r, "ttl_minutes")
	label, _ := bodyValue(r, "label")
	uses, _ := bodyValue(r, "max_uses")
	if problem := linkTypes(ttl, label, uses); problem != "" {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: problem})
		return
	}

	devices, missing := h.scopeDevices(r.Context(), ids)
	scope, err := h.scope(r.Context(), scopeUser(r), devices)
	if err != nil {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: err.Error()})
		return
	}
	if !scope.Unlimited() {
		for _, device := range devices {
			if missing[device.ID] || !scope.Has(device.ID) {
				kit.JSON(w, r, http.StatusForbidden, failure{Error: "One or more devices are outside your device scope"})
				return
			}
		}
	}

	viewOnly := body.Get("view_only").Truthy()
	if viewOnly {
		for _, id := range ids {
			if !watchable(h.device(r.Context(), id)) {
				kit.JSON(w, r, http.StatusBadRequest, failure{Error: viewOnlyRefused})
				return
			}
		}
	}

	link := goapi.NewObject("peer_ids", peerIDs)
	if hasTTL {
		link.Set("ttl_minutes", ttl)
	}
	link.Set("view_only", viewOnly)
	link.Set("label", "")
	if goapi.Truthy(label) {
		link.Set("label", label)
	}
	link.Set("max_uses", float64(0))
	if goapi.Truthy(uses) {
		link.Set("max_uses", uses)
	}
	goapipath.ProxyToGo(h.api, w, r, http.MethodPost, goapipath.Fixed("/guest/access-links"), goapipath.Body(link))
}

// viewOnlyRefused is the refusal of a view_only link for a device that is
// not watchable.
const viewOnlyRefused = "View-only links work for mesh and CDAP devices only: a RustDesk device cannot be limited to watching"

// cdapDeviceTypes are the device types a CDAP device registers with (the
// types of a CDAP manifest).
var cdapDeviceTypes = map[string]bool{"scada": true, "iot": true, "os_agent": true, "network": true, "camera": true, "desktop": true, "custom": true}

// watchable reports whether the server can keep a guest of the device to
// watching: a mesh agent, or a CDAP device by its type or its connection.
// The web viewer reaches any other device over a RustDesk session, where
// the input cannot be filtered; a device that is not known is not
// watchable either.
func watchable(device *goapi.Object) bool {
	if device == nil {
		return false
	}
	kind, _ := device.Get("device_type").(string)
	kind = strings.ToLower(strings.TrimSpace(kind))
	return kind == "mesh_agent" || cdapDeviceTypes[kind] || goapi.Truthy(device.Get("cdap_connected"))
}

// scopeDevices looks up the devices of ids for the device scope, each
// with its folder and tags under the id as it was asked. missing holds the
// ids that are no device.
func (h *handlers) scopeDevices(ctx context.Context, ids []string) (devices []access.Device, missing map[string]bool) {
	devices = make([]access.Device, len(ids))
	missing = map[string]bool{}
	for i, id := range ids {
		found := h.device(ctx, id)
		if found == nil {
			devices[i], missing[id] = access.Device{ID: id}, true
			continue
		}
		devices[i] = devicegroups.ScopeDevices([]*goapi.Object{found})[0]
		devices[i].ID = id
	}
	return devices, missing
}

// linkTypes checks the values of a new link the API server reads as
// numbers and text: ttl_minutes a whole number from 1, label a text,
// max_uses a whole number from 0, each also missing or null. It returns
// the message for the first value that is not, "" when all are good.
func linkTypes(ttl, label, uses any) string {
	if ttl != nil && !wholeNumber(ttl, 1) {
		return "Invalid ttl_minutes"
	}
	if _, isText := label.(string); label != nil && !isText {
		return "Invalid label"
	}
	if uses != nil && !wholeNumber(uses, 0) {
		return "Invalid max_uses"
	}
	return ""
}

// wholeNumber reports a number without a fraction from min to 2^53.
func wholeNumber(v any, min float64) bool {
	f, isNumber := v.(float64)
	return isNumber && f == math.Trunc(f) && f >= min && f <= 1<<53
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

// guestAdmin reports a role that sees and takes back the links of every
// account, the roles the API server's guest handlers count as guest
// admins.
func guestAdmin(role string) bool {
	return role == "admin" || role == "super_admin" || role == "global_admin"
}

// list answers GET /api/guest/access-links with the links that are still
// good. The console calls the API server as itself, which sees every
// link; for an account whose role is no guest admin the list keeps only
// the links whose created_by is the account's name.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	user := kit.User(r)
	if user == nil || guestAdmin(user.Role) {
		goapipath.ProxyToGo(h.api, w, r, http.MethodGet, goapipath.Fixed("/guest/access-links"))
		return
	}
	resp, err := h.api.Get(r.Context(), "/guest/access-links")
	if err != nil {
		h.failed(w, r, err)
		return
	}
	data := resp.Data()
	if body, ok := data.(*goapi.Object); ok && body != nil {
		if grants, ok := body.Get("grants").([]any); ok {
			mine := make([]any, 0, len(grants))
			for _, grant := range grants {
				if createdBy(grant) == user.Username {
					mine = append(mine, grant)
				}
			}
			body = body.Clone()
			body.Set("grants", mine)
			data = body
		}
	}
	goapipath.WriteJSON(w, r, statusOf(resp), data)
}

// createdBy is the created_by of a link in the API server's list, "" when
// it has none.
func createdBy(grant any) string {
	link, _ := grant.(*goapi.Object)
	if link == nil {
		return ""
	}
	name, _ := link.Get("created_by").(string)
	return name
}

// revoke answers DELETE /api/guest/access-links/{id}. A link that was
// taken back also ends the sessions that run on it. An account whose role
// is no guest admin may take back only the links it created: for another
// account's link the answer is 403 {"error":"forbidden"}, as the API
// server's own rule gives it, and nothing is deleted. A link the list does
// not hold goes on to the API server, which answers for it.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if user := kit.User(r); user != nil && !guestAdmin(user.Role) {
		list, err := h.api.Get(r.Context(), "/guest/access-links")
		if err != nil {
			h.failed(w, r, err)
			return
		}
		grants, _ := goapi.Dig(list.Data(), "grants").([]any)
		for _, grant := range grants {
			link, _ := grant.(*goapi.Object)
			if link == nil || link.Get("id") != strings.TrimSpace(id) {
				continue
			}
			if createdBy(grant) != user.Username {
				kit.JSON(w, r, http.StatusForbidden, failure{Error: "forbidden"})
				return
			}
		}
	}
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
