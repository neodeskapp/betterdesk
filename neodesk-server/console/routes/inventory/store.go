package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// inventory is a device's last report as the old console read it from
// device_inventory: the hardware and software documents parsed, the
// times as the database gives them (text on SQLite, an instant on
// PostgreSQL).
type inventory struct {
	deviceID    jsbind.Cell
	hardware    jsjson.Value
	software    jsjson.Value
	collectedAt any
	receivedAt  any
	// unreadable says why a document of the row is no JSON; that document
	// is then an empty object.
	unreadable error
}

// timeDest is where a time column is read into: on SQLite whatever the
// column holds, on PostgreSQL the instant, which goes out in ISO form.
func timeDest(db *store.DB) any {
	if db.IsPostgres() {
		return new(store.Time)
	}
	return new(jsbind.Cell)
}

// parseDocument reads a hardware or software column as the old console
// did. It fails where JSON.parse threw.
//
// A text column (SQLite) holds text that is parsed, and NULL or the
// empty text count as an empty object. A JSON column (PostgreSQL) holds
// a value: null is null, and so is NULL; 0, false and the empty text
// count as an empty object; other text is parsed; and every other value
// (an object, a list, a number, true) is itself.
func parseDocument(column sql.NullString, document bool) (jsjson.Value, error) {
	text := column.String
	if document {
		if !column.Valid {
			return jsjson.NullValue, nil
		}
		value, err := jsjson.Parse(text)
		if err != nil {
			return jsjson.Value{}, fmt.Errorf("read the document: %w", err)
		}
		switch {
		case value.Kind() == jsjson.Null:
			return value, nil
		case !value.Truthy():
			text = ""
		case value.Kind() == jsjson.String:
			text = value.Str()
		default:
			return value, nil
		}
	}
	if text == "" {
		text = "{}"
	}
	return jsjson.Parse(text)
}

// scanInventories reads the rows of a SELECT of the five inventory
// columns. A row whose documents are no JSON is read with the
// unreadable one empty, and says why.
func scanInventories(db *store.DB, rows *store.Rows) ([]inventory, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	isDocument := func(i int) bool { return strings.HasPrefix(strings.ToUpper(types[i].DatabaseTypeName()), "JSON") }
	out := []inventory{}
	for rows.Next() {
		inv := inventory{collectedAt: timeDest(db), receivedAt: timeDest(db)}
		var hardware, software sql.NullString
		if err := rows.Scan(&inv.deviceID, &hardware, &software, inv.collectedAt, inv.receivedAt); err != nil {
			return nil, err
		}
		var hwErr, swErr error
		if inv.hardware, hwErr = parseDocument(hardware, isDocument(1)); hwErr != nil {
			inv.hardware, hwErr = emptyObject(), fmt.Errorf("hardware of device %s: %w", inv.deviceID.Value().ToString(), hwErr)
		}
		if inv.software, swErr = parseDocument(software, isDocument(2)); swErr != nil {
			inv.software, swErr = emptyObject(), fmt.Errorf("software of device %s: %w", inv.deviceID.Value().ToString(), swErr)
		}
		inv.unreadable = errors.Join(hwErr, swErr)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// emptyObject is {}.
func emptyObject() jsjson.Value {
	v, _ := jsjson.Parse("{}")
	return v
}

const inventoryColumns = "device_id, hardware, software, collected_at, received_at"

// allInventories returns the last report of every device, the one
// received last first.
func allInventories(ctx context.Context, db *store.DB) ([]inventory, error) {
	rows, err := store.Select(ctx, db, "SELECT "+inventoryColumns+" FROM device_inventory ORDER BY received_at DESC")
	if err != nil {
		return nil, fmt.Errorf("list inventories: %w", err)
	}
	defer rows.Close()
	inventories, err := scanInventories(db, rows)
	if err != nil {
		return nil, fmt.Errorf("list inventories: %w", err)
	}
	return inventories, nil
}

// inventoryOf returns the last report of a device, nil when it has sent
// none.
func inventoryOf(ctx context.Context, db *store.DB, id string) (*inventory, error) {
	rows, err := store.Select(ctx, db, "SELECT "+inventoryColumns+" FROM device_inventory WHERE device_id = ?", id)
	if err != nil {
		return nil, fmt.Errorf("load the inventory of device %s: %w", id, err)
	}
	defer rows.Close()
	inventories, err := scanInventories(db, rows)
	if err == nil && len(inventories) > 0 {
		err = inventories[0].unreadable
	}
	if err != nil {
		return nil, fmt.Errorf("load the inventory of device %s: %w", id, err)
	}
	if len(inventories) == 0 {
		return nil, nil
	}
	return &inventories[0], nil
}

// telemetry is a device's last telemetry.
//
// On SQLite every column goes out with whatever it holds. On PostgreSQL
// the four numbers are numbers also where the driver handed over text (a
// bigint), and null where the column is NULL, as on SQLite.
type telemetry struct {
	DeviceID         jsbind.Cell `json:"device_id"`
	CPUUsagePercent  any         `json:"cpu_usage_percent"`
	MemoryUsedBytes  any         `json:"memory_used_bytes"`
	MemoryTotalBytes any         `json:"memory_total_bytes"`
	UptimeSecs       any         `json:"uptime_secs"`
	Timestamp        any         `json:"timestamp"`
	ReceivedAt       any         `json:"received_at"`
}

// plus is a number column of PostgreSQL as a number: null for NULL. A
// real goes out with the digits of a four-byte number.
type plus struct {
	real  store.Float
	whole jsbind.Cell
	// isReal says which of the two the column was read into.
	isReal bool
}

// MarshalJSON implements json.Marshaler.
func (p *plus) MarshalJSON() ([]byte, error) {
	switch {
	case p.isReal && !p.real.Valid, !p.isReal && p.whole.Value().IsNull():
		return []byte("null"), nil
	case p.isReal:
		return p.real.MarshalJSON()
	}
	return number(p.whole.Value().ToNumber()).MarshalJSON()
}

// telemetryOf returns the last telemetry of a device, nil when it has
// sent none.
func telemetryOf(ctx context.Context, db *store.DB, id string) (*telemetry, error) {
	t := telemetry{Timestamp: timeDest(db), ReceivedAt: timeDest(db)}
	numbers := []*any{&t.CPUUsagePercent, &t.MemoryUsedBytes, &t.MemoryTotalBytes, &t.UptimeSecs}
	dest := []any{&t.DeviceID}
	for i, field := range numbers {
		if !db.IsPostgres() {
			*field = new(jsbind.Cell)
			dest = append(dest, *field)
			continue
		}
		p := &plus{isReal: i == 0}
		*field = p
		if p.isReal {
			dest = append(dest, &p.real)
		} else {
			dest = append(dest, &p.whole)
		}
	}
	dest = append(dest, t.Timestamp, t.ReceivedAt)
	rows, err := store.Select(ctx, db,
		`SELECT device_id, cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs, "timestamp", received_at FROM device_telemetry WHERE device_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("load the telemetry of device %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("load the telemetry of device %s: %w", id, err)
		}
		return nil, nil
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, fmt.Errorf("load the telemetry of device %s: %w", id, err)
	}
	return &t, rows.Close()
}

// upsertInventory is upsertInventory(deviceId, hardware, software,
// collectedAt): the device's report replaces the one it had, with the
// time of arrival. The two documents are stored as JSON.stringify wrote
// them, software || {} for the software, and collectedAt as the driver
// bound it.
//
// SQLite keeps the documents as text and a number given as the time as
// text like "5.0". PostgreSQL keeps the documents in jsonb columns, which
// refuse the character U+0000 and half of a surrogate pair, and the time
// in a timestamptz column, which refuses what is no time.
func upsertInventory(ctx context.Context, db *store.DB, id string, hardware, software jsjson.Value, collectedAt jsbind.Value) error {
	hw, _ := hardware.MarshalJSON()
	sw := []byte("{}")
	if software.Truthy() {
		sw, _ = software.MarshalJSON()
	}
	query := db.Pick(`
                INSERT INTO device_inventory (device_id, hardware, software, collected_at, received_at)
                VALUES (?, ?, ?, ?, datetime('now'))
                ON CONFLICT(device_id) DO UPDATE SET
                    hardware = excluded.hardware,
                    software = excluded.software,
                    collected_at = excluded.collected_at,
                    received_at = datetime('now')`, `
                INSERT INTO device_inventory (device_id, hardware, software, collected_at, received_at)
                VALUES ($1, $2, $3, $4, NOW())
                ON CONFLICT(device_id) DO UPDATE SET
                    hardware = $2, software = $3, collected_at = $4, received_at = NOW()`)
	args, err := jsbind.Args(db.Dialect(), 4, jsbind.String(id), jsbind.String(string(hw)), jsbind.String(string(sw)), collectedAt)
	if err == nil {
		_, err = db.Exec(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("store the inventory of device %s: %w", id, err)
	}
	return nil
}

// deviceTelemetry is what upsertTelemetry stores: the values of the
// request with the route's defaults filled in.
type deviceTelemetry struct {
	CPUUsagePercent, MemoryUsedBytes, MemoryTotalBytes, UptimeSecs, Timestamp jsbind.Value
}

// upsertTelemetry is upsertTelemetry(deviceId, data): the device's load
// replaces the one it had, with the time of arrival. The values are bound
// as the driver bound them: SQLite keeps whatever it is given (text in a
// number column, a fraction in an integer column), PostgreSQL refuses
// what its column type cannot read.
func upsertTelemetry(ctx context.Context, db *store.DB, id string, t deviceTelemetry) error {
	query := db.Pick(`
                INSERT INTO device_telemetry (device_id, cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs, timestamp, received_at)
                VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
                ON CONFLICT(device_id) DO UPDATE SET
                    cpu_usage_percent = excluded.cpu_usage_percent,
                    memory_used_bytes = excluded.memory_used_bytes,
                    memory_total_bytes = excluded.memory_total_bytes,
                    uptime_secs = excluded.uptime_secs,
                    timestamp = excluded.timestamp,
                    received_at = datetime('now')`, `
                INSERT INTO device_telemetry (device_id, cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs, timestamp, received_at)
                VALUES ($1, $2, $3, $4, $5, $6, NOW())
                ON CONFLICT(device_id) DO UPDATE SET
                    cpu_usage_percent = $2, memory_used_bytes = $3, memory_total_bytes = $4,
                    uptime_secs = $5, timestamp = $6, received_at = NOW()`)
	args, err := jsbind.Args(db.Dialect(), 6, jsbind.String(id), t.CPUUsagePercent, t.MemoryUsedBytes, t.MemoryTotalBytes, t.UptimeSecs, t.Timestamp)
	if err == nil {
		_, err = db.Exec(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("store the telemetry of device %s: %w", id, err)
	}
	return nil
}
