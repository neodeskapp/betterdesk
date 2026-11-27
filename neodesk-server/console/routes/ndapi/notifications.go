package ndapi

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/tickets"
)

// The notification bell of the web app: the help requests of the API
// server, for accounts that may approve them the registrations waiting
// for approval, and for accounts that may read them the tickets the
// computers opened and their replies (tickets.Notices), with a read mark
// per account (notification_reads). These routes take the session, not a
// token.

// isoTime is new Date(ms).toISOString().
func isoTime(ms float64) string {
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

// helpNotification is helpRequestToNotif.
func helpNotification(item *helpItem, read map[string]bool) *goapi.Object {
	obj := item.obj
	n := goapi.NewObject(
		"id", obj.Get("id"),
		"title", orValue(obj.Get("hostname"), obj.Get("device_id")),
		"message", orValue(obj.Get("message"), ""),
		"icon", "support_agent",
		"link", "/help-requests",
		"read", read[item.id],
		"created_at", isoTime(item.created),
		"kind", "help_request",
		"device_id", orValue(obj.Get("device_id"), ""),
		"hostname", orValue(obj.Get("hostname"), ""),
	)
	if obj.Has("status") {
		n.Set("status", obj.Get("status"))
	}
	return n
}

// registrationNotifications are the registrations waiting for approval,
// for an account with enrollment.approve, each marked read or not.
func (h *handlers) registrationNotifications(r *http.Request, read map[string]bool) []*goapi.Object {
	if !kit.HasPermission(r, "enrollment.approve") {
		return nil
	}
	items := h.regs.Notifications(r.Context())
	for _, item := range items {
		item.Set("read", read[jsreq.String(item.Get("id"))])
	}
	return items
}

// ticketNotification is a ticket a computer opened or its reply. The
// link opens the ticket on the tickets page.
func ticketNotification(n tickets.Notice, read map[string]bool) *goapi.Object {
	created := ""
	if at, ok := n.CreatedAt.Instant(); ok {
		created = isoTime(float64(at.UnixMilli()))
	}
	return goapi.NewObject(
		"id", n.ID,
		"title", n.Title,
		"message", n.Message,
		"icon", "confirmation_number",
		"link", "/tickets?ticket="+strconv.FormatInt(n.TicketID, 10),
		"read", read[n.ID],
		"created_at", created,
		"kind", n.Kind,
		"device_id", n.DeviceID,
		"hostname", "",
		"reporter", n.Reporter,
		"ticket_id", float64(n.TicketID),
	)
}

type notificationList struct {
	Success     bool            `json:"success"`
	Items       []*goapi.Object `json:"items"`
	UnreadCount int             `json:"unread_count"`
}

// notifications answers GET /api/nd/notifications?limit=&unread_only=:
// the newest notifications first, 20 unless limit says otherwise (1 to
// 50), only the unread ones with unread_only=true in any letter case, and
// the number of unread ones of all.
func (h *handlers) notifications(w http.ResponseWriter, r *http.Request) {
	limit := 20
	rawLimit := jsreq.Query(r, "limit")
	_, err := text(rawLimit)
	var unread string
	if err == nil {
		unread, err = textOr(jsreq.Query(r, "unread_only"), "")
	}
	if err != nil {
		h.serverError(w, r, "List notifications error:", err, "Failed to list notifications")
		return
	}
	if n, ok := jsreq.ParseInt(rawLimit, 10); ok {
		limit = int(min(max(n, 1), 50))
	}
	unreadOnly := jsbind.ToLower(unread) == "true"
	userID := kit.UserID(r)
	read, err := readNotificationIDs(r.Context(), h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "List notifications error:", err, "Failed to list notifications")
		return
	}
	help, _, err := h.helpList(r.Context(), goapi.NewObject("limit", float64(200)))
	if err != nil {
		h.serverError(w, r, "List notifications error:", err, "Failed to list notifications")
		return
	}
	type entry struct {
		obj *goapi.Object
		at  float64
	}
	var all []entry
	for _, item := range help {
		all = append(all, entry{obj: helpNotification(item, read), at: item.created})
	}
	for _, item := range h.registrationNotifications(r, read) {
		at, _ := jsreq.ParseDate(jsreq.String(item.Get("created_at")), time.Local)
		all = append(all, entry{obj: item, at: float64(at)})
	}
	ticketNotices, err := tickets.Notices(r, h.d)
	if err != nil {
		h.serverError(w, r, "List notifications error:", err, "Failed to list notifications")
		return
	}
	for _, n := range ticketNotices {
		at, _ := n.CreatedAt.Instant()
		all = append(all, entry{obj: ticketNotification(n, read), at: float64(at.UnixMilli())})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at > all[j].at })
	items := []*goapi.Object{}
	unreadCount := 0
	for _, e := range all {
		isRead := goapi.Truthy(e.obj.Get("read"))
		if !isRead {
			unreadCount++
		}
		if (!unreadOnly || !isRead) && len(items) < limit {
			items = append(items, e.obj)
		}
	}
	kit.JSON(w, r, http.StatusOK, notificationList{Success: true, Items: items, UnreadCount: unreadCount})
}

// markRead answers POST /api/nd/notifications/{id}/read: the id, cut to
// 128 UTF-16 units, is read for the account from now on, whether a
// notification has it or not.
func (h *handlers) markRead(w http.ResponseWriter, r *http.Request) {
	if err := markRead(r.Context(), h.d.Store, kit.UserID(r), []string{cut(r.PathValue("id"), 128)}); err != nil {
		h.serverError(w, r, "Mark notification read error:", err, "Failed to mark notification read")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// markAllRead answers POST /api/nd/notifications/read-all: every help
// request of the API server, every registration and every ticket notice
// the account sees is read for it.
func (h *handlers) markAllRead(w http.ResponseWriter, r *http.Request) {
	help, _, err := h.helpList(r.Context(), goapi.NewObject("limit", float64(200)))
	if err != nil {
		h.serverError(w, r, "Mark all read error:", err, "Failed to mark all notifications read")
		return
	}
	var ids []string
	for _, item := range help {
		ids = append(ids, item.id)
	}
	for _, item := range h.registrationNotifications(r, map[string]bool{}) {
		ids = append(ids, jsreq.String(item.Get("id")))
	}
	ticketNotices, err := tickets.Notices(r, h.d)
	if err != nil {
		h.serverError(w, r, "Mark all read error:", err, "Failed to mark all notifications read")
		return
	}
	for _, n := range ticketNotices {
		ids = append(ids, n.ID)
	}
	if err := markRead(r.Context(), h.d.Store, kit.UserID(r), ids); err != nil {
		h.serverError(w, r, "Mark all read error:", err, "Failed to mark all notifications read")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}
