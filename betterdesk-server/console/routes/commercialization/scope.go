package commercialization

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// The API server answers the billing calls as the console's bridge, so it
// sees every device. For an account limited to some devices the console
// keeps to them itself:
//
//	the sessions list                 the sessions of devices in the scope
//	the work reports list             the reports of those sessions, looked up among
//	                                  the 500 newest sessions
//	the contracts list                without the contracts of a device outside it
//	check, sessions/pending           403 for a device_id outside it, no call
//	a session's work report           403 unless the API server knows the session
//	                                  (GET /api/billing/sessions/{id}) and its device
//	                                  is in the scope, no call
//	the two exports                   403: the files cannot be cut to the scope
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

// contractInScope keeps the contracts of other targets than a device, and
// those of a device in the scope.
func contractInScope(scope *access.DeviceScope, contract *goapi.Object) bool {
	return text(contract, "target_type") != "device" || scope.Has(text(contract, "target_key"))
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
		if session != nil && !forOthers {
			other, err := h.othersSession(r, session)
			if err != nil {
				h.log.Error("Work report operator lookup error:", err)
				kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
				return
			}
			if other {
				kit.Fail(w, r, http.StatusForbidden, kit.T(r, "errors.no_permission", nil))
				return
			}
		}
		plain(w, r)
	}
}

// othersSession reports whether the operator of a billing session is
// another account than the caller's: its operator_name or operator_id is
// not the caller's name but the name of another account of the console.
// A session whose operator names no account (the console's web client, a
// desktop client's id) is nobody's in particular.
func (h *handlers) othersSession(r *http.Request, session *goapi.BillingSession) (bool, error) {
	me := kit.User(r).Username
	names := []string{text(session.Session, "operator_name"), text(session.Session, "operator_id")}
	if slices.Contains(names, me) {
		return false, nil
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if found, err := accountNamed(r.Context(), h.d.Store, name); err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// exportInScope is export, refused for an account with a limit.
func (h *handlers) exportInScope(pattern string) http.HandlerFunc {
	return h.scoped(h.export(pattern), func(w http.ResponseWriter, r *http.Request, _ *access.DeviceScope) {
		outOfScope(w, r)
	})
}
