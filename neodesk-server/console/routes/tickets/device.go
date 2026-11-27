package tickets

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The routes of the app on the computer (the receiver, and the desktop
// agent before it), which takes its device token instead of a session.
// All are below the mount /api/tickets, and the server's API port hands
// them to the console as it does the computer's chat (api/device_chat.go).
// A write with the device token and without a session passes the CSRF
// gate without a CSRF token; with a session it needs one like any other.
//
// A computer sees the tickets it opened itself (source "device") and
// nothing else: a ticket of another computer, and a ticket staff opened
// about this one, answer 404 as a ticket that is not there. Internal
// comments never reach it, and of the files it sees its own and those of
// staff's public comments.
//
//	GET  /api/tickets/nd                          its tickets
//	POST /api/tickets/nd                          open one
//	GET  /api/tickets/nd/{id}                     one, with comments and files
//	POST /api/tickets/nd/{id}/comments            reply
//	GET  /api/tickets/nd/{id}/attachments/{aid}   a file
//	POST /api/tickets/nd/{id}/read                mark it read

// The limits of what a computer sends with a ticket or a reply.
const (
	// maxDeviceFiles is the most files one request carries, the
	// screenshot included.
	maxDeviceFiles = 10
	// maxReporter is the longest name of the person at the computer and
	// the longest system name, in characters.
	maxReporter = 200
	// maxDerivedTitle is the longest title made from a description.
	maxDerivedTitle = 80
	// computerInfo is the name of the file with what the server knows
	// about the computer, attached to every ticket the computer opens.
	computerInfo = "computer-info.json"
)

// deviceTicket is a ticket as the computer gets it: the ticket's row with
// the time of the last public comment, whether staff wrote something it
// has not read, and the number of public comments.
type deviceTicket struct {
	ticket
	LastReplyAt store.Time `json:"last_reply_at"`
	Unread      bool       `json:"unread"`
	Replies     int64      `json:"replies"`
}

type agentCreated struct {
	Success  bool         `json:"success"`
	TicketID jsbind.Cell  `json:"ticket_id"`
	Ticket   deviceTicket `json:"ticket"`
}

type deviceTicketList struct {
	Tickets []deviceTicket `json:"tickets"`
	Total   int            `json:"total"`
}

// deviceComment is a public comment as the computer gets it. From is
// "staff" or "device"; AuthorName the staff member's name, or the
// reporter's for the computer's own.
type deviceComment struct {
	ID         jsbind.Cell `json:"id"`
	AuthorName string      `json:"author_name"`
	From       string      `json:"from"`
	Body       string      `json:"body"`
	CreatedAt  store.Time  `json:"created_at"`
}

// deviceFile is a file of a ticket as the computer gets it.
type deviceFile struct {
	ID        jsbind.Cell `json:"id"`
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Size      jsbind.Cell `json:"size"`
	CommentID jsbind.Cell `json:"comment_id"`
}

type deviceDetail struct {
	Ticket      deviceTicket    `json:"ticket"`
	Comments    []deviceComment `json:"comments"`
	Attachments []deviceFile    `json:"attachments"`
}

type deviceCommented struct {
	Success bool          `json:"success"`
	Comment deviceComment `json:"comment"`
}

// agentName is the author of what a computer writes.
func agentName(device string) string { return "agent:" + device }

// upload is one file a computer sends: {name, type, data_b64}.
type upload struct {
	name, mimetype string
	data           []byte
}

// readUploads reads the screenshot (one file, or null) and the files (a
// list) of a body. It answers 400 for a file that is no {name, data_b64}
// with base64 data, and 413 when the files hold more than 25 MB together,
// and then reports false.
func readUploads(w http.ResponseWriter, r *http.Request, screenshot, files jsbind.Value) ([]upload, bool) {
	var items []jsbind.Value
	if !screenshot.IsUndefined() && !screenshot.IsNull() {
		items = append(items, screenshot)
	}
	switch {
	case files.IsArray():
		items = append(items, files.Items()...)
	case !files.IsUndefined() && !files.IsNull():
		fail(w, r, http.StatusBadRequest, "Invalid files")
		return nil, false
	}
	if len(items) > maxDeviceFiles {
		fail(w, r, http.StatusBadRequest, "Too many files")
		return nil, false
	}
	var out []upload
	total := 0
	for _, item := range items {
		name, mimetype := item.Member("name"), item.Member("type")
		if !item.IsObject() || !name.IsString() || jsbind.Trim(name.Str()) == "" || !optionalText(mimetype) {
			fail(w, r, http.StatusBadRequest, "Invalid file")
			return nil, false
		}
		data, err := fileBytes(item.Member("data_b64"))
		if err != nil {
			fail(w, r, http.StatusBadRequest, "Invalid file")
			return nil, false
		}
		if total += len(data); total > maxAttachmentSize {
			fail(w, r, http.StatusRequestEntityTooLarge, "Attachments too large. Max 25 MB")
			return nil, false
		}
		out = append(out, upload{name: safeName(jsbind.Trim(name.Str())), mimetype: mimetype.Str(), data: data})
	}
	return out, true
}

// shortText reads an optional text member of at most max characters:
// the trimmed text ("" for none) and whether it may be taken.
func shortText(v jsbind.Value, max int) (string, bool) {
	if v.IsUndefined() || v.IsNull() {
		return "", true
	}
	if !v.IsString() {
		return "", false
	}
	trimmed := jsbind.Trim(v.Str())
	return trimmed, utf8.RuneCountInString(trimmed) <= max
}

// titleFrom is the title of a ticket the computer sent without one: the
// first line of the description, cut at maxDerivedTitle characters.
func titleFrom(description string) string {
	line, _, _ := strings.Cut(jsbind.Trim(description), "\n")
	line = jsbind.Trim(line)
	if utf8.RuneCountInString(line) > maxDerivedTitle {
		line = string([]rune(line)[:maxDerivedTitle-1]) + "…"
	}
	return line
}

// createFromAgent answers POST /api/tickets/nd with the body
// {description?, title?, reporter?, os?, priority?, category?,
// screenshot?: {name, type, data_b64}, files?: [{name, type, data_b64}]}:
// a ticket about the token's device, opened by the person at the
// computer (source "device"), made by "agent:<device id>" and assigned to
// nobody. A ticket without a title takes the first line of its
// description; one needs either. A priority or a category that is not one
// of the known texts becomes medium or general. The screenshot and the
// files (25 MB together) become the ticket's files, and computer-info.json
// with what the server last stored about the computer joins them.
func (h *handlers) createFromAgent(w http.ResponseWriter, r *http.Request) {
	device := kit.Device(r).DeviceID
	body := kit.Body(r)
	rawTitle := jsbind.Of(body.Get("title"))
	description := jsbind.Of(body.Get("description"))
	// A title that is no text is refused before the description is read
	if !optionalText(rawTitle) {
		fail(w, r, http.StatusBadRequest, "Title is required")
		return
	}
	if !optionalText(description) {
		fail(w, r, http.StatusBadRequest, "Invalid description")
		return
	}
	title, ok := text(rawTitle)
	if !ok {
		title = titleFrom(description.Str())
		ok = title != ""
	}
	if !ok {
		fail(w, r, http.StatusBadRequest, "Title is required")
		return
	}
	reporter, ok := shortText(jsbind.Of(body.Get("reporter")), maxReporter)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid reporter")
		return
	}
	system, ok := shortText(jsbind.Of(body.Get("os")), maxReporter)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid os")
		return
	}
	files, ok := readUploads(w, r, jsbind.Of(body.Get("screenshot")), jsbind.Of(body.Get("files")))
	if !ok {
		return
	}
	priority, category := "medium", "general"
	if p := jsbind.Of(body.Get("priority")); oneOf(p, validPriorities) {
		priority = p.Str()
	}
	if c := jsbind.Of(body.Get("category")); oneOf(c, validCategories) {
		category = c.Str()
	}
	reporterValue := jsbind.Null
	if reporter != "" {
		reporterValue = jsbind.String(reporter)
	}
	id, err := createTicket(r.Context(), h.db, newTicket{
		Title:       jsbind.String(title),
		Description: description.Or(jsbind.String("")),
		Priority:    jsbind.String(priority),
		Category:    jsbind.String(category),
		DeviceID:    jsbind.String(device),
		CreatedBy:   jsbind.String(agentName(device)),
		AssignedTo:  jsbind.Null,
		SLADueAt:    jsbind.String(h.slaDue(priority)),
		Source:      sourceDevice,
		Reporter:    reporterValue,
	})
	var t *ticket
	if err == nil {
		t, err = ticketByID(r.Context(), h.db, id.Value())
	}
	if err == nil {
		info := h.computerInfo(r.Context(), device, reporter, system)
		files = append(files, upload{name: computerInfo, mimetype: "application/json", data: info})
		for _, f := range files {
			if _, err = h.storeFile(r, t, f.name, f.mimetype, f.data, agentName(device), jsbind.Undefined); err != nil {
				break
			}
		}
	}
	var out []deviceTicket
	if err == nil {
		out, err = deviceTickets(r.Context(), h.db, device, id.Value())
	}
	if err == nil && len(out) == 0 {
		err = errNoTicket
	}
	if err != nil {
		h.serverError(w, r, "Agent create error", err)
		return
	}
	h.log.Info("#" + number(id) + " created by agent " + device + ": " + title)
	kit.JSON(w, r, http.StatusCreated, agentCreated{Success: true, TicketID: id, Ticket: out[0]})
}

// errNoTicket is a ticket that was written and cannot be read back.
var errNoTicket = errorText("the new ticket cannot be read")

type errorText string

func (e errorText) Error() string { return string(e) }

// listForAgent answers GET /api/tickets/nd: the tickets the token's
// device opened, newest first.
func (h *handlers) listForAgent(w http.ResponseWriter, r *http.Request) {
	tickets, err := deviceTickets(r.Context(), h.db, kit.Device(r).DeviceID, jsbind.Undefined)
	if err != nil {
		h.serverError(w, r, "Agent list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, deviceTicketList{Tickets: tickets, Total: len(tickets)})
}

// deviceTicketOf is the ticket the address names when the token's device
// opened it, nil for any other and for none.
func (h *handlers) deviceTicketOf(r *http.Request) (*ticket, error) {
	id, ok := ticketID(r, "id")
	if !ok {
		return nil, nil
	}
	t, err := ticketByID(r.Context(), h.db, id)
	if err != nil || t == nil || !t.fromDevice() || t.DeviceID.String != kit.Device(r).DeviceID {
		return nil, err
	}
	return t, nil
}

// getForAgent answers GET /api/tickets/nd/{id}: the ticket with its
// public comments and the files the computer may see.
func (h *handlers) getForAgent(w http.ResponseWriter, r *http.Request) {
	t, err := h.deviceTicketOf(r)
	if err == nil && t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	var detail deviceDetail
	if err == nil {
		detail, err = h.deviceDetail(r.Context(), t)
	}
	if err != nil {
		h.serverError(w, r, "Agent detail error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, detail)
}

// deviceDetail is a ticket of the computer with its public comments and
// the files it may see: its own, and those of public comments.
func (h *handlers) deviceDetail(ctx context.Context, t *ticket) (deviceDetail, error) {
	device := t.DeviceID.String
	rows, err := deviceTickets(ctx, h.db, device, t.id())
	if err != nil {
		return deviceDetail{}, err
	}
	if len(rows) == 0 {
		return deviceDetail{}, errNoTicket
	}
	comments, err := ticketComments(ctx, h.db, t.id())
	if err != nil {
		return deviceDetail{}, err
	}
	attachments, err := ticketAttachments(ctx, h.db, t.id())
	if err != nil {
		return deviceDetail{}, err
	}
	detail := deviceDetail{Ticket: rows[0], Comments: []deviceComment{}, Attachments: []deviceFile{}}
	public := map[string]bool{}
	for _, c := range comments {
		if c.IsInternal.True() {
			continue
		}
		public[number(c.ID)] = true
		detail.Comments = append(detail.Comments, h.deviceCommentOf(ctx, t, c))
	}
	for _, a := range attachments {
		if !deviceSees(a, device, public) {
			continue
		}
		detail.Attachments = append(detail.Attachments, deviceFileOf(a))
	}
	return detail, nil
}

// deviceSees reports whether the computer gets a file of its ticket: one
// it sent, or one of a public comment. A file staff put on the ticket
// itself stays with staff.
func deviceSees(a attachment, device string, public map[string]bool) bool {
	if a.CommentID.Value().IsNull() || a.CommentID.Value().IsUndefined() {
		return a.UploadedBy == agentName(device)
	}
	return public[number(a.CommentID)]
}

func deviceFileOf(a attachment) deviceFile {
	mimetype := "application/octet-stream"
	if a.Mimetype.Valid && mediaType(a.Mimetype.String) {
		mimetype = a.Mimetype.String
	}
	return deviceFile{ID: a.ID, Name: a.Filename, Type: mimetype, Size: a.SizeBytes, CommentID: a.CommentID}
}

// deviceCommentOf is a public comment as the computer gets it.
func (h *handlers) deviceCommentOf(ctx context.Context, t *ticket, c comment) deviceComment {
	out := deviceComment{ID: c.ID, From: c.From, Body: c.Body, CreatedAt: c.CreatedAt}
	if c.From == sourceDevice {
		out.AuthorName = t.Reporter.String
	} else {
		out.AuthorName = h.staffName(ctx, c.Author)
	}
	return out
}

// staffName is a staff member's first and last name, else the user name.
func (h *handlers) staffName(ctx context.Context, username string) string {
	var first, last store.NullString
	if err := h.db.QueryRow(ctx, `SELECT first_name, last_name FROM users WHERE username = ?`, username).Scan(&first, &last); err != nil {
		return username
	}
	if name := jsbind.Trim(jsbind.Trim(first.String) + " " + jsbind.Trim(last.String)); name != "" {
		return name
	}
	return username
}

// commentFromAgent answers POST /api/tickets/nd/{id}/comments with the
// body {body, files?: [{name, type, data_b64}]}: a public comment of the
// person at the computer, with its files (25 MB together). Writing counts
// as reading the ticket.
func (h *handlers) commentFromAgent(w http.ResponseWriter, r *http.Request) {
	t, err := h.deviceTicketOf(r)
	if err != nil {
		h.serverError(w, r, "Agent comment error", err)
		return
	}
	if t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	body := kit.Body(r)
	content, ok := text(jsbind.Of(body.Get("body")))
	if !ok {
		fail(w, r, http.StatusBadRequest, "Comment body is required")
		return
	}
	files, ok := readUploads(w, r, jsbind.Undefined, jsbind.Of(body.Get("files")))
	if !ok {
		return
	}
	device := kit.Device(r).DeviceID
	id, err := addComment(r.Context(), h.db, t.id(), agentName(device), content, false, sourceDevice)
	for _, f := range files {
		if err != nil {
			break
		}
		_, err = h.storeFile(r, t, f.name, f.mimetype, f.data, agentName(device), id.Value())
	}
	if err == nil {
		err = markDeviceRead(r.Context(), h.db, t.id(), true)
	}
	var comments []comment
	if err == nil {
		comments, err = ticketComments(r.Context(), h.db, t.id())
	}
	if err != nil {
		h.serverError(w, r, "Agent comment error", err)
		return
	}
	out := deviceComment{ID: id, AuthorName: t.Reporter.String, From: sourceDevice, Body: content}
	for _, c := range comments {
		if number(c.ID) == number(id) {
			out = h.deviceCommentOf(r.Context(), t, c)
		}
	}
	kit.JSON(w, r, http.StatusCreated, deviceCommented{Success: true, Comment: out})
}

// downloadForAgent answers GET /api/tickets/nd/{id}/attachments/{aid}
// with a file of the ticket the computer may see, as the staff download
// sends it.
func (h *handlers) downloadForAgent(w http.ResponseWriter, r *http.Request) {
	t, err := h.deviceTicketOf(r)
	var a *attachment
	if err == nil && t != nil {
		if id, ok := ticketID(r, "aid"); ok {
			a, err = attachmentByID(r.Context(), h.db, id)
		}
	}
	if err == nil && a != nil {
		if number(a.TicketID) != number(t.ID) {
			a = nil
		} else if !deviceSees(*a, t.DeviceID.String, nil) {
			// A file of a comment: the comment has to be public
			var comments []comment
			comments, err = ticketComments(r.Context(), h.db, t.id())
			public := map[string]bool{}
			for _, c := range comments {
				if !c.IsInternal.True() {
					public[number(c.ID)] = true
				}
			}
			if !deviceSees(*a, t.DeviceID.String, public) {
				a = nil
			}
		}
	}
	if err != nil {
		h.serverError(w, r, "Agent download error", err)
		return
	}
	if a == nil {
		fail(w, r, http.StatusNotFound, "Attachment not found")
		return
	}
	h.send(w, r, a)
}

// readForAgent answers POST /api/tickets/nd/{id}/read with 204: the
// computer has read what staff wrote.
func (h *handlers) readForAgent(w http.ResponseWriter, r *http.Request) {
	t, err := h.deviceTicketOf(r)
	if err == nil && t == nil {
		fail(w, r, http.StatusNotFound, "Ticket not found")
		return
	}
	if err == nil {
		err = markDeviceRead(r.Context(), h.db, t.id(), true)
	}
	if err != nil {
		h.serverError(w, r, "Agent read error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// computerInfo is the file computer-info.json: what the server stored
// last about the computer, from the sealed heartbeat (identity, metrics,
// hardware) and from the agent's inventory and telemetry routes, with the
// system and the person the app named. Nothing is collected for it. A
// part that is not there, or cannot be read, is left out.
func (h *handlers) computerInfo(ctx context.Context, device, reporter, system string) []byte {
	info := map[string]any{"device_id": device}
	if reporter != "" {
		info["reporter"] = reporter
	}
	if system != "" {
		info["os"] = system
	}
	snapshots := map[string]any{}
	for _, kind := range []string{"identity", "metrics", "hardware"} {
		var payload, collected, received store.NullString
		err := h.db.QueryRow(ctx, `SELECT CAST(payload AS TEXT), CAST(collected_at AS TEXT), CAST(received_at AS TEXT) FROM device_telemetry_snapshots WHERE device_id = ? AND kind = ?`,
			device, kind).Scan(&payload, &collected, &received)
		if err != nil || !json.Valid([]byte(payload.String)) {
			continue
		}
		snapshots[kind] = map[string]any{"collected_at": collected.String, "received_at": received.String, "data": json.RawMessage(payload.String)}
	}
	if len(snapshots) > 0 {
		info["telemetry"] = snapshots
	}
	var hardware, collected store.NullString
	if err := h.db.QueryRow(ctx, `SELECT CAST(hardware AS TEXT), CAST(collected_at AS TEXT) FROM device_inventory WHERE device_id = ?`, device).Scan(&hardware, &collected); err == nil && json.Valid([]byte(hardware.String)) {
		info["inventory"] = map[string]any{"collected_at": collected.String, "hardware": json.RawMessage(hardware.String)}
	}
	var cpu, used, total, uptime store.NullString
	if err := h.db.QueryRow(ctx, `SELECT CAST(cpu_usage_percent AS TEXT), CAST(memory_used_bytes AS TEXT), CAST(memory_total_bytes AS TEXT), CAST(uptime_secs AS TEXT) FROM device_telemetry WHERE device_id = ?`, device).Scan(&cpu, &used, &total, &uptime); err == nil {
		info["load"] = map[string]any{"cpu_usage_percent": cpu.String, "memory_used_bytes": used.String, "memory_total_bytes": total.String, "uptime_secs": uptime.String}
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return []byte(`{"device_id":` + string(must(json.Marshal(device))) + `}`)
	}
	return out
}

func must(b []byte, _ error) []byte { return b }
