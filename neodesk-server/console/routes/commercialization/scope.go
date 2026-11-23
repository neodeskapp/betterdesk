package commercialization

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// The API server answers the billing calls as the console's bridge, so it
// sees every device. For an account limited to some devices the console
// keeps to them itself:
//
//	the sessions list                 the sessions of devices in the scope
//	the work reports list             the reports of those sessions, looked up among
//	                                  the 500 newest sessions
//	the contracts list                the contracts of a device in the scope, of a
//	                                  device group or folder open to the account, and
//	                                  of an organization with a device in the scope
//	check, sessions/pending           403 for a device_id outside it, no call
//	a session's work report           403 unless the API server knows the session
//	                                  (GET /api/billing/sessions/{id}) and its device
//	                                  is in the scope, no call
//	the two exports                   the rows of the devices in the scope: the API
//	                                  server gets them as device_ids
//
// An account without a limit gets the API server's answers as they are.
// Without user.edit an account files no work report of a session whose
// operator is another account, with a limit or without (reportInScope).

// sessionsLookup is how many of the newest sessions are read to find
// sessions by id: the most the API server lists at once.
const sessionsLookup = "500"

// scope is the device scope of the account of a request, nil when it sees
// every device: read over the device layer's list, which knows the
// folders and tags of the devices, and only for an account with a limit,
// as the device routes do. A console without device groups has no limit.
func (h *handlers) scope(r *http.Request) (*access.DeviceScope, error) {
	if h.d.DeviceGroups == nil {
		return nil, nil
	}
	ctx, user := r.Context(), kit.User(r)
	scope, err := h.d.DeviceGroups.DeviceScopeForUser(ctx, user, nil)
	if err != nil || scope == nil {
		return scope, err
	}
	return h.d.DeviceGroups.DeviceScopeForUser(ctx, user, h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
}

// scoped answers a route with plain for an account without a limit, and
// with limited for one with a limit. A scope that cannot be read is 500.
func (h *handlers) scoped(plain http.HandlerFunc, limited func(w http.ResponseWriter, r *http.Request, scope *access.DeviceScope)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := h.scope(r)
		if err != nil {
			h.log.Error("Billing device scope error:", err)
			kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
			return
		}
		if scope.Unlimited() {
			plain(w, r)
			return
		}
		limited(w, r, scope)
	}
}

// outOfScope is the answer for a device the account may not see, as the
// device routes give it.
func outOfScope(w http.ResponseWriter, r *http.Request) {
	kit.Fail(w, r, http.StatusForbidden, kit.T(r, "errors.forbidden", nil))
}

// call asks the API server as ProxyToGo does and returns its answer to a
// call that succeeded. For any other outcome it answers as ProxyToGo and
// returns nil.
func (h *handlers) call(w http.ResponseWriter, r *http.Request, path goapipath.Path) *goapi.Response {
	target, err := path()
	var resp *goapi.Response
	if err == nil {
		resp, err = h.api.Request(r.Context(), http.MethodGet, target)
	}
	if err == nil {
		return resp
	}
	if goapipath.IsInvalid(err) {
		httpx.WriteJSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
		return nil
	}
	status, data := goapipath.FailureOf(err, "Go server unreachable")
	goapipath.WriteJSON(w, r, status, data)
	return nil
}

// keepMember cuts the list in member of an answer to the objects keep
// takes. An answer without such a list is left as it is.
func keepMember(data any, member string, keep func(item *goapi.Object) bool) {
	obj, ok := data.(*goapi.Object)
	if !ok {
		return
	}
	list, ok := obj.Get(member).([]any)
	if !ok {
		return
	}
	kept := []any{}
	for _, item := range list {
		if o, isObject := item.(*goapi.Object); isObject && keep(o) {
			kept = append(kept, item)
		}
	}
	obj.Set(member, kept)
}

// text is a member of an API answer as text, "" when it is none.
func text(o *goapi.Object, name string) string {
	s, _ := o.Get(name).(string)
	return s
}

// listInScope is withQuery for a list of the API server in member, cut
// for an account with a limit to the objects keep takes.
func (h *handlers) listInScope(pattern, member string, keep func(scope *access.DeviceScope, item *goapi.Object) bool) http.HandlerFunc {
	return h.scoped(h.withQuery(pattern), func(w http.ResponseWriter, r *http.Request, scope *access.DeviceScope) {
		resp := h.call(w, r, withSearch(goapipath.PathOf(r, pattern), search(r)))
		if resp == nil {
			return
		}
		data := resp.Data()
		keepMember(data, member, func(item *goapi.Object) bool { return keep(scope, item) })
		goapipath.WriteJSON(w, r, resp.Status, data)
	})
}

// sessionInScope keeps a session of a device in the scope.
func sessionInScope(scope *access.DeviceScope, session *goapi.Object) bool {
	return scope.Has(text(session, "device_id"))
}

// contractsInScope answers GET /api/panel/billing/contracts for an
// account with a limit: the contracts whose target is in its scope
// (contractTargets).
func (h *handlers) contractsInScope(w http.ResponseWriter, r *http.Request, scope *access.DeviceScope) {
	resp := h.call(w, r, withSearch(goapipath.Fixed("/billing/contracts"), search(r)))
	if resp == nil {
		return
	}
	data := resp.Data()
	targets := contractTargets{h: h, r: r, scope: scope, orgs: map[string]bool{}}
	keepMember(data, "contracts", targets.keep)
	if targets.err != nil {
		h.log.Error("Billing contract scope error:", targets.err)
		kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
		return
	}
	goapipath.WriteJSON(w, r, resp.Status, data)
}

// contractTargets decides which contracts an account with a limit sees:
// the contract of a device in its scope, of a device group open to it (as
// the device group list shows them), of a folder whose device group is
// open to it, and of an organization that holds a device of its scope. A
// target of another kind, or a device group that is gone, is left out.
// The first failed lookup is kept in err.
type contractTargets struct {
	h     *handlers
	r     *http.Request
	scope *access.DeviceScope
	// orgs holds the answer for each organization looked up
	orgs map[string]bool
	err  error
}

func (c *contractTargets) keep(contract *goapi.Object) bool {
	if c.err != nil {
		return false
	}
	key := text(contract, "target_key")
	var kept bool
	switch text(contract, "target_type") {
	case "device":
		return c.scope.Has(key)
	case "device_group":
		kept, c.err = c.groupOpen(key, false)
	case "folder":
		kept, c.err = c.groupOpen("folder_"+key, true)
	case "org":
		kept, c.err = c.orgInScope(key)
	}
	return kept && c.err == nil
}

// groupOpen reports whether the device group guid is open to the account.
// A folder without a device group has no access list and is open.
func (c *contractTargets) groupOpen(guid string, folder bool) (bool, error) {
	ctx := c.r.Context()
	group, err := c.h.d.DeviceGroups.DeviceGroupByGUID(ctx, guid)
	if err != nil || group == nil {
		return folder && err == nil, err
	}
	user := c.h.d.Access.UserAccessContext(ctx, devicegroups.ScopeUser(kit.User(c.r)))
	return access.GroupAllowedForUser(devicegroups.ScopeGroup(group), user), nil
}

// orgInScope reports whether the organization holds a device of the
// scope, by its device list on the API server.
func (c *contractTargets) orgInScope(id string) (bool, error) {
	if kept, seen := c.orgs[id]; seen {
		return kept, nil
	}
	resp, err := c.h.api.Request(c.r.Context(), http.MethodGet, "/org/"+goapi.EncodeURIComponent(id)+"/devices")
	if err != nil {
		if goapi.AsError(err).Status() == http.StatusNotFound {
			c.orgs[id] = false
			return false, nil
		}
		return false, err
	}
	kept := false
	keepMember(resp.Data(), "devices", func(device *goapi.Object) bool {
		kept = kept || c.scope.Has(text(device, "device_id"))
		return true
	})
	c.orgs[id] = kept
	return kept, nil
}

// sessionDevices reads the devices of the newest sessions by session id.
// It answers a failed call itself and returns nil.
func (h *handlers) sessionDevices(w http.ResponseWriter, r *http.Request) map[string]string {
	query := url.Values{"limit": {sessionsLookup}}
	resp := h.call(w, r, goapipath.Fixed("/billing/sessions?"+query.Encode()))
	if resp == nil {
		return nil
	}
	devices := map[string]string{}
	keepMember(resp.Data(), "sessions", func(session *goapi.Object) bool {
		devices[text(session, "id")] = text(session, "device_id")
		return true
	})
	return devices
}

// reportsInScope answers GET /api/panel/billing/reports for an account
// with a limit: the reports whose session is among the newest sessions
// and of a device in the scope.
func (h *handlers) reportsInScope(w http.ResponseWriter, r *http.Request, scope *access.DeviceScope) {
	devices := h.sessionDevices(w, r)
	if devices == nil {
		return
	}
	resp := h.call(w, r, withSearch(goapipath.Fixed("/billing/reports"), search(r)))
	if resp == nil {
		return
	}
	data := resp.Data()
	keepMember(data, "reports", func(report *goapi.Object) bool {
		device, found := devices[text(report, "session_id")]
		return found && scope.Has(device)
	})
	goapipath.WriteJSON(w, r, resp.Status, data)
}

// deviceInScope is withQuery for a route of one device, device_id in the
// query, refused for a device outside the scope of an account with a
// limit. The id is the one the API server reads from the query the console
// sends; without one the API server answers.
func (h *handlers) deviceInScope(pattern string) http.HandlerFunc {
	plain := h.withQuery(pattern)
	return h.scoped(plain, func(w http.ResponseWriter, r *http.Request, scope *access.DeviceScope) {
		query, _ := url.ParseQuery(strings.TrimPrefix(search(r), "?"))
		if id := strings.TrimSpace(query.Get("device_id")); id != "" && !scope.Has(id) {
			outOfScope(w, r)
			return
		}
		plain(w, r)
	})
}

// reportInScope is passBody for the work report of a session. An account
// without user.edit files no report of a session whose operator is
// another account (othersSession), and an account with a limit only that
// of a session of a device in its scope, which the API server has to
// know. The session is looked up only for those checks. Whether the
// session still takes a report is the API server's to answer.
func (h *handlers) reportInScope(pattern string) http.HandlerFunc {
	plain := h.passBody(http.MethodPost, pattern)
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := h.scope(r)
		if err != nil {
			h.log.Error("Billing device scope error:", err)
			kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
			return
		}
		forOthers := kit.HasPermission(r, "user.edit")
		if _, err := goapipath.PathOf(r, pattern)(); err != nil || scope.Unlimited() && forOthers {
			// a refused path is answered 400 before any call
			plain(w, r)
			return
		}
		session, err := h.api.GetBillingSession(r.Context(), r.PathValue("sessionId"))
		if err != nil {
			status, data := goapipath.FailureOf(err, "Go server unreachable")
			goapipath.WriteJSON(w, r, status, data)
			return
		}
		if !scope.Unlimited() && (session == nil || !scope.Has(session.DeviceID())) {
			outOfScope(w, r)
			return
		}
		if session != nil && !forOthers && !ownSession(r, session) {
			kit.Fail(w, r, http.StatusForbidden, kit.T(r, "errors.no_permission", nil))
			return
		}
		plain(w, r)
	}
}

// ownSession reports whether a billing session is the caller's: its
// operator_name (the console account the signal server recorded for a web
// client or a signed-in desktop client) or its operator_id is the caller's
// name. A session that names another account or none is not.
func ownSession(r *http.Request, session *goapi.BillingSession) bool {
	me := kit.User(r).Username
	return me != "" && slices.Contains([]string{text(session.Session, "operator_name"), text(session.Session, "operator_id")}, me)
}

// exportInScope is export. For an account with a limit the API server
// gets the devices of its scope as device_ids, in place of any the
// request names, and answers with their rows alone, as the lists keep to
// them.
func (h *handlers) exportInScope(pattern string) http.HandlerFunc {
	return h.scoped(h.export(pattern), func(w http.ResponseWriter, r *http.Request, scope *access.DeviceScope) {
		query := url.Values{"device_ids": {strings.Join(scope.IDs(), ",")}}.Encode()
		if own := withoutParameter(search(r), "device_ids"); own != "" {
			query = own + "&" + query
		}
		goapipath.ProxyBinaryToGo(h.api, w, r, http.MethodGet, withSearch(goapipath.PathOf(r, pattern), "?"+query))
	})
}

// withoutParameter is a written query (search, with its "?") without the
// parts of one parameter, and without the "?".
func withoutParameter(query, name string) string {
	var kept []string
	for _, part := range strings.Split(strings.TrimPrefix(query, "?"), "&") {
		key, _, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(key); part != "" && (err != nil || decoded != name) {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "&")
}
