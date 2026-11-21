// Package tickets holds the helpdesk routes under /api/tickets: tickets,
// their comments and their attachments.
//
// Reading needs the permission device.view, changing a ticket or
// commenting on it device.edit. A viewer sees the tickets assigned to it,
// and an account whose device scope is limited the tickets of the devices
// in its scope and the tickets of no device (mayRead), with their
// comments and attachments. Internal comments are for accounts with
// device.edit.
//
// The two routes for devices, POST and GET /api/tickets/bd, take a device
// token instead of a session (device.go).
package tickets

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

type handlers struct {
	d   *kit.Deps
	db  *store.DB
	log *kit.Logger
	// uploads is the folder of the attachment files.
	uploads string
	now     func() time.Time
	// random fills b with random bytes, for the names of stored files.
	random func(b []byte) (int, error)
}

// Register adds the group's routes below the mount (/api/tickets).
func Register(d *kit.Deps) { register(d, newHandlers(d)) }

func register(d *kit.Deps, h *handlers) {
	r := d.Router
	canRead := d.RequirePermission("device.view")
	canWrite := d.RequirePermission("device.edit")
	upload, files := d.Limits.Upload.Middleware, d.Limits.FileAccess.Middleware
	id, aid := kit.Digits("id"), kit.Digits("aid")
	attachmentBody := middleware.AttachmentBody(func(w http.ResponseWriter, r *http.Request, status int, err error) {
		d.WebUI.ServerError(w, r, err, status)
	})

	r.Get("/", h.list, requireAuth, canRead)
	r.Get("/stats", h.stats, requireAuth, canRead)
	r.Post("/", h.create, upload, requireAuth, canWrite)
	r.Get("/{id}", h.get, id, requireAuth, canRead)
	r.Patch("/{id}", h.update, id, requireAuth, canWrite)
	r.Delete("/{id}", h.remove, id, upload, requireAuth, canWrite)
	r.Post("/{id}/comments", h.addComment, id, upload, requireAuth, canWrite)
	r.Get("/{id}/comments", h.comments, id, requireAuth, canRead)
	// The body of an upload is read by this route, after the checks: up
	// to 35 MB of JSON for a file of 25 MB
	r.Post("/{id}/attachments", h.attach, id, upload, requireAuth, canWrite, attachmentBody)
	r.Get("/{id}/attachments", h.attachments, id, files, requireAuth, canRead)
	r.Get("/attachments/{aid}", h.download, aid, files, requireAuth, canRead)

	r.Post("/bd", h.createFromAgent, d.RequireDeviceToken)
	r.Get("/bd", h.listForAgent, d.RequireDeviceToken)
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// serverError answers what every route's catch block answered.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what+":", err)
	fail(w, r, http.StatusInternalServerError, "Internal server error")
}

type done struct {
	Success bool `json:"success"`
}

// requireAuth lets a signed-in account through and answers 401 otherwise.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kit.User(r) == nil {
			fail(w, r, http.StatusUnauthorized, "Authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The values a ticket's status, priority and category can have.
var (
	validStatuses   = []string{"open", "in_progress", "resolved", "closed"}
	validPriorities = []string{"low", "medium", "high", "critical"}
	validCategories = []string{"general", "hardware", "software", "network", "access", "other"}
)

// oneOf reports whether v is one of the texts.
func oneOf(v jsbind.Value, texts []string) bool {
	if !v.IsString() {
		return false
	}
	for _, text := range texts {
		if v.Str() == text {
			return true
		}
	}
	return false
}

// slaHours is the time a ticket of each priority may take.
var slaHours = map[string]int{"critical": 4, "high": 8, "medium": 24, "low": 72}

// slaDue is the time a ticket of the priority is due, as the text the
// route stored: the hours are added to the local clock reading, and the
// result is written in UTC with milliseconds.
func (h *handlers) slaDue(priority string) string {
	hours, known := slaHours[priority]
	if !known {
		hours = slaHours["medium"]
	}
	now := h.now().In(time.Local)
	due := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+hours, now.Minute(), now.Second(), now.Nanosecond(), time.Local)
	return store.ISOTime(due)
}

// maxID is the largest id an id column holds: SERIAL on PostgreSQL.
const maxID = 1<<31 - 1

// ticketID is +req.params[name]: the digits of the path as a number. ok is
// false for a number beyond maxID, which names no row on either database.
func ticketID(r *http.Request, name string) (id jsbind.Value, ok bool) {
	n := jsbind.ParseNumber(r.PathValue(name))
	if n > maxID {
		return jsbind.Undefined, false
	}
	return jsbind.Number(n), true
}

// ticketOf reads the ticket the address names; nil when there is none
// or the account may not read it.
func (h *handlers) ticketOf(r *http.Request) (*ticket, error) {
	id, ok := ticketID(r, "id")
	if !ok {
		return nil, nil
	}
	return h.readable(r, id)
}

// readable is the ticket with the id when the account may read it, nil
// when there is none or it may not.
func (h *handlers) readable(r *http.Request, id jsbind.Value) (*ticket, error) {
	t, err := ticketByID(r.Context(), h.db, id)
	if err != nil || t == nil {
		return nil, err
	}
	scope, err := h.deviceScope(r)
	if err != nil || !mayRead(r, scope, t) {
		return nil, err
	}
	return t, nil
}

// deviceScope is the device scope of the request's account, nil when it
// sees every device: whether there is a limit is decided first, and only
// for an account that has one is the scope read over every device of the
// device layer, deleted ones included.
func (h *handlers) deviceScope(r *http.Request) (*access.DeviceScope, error) {
	ctx := r.Context()
	return h.d.DeviceGroups.LimitedScope(ctx, kit.User(r), func() []*goapi.Object {
		return h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{IncludeDeleted: true})
	})
}

// mayRead reports whether the account may read the ticket, its comments
// and its attachments, and change it: a viewer only the tickets assigned
// to it, an account whose device scope is limited only the tickets of a
// device in its scope and those of no device.
func mayRead(r *http.Request, scope *access.DeviceScope, t *ticket) bool {
	user := kit.User(r)
	if user.Role == "viewer" && (!t.AssignedTo.Valid || t.AssignedTo.String != user.Username) {
		return false
	}
	return deviceInScope(scope, t.DeviceID.String)
}

// deviceInScope reports whether a ticket's device is in the scope: no
// device ("" or null) always is.
func deviceInScope(scope *access.DeviceScope, device string) bool {
	return device == "" || scope.Has(device)
}

// number is `${n}` for a row id.
func number(id jsbind.Cell) string { return id.Value().ToString() }

// audit writes an audit log entry for the signed-in account. A failure
// is ignored.
func (h *handlers) audit(r *http.Request, action, details string) {
	_ = h.d.Audit.Write(r.Context(), kit.User(r).ID, action, details, kit.ClientIP(r))
}

type ticketList struct {
	Tickets []ticket `json:"tickets"`
	Total   int      `json:"total"`
}

// filterKeys are the query parameters that filter the list, each on the
// column of its name.
var filterKeys = []string{"status", "priority", "category", "assigned_to", "device_id"}

// list answers GET /api/tickets?status=&priority=&category=&assigned_to=
// &device_id=&search=. A viewer gets the tickets assigned to it, whatever
// assigned_to says, and an account whose device scope is limited those of
// its devices and of no device. A filter is one text: given twice, in
// brackets or as an object it is refused.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	user := kit.User(r)
	var filters []filter
	for _, key := range filterKeys {
		value := jsbind.Query(r.URL.RawQuery, key)
		if key == "assigned_to" && user.Role == "viewer" {
			value = jsbind.String(user.Username)
		}
		if !value.IsUndefined() && !value.IsString() {
			fail(w, r, http.StatusBadRequest, "Invalid "+key)
			return
		}
		if value.Truthy() {
			filters = append(filters, filter{key, value})
		}
	}
	search := jsbind.Query(r.URL.RawQuery, "search")
	if !search.Truthy() {
		search = jsbind.Undefined
	}
	scope, err := h.deviceScope(r)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	tickets, err := listTickets(r.Context(), h.db, filters, search, scope)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, ticketList{Tickets: tickets, Total: len(tickets)})
}

// stats answers GET /api/tickets/stats over the tickets the account may
// read: a viewer's counts take the tickets assigned to it, a limited
// account's those of its devices and of no device.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	assignee := jsbind.Undefined
	if user := kit.User(r); user.Role == "viewer" {
		assignee = jsbind.String(user.Username)
	}
	scope, err := h.deviceScope(r)
	if err != nil {
		h.serverError(w, r, "Stats error", err)
		return
	}
	stats, err := countTickets(r.Context(), h.db, assignee, scope)
	if err != nil {
		h.serverError(w, r, "Stats error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, stats)
}

// text reads a body member that has to be text with something in it, a
// title or a comment: the trimmed text, and whether there is one. A value
// that is no text has none.
func text(v jsbind.Value) (trimmed string, ok bool) {
	if !v.IsString() {
		return "", false
	}
	trimmed = jsbind.Trim(v.Str())
	return trimmed, trimmed != ""
}

// optionalText reports whether a body member is text, null or missing.
func optionalText(v jsbind.Value) bool {
	return v.IsString() || v.IsNull() || v.IsUndefined()
}

type created struct {
	Success bool          `json:"success"`
	Ticket  createdTicket `json:"ticket"`
}

type createdTicket struct {
	ID    jsbind.Cell `json:"id"`
	Title string      `json:"title"`
}

// create answers POST /api/tickets with the body
// {title, description?, priority?, category?, device_id?, assigned_to?}.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	rawTitle := jsbind.Of(body.Get("title"))
	title, ok := text(rawTitle)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Title is required")
		return
	}
	priority, category := jsbind.Of(body.Get("priority")), jsbind.Of(body.Get("category"))
	if priority.Truthy() && !oneOf(priority, validPriorities) {
		fail(w, r, http.StatusBadRequest, "Invalid priority. Valid: "+strings.Join(validPriorities, ", "))
		return
	}
	if category.Truthy() && !oneOf(category, validCategories) {
		fail(w, r, http.StatusBadRequest, "Invalid category. Valid: "+strings.Join(validCategories, ", "))
		return
	}
	if !h.textMembers(w, r, "description", "device_id", "assigned_to") {
		return
	}
	if !h.deviceAllowed(w, r, jsbind.Of(body.Get("device_id"))) {
		return
	}
	user := kit.User(r)
	effective := priority.Or(jsbind.String("medium"))
	id, err := createTicket(r.Context(), h.db, newTicket{
		Title:       jsbind.String(title),
		Description: jsbind.Of(body.Get("description")).Or(jsbind.String("")),
		Priority:    effective,
		Category:    category.Or(jsbind.String("general")),
		DeviceID:    jsbind.Of(body.Get("device_id")).Or(jsbind.Null),
		CreatedBy:   jsbind.String(user.Username),
		AssignedTo:  jsbind.Of(body.Get("assigned_to")).Or(jsbind.Null),
		SLADueAt:    jsbind.String(h.slaDue(effective.Str())),
	})
	if err != nil {
		h.serverError(w, r, "Create error", err)
		return
	}
	h.audit(r, "ticket_created", "Ticket #"+number(id)+": "+rawTitle.Str())
	h.log.Info("#" + number(id) + " created by " + user.Username + ": " + rawTitle.Str())
	kit.JSON(w, r, http.StatusCreated, created{Success: true, Ticket: createdTicket{ID: id, Title: title}})
}

// deviceAllowed checks the device a ticket is written for: none, or one
// in the account's device scope. It answers 403 for another and 500 for a
// failed scope check, and then reports false.
func (h *handlers) deviceAllowed(w http.ResponseWriter, r *http.Request, device jsbind.Value) bool {
	if !device.IsString() {
		return true
	}
	scope, err := h.deviceScope(r)
	if err != nil {
		h.serverError(w, r, "Device scope error", err)
		return false
	}
	if !deviceInScope(scope, jsbind.Trim(device.Str())) {
		fail(w, r, http.StatusForbidden, "Device is outside your device scope")
		return false
	}
	return true
}

// textMembers checks that the members of the body are text, null or
// missing, and answers 400 naming the first that is not.
func (h *handlers) textMembers(w http.ResponseWriter, r *http.Request, members ...string) bool {
	for _, member := range members {
		if !optionalText(jsbind.Of(kit.Body(r).Get(member))) {
			fail(w, r, http.StatusBadRequest, "Invalid "+member)
			return false
		}
	}
	return true
}

// dueTime reads the time a ticket is due as the route stores it: an RFC
// 3339 time, written in UTC with milliseconds.
func dueTime(v jsbind.Value) (string, bool) {
	if !v.IsString() {
		return "", false
	}
	t, err := time.Parse(time.RFC3339Nano, v.Str())
	if err != nil {
		return "", false
	}
	return store.ISOTime(t), true
}

type ticketDetail struct {
	ticket
	Comments    []comment    `json:"comments"`
	Attachments []attachment `json:"attachments"`
}

// visible drops the internal comments for an account without
// device.edit.
func visible(r *http.Request, comments []comment) []comment {
	if kit.HasPermission(r, "device.edit") {
		return comments
	}
	shown := []comment{}
	for _, c := range comments {
		if !c.IsInternal.True() {
			shown = append(shown, c)
		}
	}
	return shown
}

// get answers GET /api/tickets/{id}: the ticket with its comments and
// attachments.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	t, err := h.ticketOf(r)
	if err == nil && t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	var comments []comment
	var attachments []attachment
	if err == nil {
		comments, err = ticketComments(r.Context(), h.db, t.id())
	}
	if err == nil {
		attachments, err = ticketAttachments(r.Context(), h.db, t.id())
	}
	if err != nil {
		h.serverError(w, r, "Detail error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, ticketDetail{ticket: *t, Comments: visible(r, comments), Attachments: attachments})
}

// update answers PATCH /api/tickets/{id}: every member of the body that
// names one of title, description, status, priority, category,
// assigned_to, sla_due_at and device_id is written, each checked: the
// title is text that is not empty, description and assigned_to are text
// or null, status, priority and category are each one of the known ones,
// device_id is a device id, empty or null, and sla_due_at a time or null.
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	t, err := h.ticketOf(r)
	if err != nil {
		h.serverError(w, r, "Update error", err)
		return
	}
	if t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	body := kit.Body(r)
	status, priority, deviceID := jsbind.Of(body.Get("status")), jsbind.Of(body.Get("priority")), jsbind.Of(body.Get("device_id"))
	if body.Has("status") && !oneOf(status, validStatuses) {
		fail(w, r, http.StatusBadRequest, "Invalid status. Valid: "+strings.Join(validStatuses, ", "))
		return
	}
	if body.Has("priority") && !oneOf(priority, validPriorities) {
		fail(w, r, http.StatusBadRequest, "Invalid priority. Valid: "+strings.Join(validPriorities, ", "))
		return
	}
	// The device a ticket is about: a device id, or empty or null for none
	if !deviceID.IsUndefined() && !deviceID.IsNull() && (!deviceID.IsString() || utf16Length(jsbind.Trim(deviceID.Str())) > 64) {
		fail(w, r, http.StatusBadRequest, "Invalid device_id")
		return
	}
	// A ticket keeps a title
	if _, ok := text(jsbind.Of(body.Get("title"))); body.Has("title") && !ok {
		fail(w, r, http.StatusBadRequest, "Title is required")
		return
	}
	if !h.textMembers(w, r, "description", "assigned_to") {
		return
	}
	if !h.deviceAllowed(w, r, deviceID) {
		return
	}
	if body.Has("category") && !oneOf(jsbind.Of(body.Get("category")), validCategories) {
		fail(w, r, http.StatusBadRequest, "Invalid category. Valid: "+strings.Join(validCategories, ", "))
		return
	}
	// The time a ticket is due, or null for none
	if due := jsbind.Of(body.Get("sla_due_at")); body.Has("sla_due_at") && !due.IsNull() {
		if _, ok := dueTime(due); !ok {
			fail(w, r, http.StatusBadRequest, "Invalid sla_due_at")
			return
		}
	}

	var changes []change
	for _, column := range []string{"title", "description", "status", "priority", "category", "assigned_to", "sla_due_at", "device_id"} {
		if !body.Has(column) {
			continue
		}
		value := jsbind.Of(body.Get(column))
		switch column {
		case "title":
			value = jsbind.String(jsbind.Trim(value.Str()))
		case "sla_due_at":
			if due, ok := dueTime(value); ok {
				value = jsbind.String(due)
			}
		case "device_id":
			if trimmed := jsbind.Trim(value.Str()); trimmed != "" {
				value = jsbind.String(trimmed)
			} else {
				value = jsbind.Null
			}
		}
		changes = append(changes, change{column, value})
	}
	if err := updateTicket(r.Context(), h.db, t.id(), changes); err != nil {
		h.serverError(w, r, "Update error", err)
		return
	}
	columns := make([]string, len(changes))
	for i, c := range changes {
		columns[i] = c.column
	}
	h.audit(r, "ticket_updated", "Ticket #"+number(t.ID)+" updated: "+strings.Join(columns, ", "))
	h.log.Info("#" + number(t.ID) + " updated by " + kit.User(r).Username)
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// utf16Length is s.length in JavaScript: the number of UTF-16 units.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// remove answers DELETE /api/tickets/{id}: the ticket goes with its
// comments, its attachments and their files.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, err := h.ticketOf(r)
	if err == nil && t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	var attachments []attachment
	if err == nil {
		attachments, err = ticketAttachments(r.Context(), h.db, t.id())
	}
	if err == nil {
		for _, a := range attachments {
			h.removeFile(a.StoragePath)
		}
		err = deleteTicket(r.Context(), h.db, t.id())
	}
	if err != nil {
		h.serverError(w, r, "Delete error", err)
		return
	}
	h.audit(r, "ticket_deleted", "Ticket #"+number(t.ID)+": "+t.Title)
	h.log.Info("#" + number(t.ID) + " deleted by " + kit.User(r).Username)
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// uploadsDir is the folder of the attachment files: data/attachments in
// the console's folder, where they always were. A console that has no
// such folder keeps them in its data folder.
func uploadsDir(cfg *config.Config) string {
	if cfg.ConsoleDir != "" {
		return filepath.Join(cfg.ConsoleDir, "data", "attachments")
	}
	return filepath.Join(cfg.DataDir, "attachments")
}

func newHandlers(d *kit.Deps) *handlers {
	h := &handlers{
		d:       d,
		db:      d.Store,
		log:     d.Log.Child("Tickets"),
		uploads: uploadsDir(d.Config),
		now:     time.Now,
		random:  randomBytes,
	}
	h.makeUploads()
	return h
}
