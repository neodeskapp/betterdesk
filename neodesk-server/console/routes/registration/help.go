package registration

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The help requests devices raise, kept by the API server. The API
// server records who accepted or resolved one as the API key the console
// uses ("apikey:console-bridge"), so the list shows the account the
// console's own audit entry names instead.

const (
	helpAccepted = "help_request_accepted"
	helpResolved = "help_request_resolved"
)

// maxHelpActors is how many names the memory of help request handlers
// keeps; the oldest goes first, and the audit log answers for it.
const maxHelpActors = 500

// helpActors is the console's memory of who accepted or resolved which
// help request: action and request id to the account's name, "" for one
// nobody knows, at most maxHelpActors of them.
type helpActors struct {
	mu    sync.Mutex
	names map[string]string
	order []string
}

func newHelpActors() *helpActors {
	return &helpActors{names: map[string]string{}}
}

func (a *helpActors) get(key string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name, ok := a.names[key]
	return name, ok
}

func (a *helpActors) set(key, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, known := a.names[key]; !known {
		a.order = append(a.order, key)
		if len(a.order) > maxHelpActors {
			delete(a.names, a.order[0])
			a.order = a.order[1:]
		}
	}
	a.names[key] = name
}

// helpAuditDetails is the audit text of an accept or resolve: the id cut
// to 40 UTF-16 units.
func helpAuditDetails(action, id string) string {
	verb := "resolved"
	if action == helpAccepted {
		verb = "accepted"
	}
	return "Help request " + cut(id, 40) + " " + verb
}

// helpItem is a help request as the list shows it, with its time to sort
// by.
type helpItem struct {
	obj     *goapi.Object
	created float64
}

// normalizeHelpRequest is normalizeHelpRequest for one entry of the API
// server's list; nil for an entry that is no object. now is the time of
// the request, which a request without a readable creation time gets.
func normalizeHelpRequest(v any, now float64, loc *time.Location) (*helpItem, error) {
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
		if ms, ok := jsreq.ParseDate(s, loc); ok {
			created = float64(ms)
		}
	}
	var updated any
	if u := get("updated_at"); goapi.Truthy(u) {
		s, err := text(u)
		if err != nil {
			return nil, err
		}
		if ms, ok := jsreq.ParseDate(s, loc); ok {
			updated = float64(ms)
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
	item.Set("updated_at", updated)
	return &helpItem{obj: item, created: created}, nil
}

// helpStatus is the status the console shows: "accepted" for the API
// server's "acknowledged", any other status as it is, "pending" for none.
func helpStatus(status any) any {
	if status == "acknowledged" {
		return "accepted"
	}
	return orValue(status, "pending")
}

// helpActionID is the id of the request an accept or resolve was for, as
// the list shows it: the id in the API server's answer, else the
// address's id read as a whole number ("021" is 21), else the address's
// id as it is.
func helpActionID(pathID string, data any) string {
	if id := helpRequestID(jsreq.Prop(data, "id")); id != "" {
		return id
	}
	if n, err := strconv.ParseInt(pathID, 10, 64); err == nil {
		return strconv.FormatInt(n, 10)
	}
	return pathID
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

// helpRequests answers GET /api/help/requests?status=&device_id=: at
// most 200 requests of the API server, newest first, with the console's
// names for the ones the console accepted or resolved. A list the API
// server refused is empty.
func (h *handlers) helpRequests(w http.ResponseWriter, r *http.Request) {
	filter := goapi.NewObject("limit", float64(200))
	for _, name := range []string{"status", "device_id"} {
		if v := jsreq.Query(r, name); goapi.Truthy(v) {
			filter.Set(name, jsreq.String(v))
		}
	}
	result := h.d.GoAPI.ListHelpRequests(r.Context(), filter)
	if !result.Bool("success") {
		h.log.Warn("[help] List help requests Go proxy failed:", jsreq.String(result.Get("error")))
		kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "requests", []any{}))
		return
	}
	var list []any
	switch data := result.Get("data").(type) {
	case []any:
		list = data
	default:
		if goapi.Truthy(data) {
			h.serverError(w, r, "[help] List help requests error:", errNotList)
			return
		}
	}
	now := float64(h.now().UnixMilli())
	var items []*helpItem
	for _, entry := range list {
		item, err := normalizeHelpRequest(entry, now, time.Local)
		if err != nil {
			h.serverError(w, r, "[help] List help requests error:", err)
			return
		}
		if item != nil {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].created > items[j].created })
	out := make([]any, len(items))
	for i, item := range items {
		if err := h.nameHelpActors(r.Context(), item); err != nil {
			h.serverError(w, r, "[help] List help requests error:", err)
			return
		}
		out[i] = item.obj
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "requests", out))
}

type notList struct{}

func (notList) Error() string { return "result.data.map is not a function" }

var errNotList error = notList{}

// nameHelpActors replaces an accepted_by or resolved_by that is an API
// key ("apikey:...") with the console's name for it.
func (h *handlers) nameHelpActors(ctx context.Context, item *helpItem) error {
	id := jsreq.String(item.obj.Get("id"))
	for _, f := range []struct{ field, action string }{{"accepted_by", helpAccepted}, {"resolved_by", helpResolved}} {
		s, err := text(item.obj.Get(f.field))
		if err != nil {
			return err
		}
		if strings.HasPrefix(s, "apikey:") {
			item.obj.Set(f.field, h.helpActor(ctx, f.action, id, item.created))
		}
	}
	return nil
}

// helpActor is consoleHelpActor: the name the console remembers, else
// the account of the newest audit entry of the action for the request,
// unless that entry is more than a minute older than the request (an
// earlier request with the same id). The answer is remembered, "" too.
func (h *handlers) helpActor(ctx context.Context, action, id string, createdMs float64) string {
	key := action + ":" + id
	if name, ok := h.actors.get(key); ok {
		return name
	}
	name := ""
	entry, err := latestAuditEntry(ctx, h.d.Store, action, helpAuditDetails(action, id))
	if err == nil && entry != nil {
		at := auditTimeMs(entry, time.Local)
		if !(at < createdMs-60000) {
			name = entry.username
		}
	}
	h.actors.set(key, name)
	return name
}

// auditTimeMs is the time of an audit entry in milliseconds, NaN when it
// cannot be read: a PostgreSQL time as it is, SQLite text of the form
// "2026-11-10 07:32" and more as a time in UTC, other text as Date.parse
// reads it.
func auditTimeMs(e *auditEntry, loc *time.Location) float64 {
	if e.instant != nil {
		return float64(e.instant.UnixMilli())
	}
	s := e.text
	if sqliteClock.MatchString(s) {
		s = strings.Replace(s, " ", "T", 1) + "Z"
	}
	if ms, ok := jsreq.ParseDate(s, loc); ok {
		return float64(ms)
	}
	return math.NaN()
}

// helpAccept answers POST /api/help/requests/{id}/accept.
func (h *handlers) helpAccept(w http.ResponseWriter, r *http.Request) {
	h.helpAction(w, r, helpAccepted, h.d.GoAPI.AcknowledgeHelpRequest, "Failed to accept")
}

// helpResolve answers POST /api/help/requests/{id}/resolve.
func (h *handlers) helpResolve(w http.ResponseWriter, r *http.Request) {
	h.helpAction(w, r, helpResolved, h.d.GoAPI.ResolveHelpRequest, "Failed to resolve")
}

// helpRefusals are the failures of an accept or resolve that are about the
// request's id, with the status they answer: the API server's reasons for
// a request it does not have and for an id it cannot read, and the
// client's for an id it does not send. The client's answer holds the
// reason only, so the reason tells them apart.
var helpRefusals = map[string]int{
	"not found":         http.StatusNotFound,
	"invalid id":        http.StatusBadRequest,
	"Invalid requestId": http.StatusBadRequest,
}

// helpAction asks the API server to accept or resolve a request; 404 or
// 400 with the reason when the id names no request, 502 with its error
// when it fails otherwise. The console writes its own audit entry and
// remembers the account's name for the list, both under the request's id
// as the list shows it (helpActionID).
func (h *handlers) helpAction(w http.ResponseWriter, r *http.Request, action string, call func(context.Context, string) *goapi.Object, fallback string) {
	result := call(r.Context(), r.PathValue("id"))
	if !result.Bool("success") {
		status := http.StatusBadGateway
		if reason, isText := result.Get("error").(string); isText && helpRefusals[reason] != 0 {
			status = helpRefusals[reason]
		}
		failure(w, r, status, orValue(result.Get("error"), fallback))
		return
	}
	id := helpActionID(r.PathValue("id"), result.Get("data"))
	h.audit(r, action, helpAuditDetails(action, id))
	name := ""
	if user := kit.User(r); user != nil {
		name = user.Username
	}
	h.actors.set(action+":"+id, name)
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "request", result.Get("data")))
}
