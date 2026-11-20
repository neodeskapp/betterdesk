package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries take request values as they came (jsbind.Value) and bind
// them as the old console's driver for the database did, so a value of
// the wrong type fails or is stored the way it was on each database.

// ticket is a row of tickets, in the order of the table's columns.
type ticket struct {
	ID          jsbind.Cell      `json:"id"`
	Title       string           `json:"title"`
	Description store.NullString `json:"description"`
	Status      store.NullString `json:"status"`
	Priority    store.NullString `json:"priority"`
	Category    store.NullString `json:"category"`
	DeviceID    store.NullString `json:"device_id"`
	CreatedBy   string           `json:"created_by"`
	AssignedTo  store.NullString `json:"assigned_to"`
	SLADueAt    store.Time       `json:"sla_due_at"`
	ResolvedAt  store.Time       `json:"resolved_at"`
	ClosedAt    store.Time       `json:"closed_at"`
	CreatedAt   store.Time       `json:"created_at"`
	UpdatedAt   store.Time       `json:"updated_at"`
}

const ticketColumns = "id, title, description, status, priority, category, device_id, created_by, assigned_to, sla_due_at, resolved_at, closed_at, created_at, updated_at"

func (t *ticket) dest() []any {
	return []any{&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Category, &t.DeviceID, &t.CreatedBy, &t.AssignedTo,
		&t.SLADueAt, &t.ResolvedAt, &t.ClosedAt, &t.CreatedAt, &t.UpdatedAt}
}

// id is the ticket's id as a statement parameter.
func (t *ticket) id() jsbind.Value { return t.ID.Value() }

// ticketByID returns the ticket with the id, nil when there is none.
func ticketByID(ctx context.Context, db *store.DB, id jsbind.Value) (*ticket, error) {
	doing := "load ticket " + id.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	var t ticket
	switch err := store.SelectRow(ctx, db, "SELECT "+ticketColumns+" FROM tickets WHERE id = ?", args...).Scan(t.dest()...); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return &t, nil
}

// filter is one condition of the ticket list: a column and the value the
// request gave for it.
type filter struct {
	column string
	value  jsbind.Value
}

// listTickets returns the tickets that pass every filter and whose title
// or description holds the search text, newest first; tickets of the same
// second by their id. search is
// undefined for a list without a search.
func listTickets(ctx context.Context, db *store.DB, filters []filter, search jsbind.Value) ([]ticket, error) {
	where := "WHERE 1=1"
	var values []jsbind.Value
	placeholders := 0
	for _, f := range filters {
		where += " AND " + f.column + " = ?"
		values = append(values, f.value)
		placeholders++
	}
	if !search.IsUndefined() {
		// Only a text is searched for; any other value matches everything
		pattern := jsbind.String("%" + store.EscapeLike(search.Str()) + "%")
		where += db.Pick(
			` AND (title LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\')`,
			` AND (title ILIKE ? ESCAPE '\' OR description ILIKE ? ESCAPE '\')`)
		values = append(values, pattern, pattern)
		placeholders += 2
	}
	args, err := jsbind.Args(db.Dialect(), placeholders, values...)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	rows, err := store.Select(ctx, db, "SELECT "+ticketColumns+" FROM tickets "+where+" ORDER BY created_at DESC, id DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()
	tickets := []ticket{}
	for rows.Next() {
		var t ticket
		if err := rows.Scan(t.dest()...); err != nil {
			return nil, fmt.Errorf("read a ticket: %w", err)
		}
		tickets = append(tickets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	return tickets, nil
}

// ticketStats is the number of tickets, all and by status.
type ticketStats struct {
	Total      int64 `json:"total"`
	Open       int64 `json:"open"`
	InProgress int64 `json:"in_progress"`
	Resolved   int64 `json:"resolved"`
	Closed     int64 `json:"closed"`
}

func countTickets(ctx context.Context, db *store.DB) (ticketStats, error) {
	var s ticketStats
	err := db.QueryRow(ctx, `SELECT COUNT(*),
		COUNT(CASE WHEN status = 'open' THEN 1 END),
		COUNT(CASE WHEN status = 'in_progress' THEN 1 END),
		COUNT(CASE WHEN status = 'resolved' THEN 1 END),
		COUNT(CASE WHEN status = 'closed' THEN 1 END)
		FROM tickets`).Scan(&s.Total, &s.Open, &s.InProgress, &s.Resolved, &s.Closed)
	if err != nil {
		return s, fmt.Errorf("count tickets: %w", err)
	}
	return s, nil
}

// newTicket is what a ticket is made from. Empty values have been
// replaced by their defaults already.
type newTicket struct {
	Title, Description, Priority, Category, DeviceID, CreatedBy, AssignedTo, SLADueAt jsbind.Value
}

// insertRow runs an INSERT and returns the new row's id as the old
// console's driver gave it.
func insertRow(ctx context.Context, db *store.DB, doing, query string, values ...jsbind.Value) (jsbind.Cell, error) {
	args, err := jsbind.Args(db.Dialect(), strings.Count(query, "?"), values...)
	if err != nil {
		return jsbind.Cell{}, fmt.Errorf("%s: %w", doing, err)
	}
	if db.IsPostgres() {
		var id jsbind.Cell
		if err := store.SelectRow(ctx, db, query+" RETURNING id", args...).Scan(&id); err != nil {
			return jsbind.Cell{}, fmt.Errorf("%s: %w", doing, err)
		}
		return id, nil
	}
	res, err := db.Exec(ctx, query, args...)
	if err != nil {
		return jsbind.Cell{}, fmt.Errorf("%s: %w", doing, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return jsbind.Cell{}, fmt.Errorf("%s: %w", doing, err)
	}
	return jsbind.CellOf(id), nil
}

// createTicket inserts a ticket and returns its id.
func createTicket(ctx context.Context, db *store.DB, data newTicket) (jsbind.Cell, error) {
	return insertRow(ctx, db, "create ticket",
		"INSERT INTO tickets (title, description, priority, category, device_id, created_by, assigned_to, sla_due_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		data.Title, data.Description, data.Priority, data.Category, data.DeviceID, data.CreatedBy, data.AssignedTo, data.SLADueAt)
}

// change is one column an update sets.
type change struct {
	column string
	value  jsbind.Value
}

// updateTicket sets the given columns and the time of the change. A
// ticket whose status becomes resolved or closed gets that time too.
func updateTicket(ctx context.Context, db *store.DB, id jsbind.Value, changes []change) error {
	now := store.NowSQL(db.Dialect())
	sets := make([]string, 0, len(changes)+3)
	values := make([]jsbind.Value, 0, len(changes)+1)
	for _, c := range changes {
		sets = append(sets, c.column+" = ?")
		values = append(values, c.value)
	}
	for _, c := range changes {
		if c.column != "status" || !c.value.IsString() {
			continue
		}
		switch c.value.Str() {
		case "resolved":
			sets = append(sets, "resolved_at = "+now)
		case "closed":
			sets = append(sets, "closed_at = "+now)
		}
	}
	sets = append(sets, "updated_at = "+now)
	values = append(values, id)
	args, err := jsbind.Args(db.Dialect(), len(values), values...)
	if err != nil {
		return fmt.Errorf("update ticket %s: %w", id.ToString(), err)
	}
	if _, err := db.Exec(ctx, "UPDATE tickets SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return fmt.Errorf("update ticket %s: %w", id.ToString(), err)
	}
	return nil
}

// deleteTicket removes a ticket with its comments and attachment rows, in
// three statements that are not one transaction.
func deleteTicket(ctx context.Context, db *store.DB, id jsbind.Value) error {
	for _, query := range []string{
		"DELETE FROM ticket_comments WHERE ticket_id = ?",
		"DELETE FROM ticket_attachments WHERE ticket_id = ?",
		"DELETE FROM tickets WHERE id = ?",
	} {
		args, err := jsbind.Args(db.Dialect(), 1, id)
		if err == nil {
			_, err = db.Exec(ctx, query, args...)
		}
		if err != nil {
			return fmt.Errorf("delete ticket %s: %w", id.ToString(), err)
		}
	}
	return nil
}

// comment is a row of ticket_comments.
type comment struct {
	ID         jsbind.Cell `json:"id"`
	TicketID   jsbind.Cell `json:"ticket_id"`
	Author     string      `json:"author"`
	Body       string      `json:"body"`
	IsInternal store.Bool  `json:"is_internal"`
	CreatedAt  store.Time  `json:"created_at"`
}

// ticketComments lists a ticket's comments, oldest first.
func ticketComments(ctx context.Context, db *store.DB, ticketID jsbind.Value) ([]comment, error) {
	doing := "list the comments of ticket " + ticketID.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, ticketID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	rows, err := store.Select(ctx, db, "SELECT id, ticket_id, author, body, is_internal, created_at FROM ticket_comments WHERE ticket_id = ? ORDER BY created_at ASC, id ASC", args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	defer rows.Close()
	comments := []comment{}
	for rows.Next() {
		var c comment
		if err := rows.Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &c.IsInternal, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
		comments = append(comments, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return comments, nil
}

// addComment inserts a comment, moves the ticket's time of change and
// returns the comment's id.
func addComment(ctx context.Context, db *store.DB, ticketID jsbind.Value, author, body string, internal bool) (jsbind.Cell, error) {
	flag := jsbind.Bool(internal)
	if !db.IsPostgres() {
		flag = jsbind.Number(0)
		if internal {
			flag = jsbind.Number(1)
		}
	}
	id, err := insertRow(ctx, db, "add a comment to ticket "+ticketID.ToString(),
		"INSERT INTO ticket_comments (ticket_id, author, body, is_internal) VALUES (?, ?, ?, ?)",
		ticketID, jsbind.String(author), jsbind.String(body), flag)
	if err != nil {
		return id, err
	}
	args, err := jsbind.Args(db.Dialect(), 1, ticketID)
	if err == nil {
		_, err = db.Exec(ctx, "UPDATE tickets SET updated_at = "+store.NowSQL(db.Dialect())+" WHERE id = ?", args...)
	}
	if err != nil {
		return id, fmt.Errorf("touch ticket %s: %w", ticketID.ToString(), err)
	}
	return id, nil
}

// attachment is a row of ticket_attachments. StoragePath names the file
// in the attachment folder (older rows: its absolute path); it never goes
// into an answer.
type attachment struct {
	ID          jsbind.Cell      `json:"id"`
	Filename    string           `json:"filename"`
	Mimetype    store.NullString `json:"mimetype"`
	SizeBytes   jsbind.Cell      `json:"size_bytes"`
	UploadedBy  string           `json:"uploaded_by"`
	CreatedAt   store.Time       `json:"created_at"`
	StoragePath string           `json:"-"`
}

const attachmentColumns = "id, filename, mimetype, size_bytes, uploaded_by, created_at, storage_path"

func (a *attachment) dest() []any {
	return []any{&a.ID, &a.Filename, &a.Mimetype, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt, &a.StoragePath}
}

// ticketAttachments lists a ticket's attachments, oldest first.
func ticketAttachments(ctx context.Context, db *store.DB, ticketID jsbind.Value) ([]attachment, error) {
	doing := "list the attachments of ticket " + ticketID.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, ticketID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	rows, err := store.Select(ctx, db, "SELECT "+attachmentColumns+" FROM ticket_attachments WHERE ticket_id = ? ORDER BY created_at ASC, id ASC", args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	defer rows.Close()
	attachments := []attachment{}
	for rows.Next() {
		var a attachment
		if err := rows.Scan(a.dest()...); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
		attachments = append(attachments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return attachments, nil
}

// attachmentByID returns the attachment with the id, nil when there is
// none.
func attachmentByID(ctx context.Context, db *store.DB, id jsbind.Value) (*attachment, error) {
	doing := "load attachment " + id.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	var a attachment
	switch err := store.SelectRow(ctx, db, "SELECT "+attachmentColumns+" FROM ticket_attachments WHERE id = ?", args...).Scan(a.dest()...); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return &a, nil
}

// addAttachment inserts the row of a stored file and returns its id.
func addAttachment(ctx context.Context, db *store.DB, ticketID jsbind.Value, filename string, mimetype jsbind.Value, size int, storagePath, uploadedBy string) (jsbind.Cell, error) {
	return insertRow(ctx, db, "add an attachment to ticket "+ticketID.ToString(),
		"INSERT INTO ticket_attachments (ticket_id, filename, mimetype, size_bytes, storage_path, uploaded_by) VALUES (?, ?, ?, ?, ?, ?)",
		ticketID, jsbind.String(filename), mimetype, jsbind.Number(float64(size)), jsbind.String(storagePath), jsbind.String(uploadedBy))
}
