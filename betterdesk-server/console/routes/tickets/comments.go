package tickets

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
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
	t, err := ticketByID(r.Context(), h.db, ticketID(r, "id"))
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
	id, err := addComment(r.Context(), h.db, t.id(), user.Username, content, internal)
	if err != nil {
		h.serverError(w, r, "Comment error", err)
		return
	}
	kit.JSON(w, r, http.StatusCreated, commented{Success: true, Comment: createdComment{ID: id}})
}

type commentList struct {
	Comments []comment `json:"comments"`
}

// comments answers GET /api/tickets/{id}/comments. A viewer does not get
// the internal ones.
func (h *handlers) comments(w http.ResponseWriter, r *http.Request) {
	comments, err := ticketComments(r.Context(), h.db, ticketID(r, "id"))
	if err != nil {
		h.serverError(w, r, "Comments list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, commentList{Comments: visible(r, comments)})
}
