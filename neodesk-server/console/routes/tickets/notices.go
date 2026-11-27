package tickets

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The bell of the web app tells staff when the person at a computer opens
// a ticket or replies to one (A277). Its route (ndapi notifications)
// takes them from here, for an account that may read the ticket: with
// device.view, a viewer only the tickets assigned to it, a limited account
// only those of its devices.

// Notice kinds and the prefixes of their ids.
const (
	NoticeTicket      = "ticket"
	NoticeTicketReply = "ticket_reply"
)

// maxNotices is how many of each kind the bell looks at.
const maxNotices = 50

// Notice is one entry of the bell: a ticket a computer opened, or a reply
// it wrote.
type Notice struct {
	// ID is "ticket:<ticket id>" or "ticket_reply:<comment id>"
	ID       string
	Kind     string
	TicketID int64
	Title    string
	DeviceID string
	Reporter string
	// Message is the ticket's description or the reply
	Message   string
	CreatedAt store.Time
}

// Notices are the newest tickets the computers opened and the newest
// replies they wrote that the request's account may read, maxNotices of
// each at most.
func Notices(r *http.Request, d *kit.Deps) ([]Notice, error) {
	if kit.User(r) == nil || !kit.HasPermission(r, "device.view") {
		return nil, nil
	}
	ctx := r.Context()
	scope, err := d.DeviceGroups.LimitedScope(ctx, kit.User(r), func() []*goapi.Object {
		return d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{IncludeDeleted: true})
	})
	if err != nil {
		return nil, err
	}
	db := d.Store
	created, err := notices(ctx, db, NoticeTicket,
		`SELECT t.id, t.id, t.title, t.device_id, t.reporter, t.description, t.created_at, t.assigned_to FROM tickets t
		WHERE t.source = 'device' ORDER BY t.created_at DESC, t.id DESC LIMIT `+strconv.Itoa(maxNotices))
	if err != nil {
		return nil, err
	}
	replies, err := notices(ctx, db, NoticeTicketReply,
		`SELECT c.id, t.id, t.title, t.device_id, t.reporter, c.body, c.created_at, t.assigned_to FROM ticket_comments c
		JOIN tickets t ON t.id = c.ticket_id
		WHERE c.from_kind = 'device' AND t.source = 'device' ORDER BY c.created_at DESC, c.id DESC LIMIT `+strconv.Itoa(maxNotices))
	if err != nil {
		return nil, err
	}
	var out []Notice
	for _, n := range append(created, replies...) {
		if mayReadNotice(r, scope, n) {
			out = append(out, n.Notice)
		}
	}
	return out, nil
}

// notice is a Notice with the ticket's assignee, for the check.
type notice struct {
	Notice
	assignee store.NullString
}

func mayReadNotice(r *http.Request, scope *access.DeviceScope, n notice) bool {
	user := kit.User(r)
	if user.Role == "viewer" && (!n.assignee.Valid || n.assignee.String != user.Username) {
		return false
	}
	return deviceInScope(scope, n.DeviceID)
}

func notices(ctx context.Context, db *store.DB, kind, query string) ([]notice, error) {
	rows, err := store.Select(ctx, db, query)
	if err != nil {
		return nil, fmt.Errorf("ticket notices: %w", err)
	}
	defer rows.Close()
	var out []notice
	for rows.Next() {
		var n notice
		var id int64
		var device, reporter, message store.NullString
		if err := rows.Scan(&id, &n.TicketID, &n.Title, &device, &reporter, &message, &n.CreatedAt, &n.assignee); err != nil {
			return nil, fmt.Errorf("ticket notices: %w", err)
		}
		n.ID, n.Kind = kind+":"+strconv.FormatInt(id, 10), kind
		n.DeviceID, n.Reporter = device.String, reporter.String
		n.Message = strings.TrimSpace(message.String)
		out = append(out, n)
	}
	return out, rows.Err()
}
