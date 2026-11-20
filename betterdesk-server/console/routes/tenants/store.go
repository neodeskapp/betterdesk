package tenants

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
// them as the old console's driver for the database did. So a tenant id
// that is not a whole number finds nothing on SQLite and is an error on
// PostgreSQL, and a text field given a number holds "5.0" on SQLite and
// "5" on PostgreSQL.

// tenant is a row of tenants, in the order of the table's columns.
type tenant struct {
	ID           jsbind.Cell      `json:"id"`
	Name         string           `json:"name"`
	Slug         string           `json:"slug"`
	ContactName  store.NullString `json:"contact_name"`
	ContactEmail store.NullString `json:"contact_email"`
	MaxDevices   jsbind.Cell      `json:"max_devices"`
	Notes        store.NullString `json:"notes"`
	Active       store.Bool       `json:"active"`
	CreatedAt    store.Time       `json:"created_at"`
	UpdatedAt    store.Time       `json:"updated_at"`
}

const tenantColumns = "id, name, slug, contact_name, contact_email, max_devices, notes, active, created_at, updated_at"

func (t *tenant) dest() []any {
	return []any{&t.ID, &t.Name, &t.Slug, &t.ContactName, &t.ContactEmail, &t.MaxDevices, &t.Notes, &t.Active, &t.CreatedAt, &t.UpdatedAt}
}

// listTenants returns every tenant, by name.
func listTenants(ctx context.Context, db *store.DB) ([]tenant, error) {
	rows, err := store.Select(ctx, db, "SELECT "+tenantColumns+" FROM tenants ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()
	tenants := []tenant{}
	for rows.Next() {
		var t tenant
		if err := rows.Scan(t.dest()...); err != nil {
			return nil, fmt.Errorf("read a tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	return tenants, nil
}

// tenantByID returns the tenant with the id, nil when there is none.
func tenantByID(ctx context.Context, db *store.DB, id jsbind.Value) (*tenant, error) {
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return nil, fmt.Errorf("load tenant %s: %w", id.ToString(), err)
	}
	return oneTenant(store.SelectRow(ctx, db, "SELECT "+tenantColumns+" FROM tenants WHERE id = ?", args...), "load tenant "+id.ToString())
}

func oneTenant(row *store.Row, doing string) (*tenant, error) {
	var t tenant
	switch err := row.Scan(t.dest()...); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return &t, nil
}

// newTenant is what a new tenant is made from. Empty values have been
// replaced by their defaults already.
type newTenant struct {
	Name, Slug, ContactName, ContactEmail, MaxDevices, Notes jsbind.Value
}

// createTenant inserts a tenant, active, and returns its row.
func createTenant(ctx context.Context, db *store.DB, data newTenant) (*tenant, error) {
	const insert = "INSERT INTO tenants (name, slug, contact_name, contact_email, max_devices, notes, active) VALUES (?, ?, ?, ?, ?, ?, ?)"
	active := jsbind.Number(1)
	if db.IsPostgres() {
		active = jsbind.Bool(true)
	}
	args, err := jsbind.Args(db.Dialect(), 7, data.Name, data.Slug, data.ContactName, data.ContactEmail, data.MaxDevices, data.Notes, active)
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	if db.IsPostgres() {
		return oneTenant(store.SelectRow(ctx, db, insert+" RETURNING "+tenantColumns, args...), "create tenant")
	}
	res, err := db.Exec(ctx, insert, args...)
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	return tenantByID(ctx, db, jsbind.Number(float64(id)))
}

// tenantChange is one column an update sets.
type tenantChange struct {
	column string
	value  jsbind.Value
}

// updateTenant sets the given columns and the time of the change, and
// returns the row as it is afterwards: nil when the tenant is gone.
// Without changes it only reads the row.
func updateTenant(ctx context.Context, db *store.DB, id jsbind.Value, changes []tenantChange) (*tenant, error) {
	if len(changes) == 0 {
		return tenantByID(ctx, db, id)
	}
	sets := make([]string, 0, len(changes)+1)
	values := make([]jsbind.Value, 0, len(changes)+1)
	for _, change := range changes {
		sets = append(sets, change.column+" = ?")
		values = append(values, change.value)
	}
	sets = append(sets, "updated_at = "+store.NowSQL(db.Dialect()))
	values = append(values, id)
	args, err := jsbind.Args(db.Dialect(), len(values), values...)
	if err != nil {
		return nil, fmt.Errorf("update tenant %s: %w", id.ToString(), err)
	}
	update := "UPDATE tenants SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	if db.IsPostgres() {
		return oneTenant(store.SelectRow(ctx, db, update+" RETURNING "+tenantColumns, args...), "update tenant "+id.ToString())
	}
	if _, err := db.Exec(ctx, update, args...); err != nil {
		return nil, fmt.Errorf("update tenant %s: %w", id.ToString(), err)
	}
	return tenantByID(ctx, db, id)
}

// deleteWhere runs a DELETE and reports whether it removed a row.
func deleteWhere(ctx context.Context, db *store.DB, doing, query string, values ...jsbind.Value) (bool, error) {
	args, err := jsbind.Args(db.Dialect(), len(values), values...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	res, err := db.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	return n > 0, nil
}

// deleteTenant removes a tenant; its assignments go with it.
func deleteTenant(ctx context.Context, db *store.DB, id jsbind.Value) (bool, error) {
	return deleteWhere(ctx, db, "delete tenant "+id.ToString(), "DELETE FROM tenants WHERE id = ?", id)
}

// tenantDevice is a device assigned to a tenant, with what the console's
// device table knows about it.
type tenantDevice struct {
	DeviceID   string     `json:"device_id"`
	AssignedAt store.Time `json:"assigned_at"`
	Online     bool       `json:"online"`
	Hostname   string     `json:"hostname"`
	IP         string     `json:"ip"`
}

// tenantDevices lists a tenant's devices, newest assignment first. A
// device the console does not know, or has deleted, is listed without
// name and address.
func tenantDevices(ctx context.Context, db *store.DB, tenantID jsbind.Value) ([]tenantDevice, error) {
	doing := "list the devices of tenant " + tenantID.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, tenantID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	rows, err := store.Select(ctx, db, `
		SELECT td.device_id, td.assigned_at, p.note, p.status_online, p.ip
		FROM tenant_devices td
		LEFT JOIN peer p ON p.id = td.device_id AND `+db.Pick("(p.is_deleted = 0 OR p.is_deleted IS NULL)", "(NOT p.is_deleted OR p.is_deleted IS NULL)")+`
		WHERE td.tenant_id = ?
		ORDER BY td.assigned_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	defer rows.Close()
	devices := []tenantDevice{}
	for rows.Next() {
		var d tenantDevice
		var note, ip store.NullString
		var online jsbind.Cell
		if err := rows.Scan(&d.DeviceID, &d.AssignedAt, &note, &online, &ip); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
		d.Hostname, d.IP = note.OrEmpty(), ip.OrEmpty()
		// SQLite: status_online === 1; PostgreSQL: !!status_online
		if db.IsPostgres() {
			d.Online = online.Value().Truthy()
		} else {
			d.Online = online.Value().ToNumber() == 1
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return devices, nil
}

// assign inserts an assignment and reports whether it is new. A row that
// is there already stays as it is, with its id and the time of the first
// assignment.
func assign(ctx context.Context, db *store.DB, table, column string, tenantID, other jsbind.Value) (bool, error) {
	doing := "assign to tenant " + tenantID.ToString()
	args, err := jsbind.Args(db.Dialect(), 2, tenantID, other)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	res, err := db.Exec(ctx, "INSERT INTO "+table+" (tenant_id, "+column+") VALUES (?, ?) ON CONFLICT (tenant_id, "+column+") DO NOTHING", args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	return n > 0, nil
}

// assignDevice assigns a device to a tenant and reports whether the
// tenant did not have it before.
func assignDevice(ctx context.Context, db *store.DB, tenantID jsbind.Value, deviceID string) (bool, error) {
	return assign(ctx, db, "tenant_devices", "device_id", tenantID, jsbind.String(deviceID))
}

// removeDevice takes a device from a tenant.
func removeDevice(ctx context.Context, db *store.DB, tenantID jsbind.Value, deviceID string) (bool, error) {
	return deleteWhere(ctx, db, "remove a device from tenant "+tenantID.ToString(),
		"DELETE FROM tenant_devices WHERE tenant_id = ? AND device_id = ?", tenantID, jsbind.String(deviceID))
}

// tenantUser is a user assigned to a tenant.
type tenantUser struct {
	UserID     jsbind.Cell `json:"user_id"`
	AssignedAt store.Time  `json:"assigned_at"`
}

// tenantUsers lists a tenant's users, newest assignment first.
func tenantUsers(ctx context.Context, db *store.DB, tenantID jsbind.Value) ([]tenantUser, error) {
	doing := "list the users of tenant " + tenantID.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, tenantID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	rows, err := store.Select(ctx, db, `
		SELECT tu.user_id, tu.assigned_at
		FROM tenant_users tu
		WHERE tu.tenant_id = ?
		ORDER BY tu.assigned_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	defer rows.Close()
	users := []tenantUser{}
	for rows.Next() {
		var u tenantUser
		if err := rows.Scan(&u.UserID, &u.AssignedAt); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return users, nil
}

// assignUser assigns a user to a tenant. Every failure is false.
func assignUser(ctx context.Context, db *store.DB, tenantID, userID jsbind.Value) bool {
	_, err := assign(ctx, db, "tenant_users", "user_id", tenantID, userID)
	return err == nil
}

// removeUser takes a user from a tenant.
func removeUser(ctx context.Context, db *store.DB, tenantID, userID jsbind.Value) (bool, error) {
	return deleteWhere(ctx, db, "remove a user from tenant "+tenantID.ToString(),
		"DELETE FROM tenant_users WHERE tenant_id = ? AND user_id = ?", tenantID, userID)
}
