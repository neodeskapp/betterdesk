package tickets

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

type commented struct {
	Success bool           `json:"success"`
	Comment createdComment `json:"comment"`
}

type createdComment struct {
	ID jsbind.Cell `json:"id"`
}

// addComment answers POST /api/tickets/{id}/comments with the body
// {body, is_internal?}. A viewer's comment is never internal.
func (h *handlers) addComment(w http.ResponseWriter, r *http.Request) {
	t, err := h.ticketOf(r)
	if err != nil {
		h.serverError(w, r, "Comment error", err)
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
	user := kit.User(r)
	internal := user.Role != "viewer" && body.Truthy("is_internal")
	id, err := addComment(r.Context(), h.db, t.id(), user.Username, content, internal, sourceStaff)
	if err != nil {
		h.serverError(w, r, "Comment error", err)
		return
	}
	// A public answer to a computer's ticket reaches the computer
	if !internal && t.fromDevice() {
		h.tellDevice(r, t, t.Status.String)
	}
	kit.JSON(w, r, http.StatusCreated, commented{Success: true, Comment: createdComment{ID: id}})
}

// tellDevice marks a computer's ticket unread for it and sends its chat
// sockets ticket.updated with the ticket's status. A failure to mark it
// is logged; the staff member's change stands.
func (h *handlers) tellDevice(r *http.Request, t *ticket, status string) {
	if err := markDeviceRead(r.Context(), h.db, t.id(), false); err != nil {
		h.log.Error("Mark ticket #"+number(t.ID)+" unread for its device:", err)
	}
	if h.push != nil && t.DeviceID.String != "" {
		h.push(t.DeviceID.String, "ticket.updated", map[string]any{"ticket_id": t.ID, "status": status, "unread": true})
	}
}

type commentList struct {
	Comments []comment `json:"comments"`
}

// comments answers GET /api/tickets/{id}/comments. An account without
// device.edit does not get the internal ones, and an account gets none of
// a ticket it may not read (mayRead).
func (h *handlers) comments(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r, "id")
	if ok {
		t, err := h.readable(r, id)
		if err != nil {
			h.serverError(w, r, "Comments list error", err)
			return
		}
		ok = t != nil
	}
	if !ok {
		kit.JSON(w, r, http.StatusOK, commentList{Comments: []comment{}})
		return
	}
	comments, err := ticketComments(r.Context(), h.db, id)
	if err != nil {
		h.serverError(w, r, "Comments list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, commentList{Comments: visible(r, comments)})
}
