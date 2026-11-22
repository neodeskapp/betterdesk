package cdapstudio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// text is a text column. In JSON it is written the way the old console
// wrote a string: only the quote, the backslash and control characters
// are escaped.
type text string

// MarshalJSON implements json.Marshaler.
func (t text) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	quote(&b, string(t))
	return []byte(b.String()), nil
}

// flow is a row of studio_flows, with its members in the order of the
// table's columns.
type flow struct {
	ID          text       `json:"id"`
	Name        text       `json:"name"`
	Description text       `json:"description"`
	FlowJSON    text       `json:"flow_json"`
	Status      text       `json:"status"`
	Version     int64      `json:"version"`
	CreatedBy   text       `json:"created_by"`
	CreatedAt   store.Time `json:"created_at"`
	UpdatedAt   store.Time `json:"updated_at"`
}

// listedFlow is a flow in the list: everything but its JSON.
type listedFlow struct {
	ID          text       `json:"id"`
	Name        text       `json:"name"`
	Description text       `json:"description"`
	Status      text       `json:"status"`
	Version     int64      `json:"version"`
	CreatedBy   text       `json:"created_by"`
	CreatedAt   store.Time `json:"created_at"`
	UpdatedAt   store.Time `json:"updated_at"`
}

// listFlows returns the flows without their JSON, the one changed last
// first. owner limits the list to the flows of one account; nil lists
// all.
func listFlows(ctx context.Context, db *store.DB, owner *string) ([]listedFlow, error) {
	query := "SELECT id, name, description, status, version, created_by, created_at, updated_at FROM studio_flows"
	var args []any
	if owner != nil {
		query += " WHERE created_by = ?"
		args = append(args, *owner)
	}
	rows, err := store.Select(ctx, db, query+" ORDER BY updated_at DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	defer rows.Close()
	flows := []listedFlow{}
	for rows.Next() {
		var f listedFlow
		if err := rows.Scan(&f.ID, &f.Name, &f.Description, &f.Status, &f.Version, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read a flow of the list: %w", err)
		}
		flows = append(flows, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	return flows, nil
}

// getFlow returns the flow with the id, nil when there is none.
func getFlow(ctx context.Context, db *store.DB, id string) (*flow, error) {
	var f flow
	err := store.SelectRow(ctx, db,
		"SELECT id, name, description, flow_json, status, version, created_by, created_at, updated_at FROM studio_flows WHERE id = ?", id).
		Scan(&f.ID, &f.Name, &f.Description, &f.FlowJSON, &f.Status, &f.Version, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load flow %s: %w", id, err)
	}
	return &f, nil
}

// createFlow stores a new draft. On SQLite both times are now as an ISO
// text; on PostgreSQL the columns take their default, the database's
// clock.
func createFlow(ctx context.Context, db *store.DB, id, name, description, flowJSON, createdBy string, now time.Time) error {
	var err error
	if db.IsPostgres() {
		_, err = db.Exec(ctx,
			"INSERT INTO studio_flows (id, name, description, flow_json, status, created_by) VALUES (?, ?, ?, ?, ?, ?)",
			id, name, description, flowJSON, "draft", createdBy)
	} else {
		at := store.ISOTime(now)
		_, err = db.Exec(ctx,
			"INSERT INTO studio_flows (id, name, description, flow_json, status, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			id, name, description, flowJSON, "draft", createdBy, at, at)
	}
	if err != nil {
		return fmt.Errorf("create flow %s: %w", id, err)
	}
	return nil
}

// updateFlow changes name, description and JSON of a flow and counts its
// version up.
func updateFlow(ctx context.Context, db *store.DB, id, name, description, flowJSON string, now time.Time) error {
	if flowJSON == "" {
		flowJSON = "{}"
	}
	var err error
	if db.IsPostgres() {
		_, err = db.Exec(ctx,
			"UPDATE studio_flows SET name = ?, description = ?, flow_json = ?, version = version + 1, updated_at = NOW() WHERE id = ?",
			name, description, flowJSON, id)
	} else {
		_, err = db.Exec(ctx,
			"UPDATE studio_flows SET name = ?, description = ?, flow_json = ?, version = version + 1, updated_at = ? WHERE id = ?",
			name, description, flowJSON, store.ISOTime(now), id)
	}
	if err != nil {
		return fmt.Errorf("update flow %s: %w", id, err)
	}
	return nil
}

// setFlowStatus changes the status of a flow.
func setFlowStatus(ctx context.Context, db *store.DB, id, status string, now time.Time) error {
	var err error
	if db.IsPostgres() {
		_, err = db.Exec(ctx, "UPDATE studio_flows SET status = ?, updated_at = NOW() WHERE id = ?", status, id)
	} else {
		_, err = db.Exec(ctx, "UPDATE studio_flows SET status = ?, updated_at = ? WHERE id = ?", status, store.ISOTime(now), id)
	}
	if err != nil {
		return fmt.Errorf("set the status of flow %s: %w", id, err)
	}
	return nil
}

// deleteFlow removes a flow.
func deleteFlow(ctx context.Context, db *store.DB, id string) error {
	if _, err := db.Exec(ctx, "DELETE FROM studio_flows WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete flow %s: %w", id, err)
	}
	return nil
}
