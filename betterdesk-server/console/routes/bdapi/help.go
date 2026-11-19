package bdapi

import (
	"context"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// Help requests live on the API server. A device raises one here (newer
// agents do it over CDAP); the desktop client in operator mode lists,
// accepts and resolves them. The API server publishes each change on its
// event bus, which package statuspush passes to the browsers and the
// help request mail job reads; nothing here adds to that.

// helpItem is a help request as the lists show it, with its time to sort
// by.
type helpItem struct {
	obj     *goapi.Object
	id      string
	created float64
}

// helpStatus is the status the device API shows: "accepted" for the API
// server's "acknowledged", any other status as it is, "pending" for none.
func helpStatus(status any) any {
	if status == "acknowledged" {
		return "accepted"
	}
	return orValue(status, "pending")
}

// helpRequestID is the id of a help request as text: the API server's id
// when it is text or a number, else "".
func helpRequestID(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return jsbind.NumberString(t)
	}
	return ""
}

// normalizeHelpRequest is the route file's normalizeHelpRequest for one
// entry of the API server's list; nil for an entry that is no object.
// now is the time of the request, which a request without a readable
// creation time gets.
func normalizeHelpRequest(v any, now float64) (*helpItem, error) {
	switch v.(type) {
	case *goapi.Object, []any:
	default:
		return nil, nil
	}
	if o, ok := v.(*goapi.Object); ok && o == nil {
		return nil, nil
	}
	get := func(name string) any { return jsreq.Prop(v, name) }
	id := helpRequestID(get("id"))
	created := now
	if c := get("created_at"); goapi.Truthy(c) {
		s, err := text(c)
		if err != nil {
			return nil, err
		}
		if ms, ok := jsreq.ParseDate(s, time.Local); ok {
			created = float64(ms)
		}
	}
	item := goapi.NewObject(
		"id", id,
		"device_id", orValue(get("device_id"), ""),
		"hostname", orValue(get("hostname"), ""),
		"message", orValue(get("message"), ""),
	)
	status := get("status")
	item.Set("status", helpStatus(status))
	handledBy := orValue(get("handled_by"), "")
	acceptedBy, resolvedBy := any(""), any("")
	switch status {
	case "acknowledged":
		acceptedBy = handledBy
	case "resolved":
		resolvedBy = handledBy
	}
	item.Set("accepted_by", acceptedBy)
	item.Set("resolved_by", resolvedBy)
	item.Set("created_at", created)
	return &helpItem{obj: item, id: id, created: created}, nil
}

// errNotList is the TypeError of (result.data || []).map on data that is
// truthy but no list.
type notList struct{}

func (notList) Error() string { return "(result.data || []).map is not a function" }

var errNotList error = notList{}

// helpList asks the API server for help requests and returns them
// normalized, newest first. listed is false when the API server refused;
// a list the old code could not read is an error.
func (h *handlers) helpList(ctx context.Context, filter *goapi.Object) (items []*helpItem, listed bool, err error) {
	result := h.d.GoAPI.ListHelpRequests(ctx, filter)
	if !result.Bool("success") {
		return nil, false, nil
	}
	var list []any
	switch data := result.Get("data").(type) {
	case []any:
		list = data
	default:
		if goapi.Truthy(data) {
			return nil, true, errNotList
		}
	}
	now := float64(h.now().UnixMilli())
	for _, entry := range list {
		item, err := normalizeHelpRequest(entry, now)
		if err != nil {
			return nil, true, err
		}
		if item != nil {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].created > items[j].created })
	return items, true, nil
}

// helpRequest answers POST /api/bd/help-request {device_id, hostname,
// message} from a device: the request goes to the API server, and the
// console writes it into its audit log. The answer's request_id is the
// API server's id, or a random UUID when the API server gave none (it has
// no route to create one, BUGS RG12).
func (h *handlers) helpRequest(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	deviceID, ok := jsreq.Prop(body, "device_id").(string)
	if !ok || deviceID == "" {
		fail(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	hostname, err := textOr(jsreq.Prop(body, "hostname"), "")
	var message string
	if err == nil {
		message, err = textOr(jsreq.Prop(body, "message"), "")
	}
	if err != nil {
		h.serverError(w, r, "Help request error:", err, "Failed to process help request")
		return
	}
	device, hostname, message := cut(deviceID, 32), cut(hostname, 128), cut(message, 500)

	var requestID any
	res, err := h.d.GoAPI.Post(r.Context(), "/help/requests", goapi.Body(goapi.NewObject(
		"device_id", device, "hostname", hostname, "message", message,
	)))
	if err != nil {
		h.log.Warn("Help request Go proxy failed:", err)
	} else if data := res.Data(); goapi.Truthy(data) {
		requestID = orValue(jsreq.Prop(data, "id"), jsreq.Prop(data, "request_id"))
	}
	if err := h.d.Audit.Write(r.Context(), 0, "help_request", "Help requested by "+device+": "+message, clientIP(r)); err != nil {
		h.serverError(w, r, "Help request error:", err, "Failed to process help request")
		return
	}
	h.log.Info("Help request from " + device)
	id := ""
	if goapi.Truthy(requestID) {
		if id, err = text(requestID); err != nil {
			h.serverError(w, r, "Help request error:", err, "Failed to process help request")
			return
		}
	} else {
		id = randomUUID(h.random)
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "request_id", id))
}

// randomUUID is crypto.randomUUID(): a version 4 UUID in lower case.
func randomUUID(random io.Reader) string {
	b := make([]byte, 16)
	if _, err := io.ReadFull(random, b); err != nil {
		panic("bdapi: random source failed: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

// helpRequests answers GET /api/bd/help-requests?status=&device_id= for an
// operator: at most 200 requests of the API server, newest first. A list
// the API server refused is empty.
func (h *handlers) helpRequests(w http.ResponseWriter, r *http.Request) {
	filter := goapi.NewObject("limit", float64(200))
	for _, name := range []string{"status", "device_id"} {
		if v := jsreq.Query(r, name); goapi.Truthy(v) {
			s, err := text(v)
			if err != nil {
				h.serverError(w, r, "List help requests error:", err, "Failed to list help requests")
				return
			}
			filter.Set(name, s)
		}
	}
	items, listed, err := h.helpList(r.Context(), filter)
	if err != nil {
		h.serverError(w, r, "List help requests error:", err, "Failed to list help requests")
		return
	}
	if !listed {
		h.log.Warn("List help requests Go proxy failed")
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item.obj
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "requests", out))
}

// acceptHelp answers POST /api/bd/help-requests/{id}/accept.
func (h *handlers) acceptHelp(w http.ResponseWriter, r *http.Request) {
	h.helpAction(w, r, h.d.GoAPI.AcknowledgeHelpRequest, "help_request_accept", "Accepted help request ", "Failed to accept help request")
}

// resolveHelp answers POST /api/bd/help-requests/{id}/resolve.
func (h *handlers) resolveHelp(w http.ResponseWriter, r *http.Request) {
	h.helpAction(w, r, h.d.GoAPI.ResolveHelpRequest, "help_request_resolve", "Resolved help request ", "Failed to resolve help request")
}

// helpAction asks the API server to accept or resolve a request, 502 when
// it does not, and writes the operator's audit entry; a failure to write
// it is the route's 500.
func (h *handlers) helpAction(w http.ResponseWriter, r *http.Request, call func(context.Context, string) *goapi.Object, action, details, message string) {
	id := r.PathValue("id")
	result := call(r.Context(), id)
	if !result.Bool("success") {
		h.log.Warn(message+" (Go proxy):", jsreq.String(result.Get("error")))
		fail(w, r, http.StatusBadGateway, message)
		return
	}
	var userID int64
	if user := callerOf(r).user; user != nil {
		userID = user.ID.V
	}
	if err := h.d.Audit.Write(r.Context(), userID, action, details+id, clientIP(r)); err != nil {
		h.serverError(w, r, message+":", err, message)
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "request", result.Get("data")))
}

// deleteHelp answers DELETE /api/bd/help-requests/{id}. The API server
// cannot delete a help request; resolving it takes it off the active
// list.
func (h *handlers) deleteHelp(w http.ResponseWriter, r *http.Request) {
	result := h.d.GoAPI.ResolveHelpRequest(r.Context(), r.PathValue("id"))
	if !result.Bool("success") {
		h.log.Warn("Delete help request Go proxy failed:", jsreq.String(result.Get("error")))
		fail(w, r, http.StatusBadGateway, "Failed to delete help request")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}
