package registrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The pending_registrations methods of services/dbAdapter.js, for both
// adapters. Where they differ, the difference is kept:
//
//   - the search of List is LIKE on SQLite (letter case ignored for ASCII
//     only) and ILIKE on PostgreSQL;
//   - values are bound as each driver bound them (package jsbind): on
//     SQLite a list is spread over the placeholders and an object fails, on
//     PostgreSQL every value is sent as text, so an id the database cannot
//     read as an integer (NaN, 1e+21, 99999999999) is an error there and
//     finds nothing on SQLite.

// Request is a registration request as the device route stores it.
type Request struct {
	DeviceID  string
	Hostname  string
	Platform  string
	Version   string
	IPAddress string
	PublicKey string
	UUID      string
}

// ServerConfig is what an approved device gets from the status route.
// Empty fields are stored as NULL.
type ServerConfig struct {
	ConsoleURL    string
	ServerAddress string
	ServerKey     string
	AccessToken   string
}

// columnNames are the table's columns in their order, which is the order
// of the keys of a row. On SQLite the two DATETIME columns are read as the
// text they hold: the driver would read them as times.
var columnNames = []string{
	"id", "device_id", "hostname", "platform", "version", "ip_address", "public_key", "uuid", "status",
	"approved_by", "approved_at", "rejected_reason", "access_token", "console_url", "server_address",
	"server_key", "created_at", "updated_at",
}

const (
	sqliteColumns = `id, device_id, hostname, platform, version, ip_address, public_key, uuid, status,
		approved_by, approved_at, rejected_reason, access_token, console_url, server_address, server_key,
		CAST(created_at AS TEXT) AS created_at, CAST(updated_at AS TEXT) AS updated_at`
	postgresColumns = `id, device_id, hostname, platform, version, ip_address, public_key, uuid, status,
		approved_by, approved_at, rejected_reason, access_token, console_url, server_address, server_key,
		created_at, updated_at`
)

func columns(q store.Querier) string {
	return q.Pick(sqliteColumns, postgresColumns)
}

// readRows runs a statement that returns registration rows.
func readRows(ctx context.Context, q store.Querier, query string, args ...any) ([]*goapi.Object, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*goapi.Object{}
	for rows.Next() {
		cells := make([]any, len(columnNames))
		dest := make([]any, len(cells))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := goapi.NewObject()
		for i, name := range columnNames {
			row.Set(name, native(cells[i]))
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// readRow is readRows for at most one row; nil when there is none.
func readRow(ctx context.Context, q store.Querier, query string, args ...any) (*goapi.Object, error) {
	rows, err := readRows(ctx, q, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// native is a column value as the old console's driver gave it to
// JavaScript: numbers as numbers, text as text, a PostgreSQL time as the
// ISO text JSON.stringify wrote for its Date.
func native(v any) any {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case float64, string, bool:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return store.ISOTime(t)
	}
	return nil
}

// args binds values as the driver of the database did.
func args(q store.Querier, placeholders int, values ...jsbind.Value) ([]any, error) {
	return jsbind.Args(q.Dialect(), placeholders, values...)
}

// Filters are the filters of List. A value that is not truthy for
// JavaScript filters nothing.
type Filters struct {
	// Status keeps the rows with this status.
	Status jsbind.Value
	// Search keeps the rows whose device id, host name or address holds
	// its text; % and _ in it are wildcards.
	Search jsbind.Value
}

// List is getPendingRegistrations: the rows the filters keep, newest
// first.
func (s *Service) List(ctx context.Context, f Filters) ([]*goapi.Object, error) {
	where := "WHERE 1=1"
	var values []jsbind.Value
	placeholders := 0
	if f.Status.Truthy() {
		where += " AND status = ?"
		values = append(values, f.Status)
		placeholders++
	}
	if f.Search.Truthy() {
		like := jsbind.String("%" + f.Search.ToString() + "%")
		where += s.db.Pick(
			" AND (device_id LIKE ? OR hostname LIKE ? OR ip_address LIKE ?)",
			" AND (device_id ILIKE ? OR hostname ILIKE ? OR ip_address ILIKE ?)")
		values = append(values, like, like, like)
		placeholders += 3
	}
	bound, err := args(s.db, placeholders, values...)
	if err != nil {
		return nil, fmt.Errorf("list registrations: %w", err)
	}
	rows, err := readRows(ctx, s.db, "SELECT "+columns(s.db)+" FROM pending_registrations "+where+" ORDER BY created_at DESC", bound...)
	if err != nil {
		return nil, fmt.Errorf("list registrations: %w", err)
	}
	return rows, nil
}

// ByID is getPendingRegistrationById: the row with the id, nil when there
// is none. id is the number the route made of the address
// (parseInt(req.params.id, 10)).
func (s *Service) ByID(ctx context.Context, id jsbind.Value) (*goapi.Object, error) {
	bound, err := args(s.db, 1, id)
	if err != nil {
		return nil, fmt.Errorf("load registration: %w", err)
	}
	row, err := readRow(ctx, s.db, "SELECT "+columns(s.db)+" FROM pending_registrations WHERE id = ?", bound...)
	if err != nil {
		return nil, fmt.Errorf("load registration: %w", err)
	}
	return row, nil
}

// ByDeviceID is getPendingRegistrationByDeviceId: the row of the device,
// nil when there is none.
func (s *Service) ByDeviceID(ctx context.Context, deviceID string) (*goapi.Object, error) {
	return s.ByDeviceIDValue(ctx, jsbind.String(deviceID))
}

// ByDeviceIDValue is ByDeviceID for any value a request can carry: a list
// or an object from the query string is bound as the driver bound it.
func (s *Service) ByDeviceIDValue(ctx context.Context, deviceID jsbind.Value) (*goapi.Object, error) {
	bound, err := args(s.db, 1, deviceID)
	if err != nil {
		return nil, fmt.Errorf("load registration of a device: %w", err)
	}
	row, err := readRow(ctx, s.db, "SELECT "+columns(s.db)+" FROM pending_registrations WHERE device_id = ?", bound...)
	if err != nil {
		return nil, fmt.Errorf("load registration of a device: %w", err)
	}
	return row, nil
}

// Create is createPendingRegistration: a new pending row for a device
// without one, else the device's row as it is. A request never changes a
// row: the approver reviews the key, uuid and fields of the first
// request, and a rejected device stays rejected until someone with
// enrollment.manage deletes its row.
func (s *Service) Create(ctx context.Context, req Request) (*goapi.Object, error) {
	var row *goapi.Object
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, "SELECT id FROM pending_registrations WHERE device_id = ?", req.DeviceID).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return s.insert(ctx, tx, req, &row)
		case err != nil:
			return err
		}
		row, err = readRow(ctx, tx, "SELECT "+columns(tx)+" FROM pending_registrations WHERE id = ?", id)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create registration of device %q: %w", req.DeviceID, err)
	}
	return row, nil
}

func (s *Service) insert(ctx context.Context, tx *store.Tx, req Request, row **goapi.Object) error {
	insert := `INSERT INTO pending_registrations (device_id, hostname, platform, version, ip_address, public_key, uuid)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	values := []any{req.DeviceID, req.Hostname, req.Platform, req.Version, req.IPAddress, req.PublicKey, req.UUID}
	var err error
	if tx.Dialect() == store.Postgres {
		*row, err = readRow(ctx, tx, insert+" RETURNING "+postgresColumns, values...)
		return err
	}
	id, err := tx.Insert(ctx, insert, values...)
	if err != nil {
		return err
	}
	*row, err = readRow(ctx, tx, "SELECT "+sqliteColumns+" FROM pending_registrations WHERE id = ?", id)
	return err
}

// orNull is v || null for a text.
func orNull(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// Approve is approvePendingRegistration: the pending row with the id
// becomes approved by approvedBy ("admin" when empty) with the server
// configuration. It returns the row as it is now, and whether this call
// approved it: a row that was no longer pending (another approver was
// first) comes back unchanged, nil when it is gone.
func (s *Service) Approve(ctx context.Context, id jsbind.Value, approvedBy string, cfg ServerConfig) (*goapi.Object, bool, error) {
	if approvedBy == "" {
		approvedBy = "admin"
	}
	now := store.NowSQL(s.db.Dialect())
	update := `UPDATE pending_registrations
		SET status = 'approved', approved_by = ?, approved_at = ` + now + `,
			access_token = ?, console_url = ?, server_address = ?, server_key = ?,
			updated_at = ` + now + `
		WHERE id = ? AND status = 'pending'`
	row, changed, err := s.change(ctx, update, id,
		approvedBy, orNull(cfg.AccessToken), orNull(cfg.ConsoleURL), orNull(cfg.ServerAddress), orNull(cfg.ServerKey))
	if err != nil {
		return nil, false, fmt.Errorf("approve registration: %w", err)
	}
	return row, changed, nil
}

// Reject is rejectPendingRegistration: the pending row with the id becomes
// rejected with the reason. It returns what Approve returns.
func (s *Service) Reject(ctx context.Context, id jsbind.Value, reason string) (*goapi.Object, bool, error) {
	update := `UPDATE pending_registrations
		SET status = 'rejected', rejected_reason = ?, updated_at = ` + store.NowSQL(s.db.Dialect()) + `
		WHERE id = ? AND status = 'pending'`
	row, changed, err := s.change(ctx, update, id, reason)
	if err != nil {
		return nil, false, fmt.Errorf("reject registration: %w", err)
	}
	return row, changed, nil
}

// change runs an UPDATE whose last placeholder is the id, and returns the
// row after it and whether the UPDATE changed it, on both databases alike.
func (s *Service) change(ctx context.Context, update string, id jsbind.Value, values ...any) (*goapi.Object, bool, error) {
	bound, err := args(s.db, 1, id)
	if err != nil {
		return nil, false, err
	}
	values = append(values, bound...)
	var row *goapi.Object
	changed := false
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		res, err := tx.Exec(ctx, update, values...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		changed = n > 0
		row, err = readRow(ctx, tx, "SELECT "+columns(tx)+" FROM pending_registrations WHERE id = ?", bound...)
		return err
	})
	return row, changed, err
}

// Delete is deletePendingRegistration: it reports whether a row went.
func (s *Service) Delete(ctx context.Context, id jsbind.Value) (bool, error) {
	bound, err := args(s.db, 1, id)
	if err != nil {
		return false, fmt.Errorf("delete registration: %w", err)
	}
	res, err := s.db.Exec(ctx, "DELETE FROM pending_registrations WHERE id = ?", bound...)
	if err != nil {
		return false, fmt.Errorf("delete registration: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete registration: %w", err)
	}
	return n > 0, nil
}

// PendingCount is getPendingRegistrationCount: the number of pending rows.
func (s *Service) PendingCount(ctx context.Context) (float64, error) {
	var n int64
	query := s.db.Pick(
		"SELECT COUNT(*) AS count FROM pending_registrations WHERE status = 'pending'",
		"SELECT COUNT(*)::INTEGER AS count FROM pending_registrations WHERE status = 'pending'")
	if err := s.db.QueryRow(ctx, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending registrations: %w", err)
	}
	return float64(n), nil
}
