package tickets

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// The two routes of the desktop agent, which takes its device token
// instead of a session. Both are below the mount /api/tickets, behind the
// CSRF gate: a write needs the CSRF token like any other.

type agentCreated struct {
	Success  bool        `json:"success"`
	TicketID jsbind.Cell `json:"ticket_id"`
}

// createFromAgent answers POST /api/tickets/bd with the body {title,
// description?, priority?, category?}: a ticket about the token's device,
// made by "agent:<device id>" and assigned to nobody. A priority or a
// category that is not one of the known texts becomes medium or general.
func (h *handlers) createFromAgent(w http.ResponseWriter, r *http.Request) {
	device := kit.Device(r).DeviceID
	body := kit.Body(r)
	rawTitle := jsbind.Of(body.Get("title"))
	title, empty, broken := text(rawTitle)
	if broken {
		h.serverError(w, r, "Agent create error", errNotText("title"))
		return
	}
	if empty {
		fail(w, r, http.StatusBadRequest, "Title is required")
		return
	}
	priority, category := "medium", "general"
	if p := jsbind.Of(body.Get("priority")); oneOf(p, validPriorities) {
		priority = p.Str()
	}
	if c := jsbind.Of(body.Get("category")); oneOf(c, validCategories) {
		category = c.Str()
	}
	id, err := createTicket(r.Context(), h.db, newTicket{
		Title:       jsbind.String(title),
		Description: jsbind.Of(body.Get("description")).Or(jsbind.String("")),
		Priority:    jsbind.String(priority),
		Category:    jsbind.String(category),
		DeviceID:    jsbind.String(device),
		CreatedBy:   jsbind.String("agent:" + device),
		AssignedTo:  jsbind.Null,
		SLADueAt:    jsbind.String(h.slaDue(priority)),
	})
	if err != nil {
		h.serverError(w, r, "Agent create error", err)
		return
	}
	h.log.Info("#" + number(id) + " created by agent " + device + ": " + rawTitle.Str())
	kit.JSON(w, r, http.StatusCreated, agentCreated{Success: true, TicketID: id})
}

// listForAgent answers GET /api/tickets/bd: the tickets about the token's
// device, newest first.
func (h *handlers) listForAgent(w http.ResponseWriter, r *http.Request) {
	filters := []filter{{"device_id", jsbind.String(kit.Device(r).DeviceID)}}
	tickets, err := listTickets(r.Context(), h.db, filters, jsbind.Undefined)
	if err != nil {
		h.serverError(w, r, "Agent list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, ticketList{Tickets: tickets, Total: len(tickets)})
}
