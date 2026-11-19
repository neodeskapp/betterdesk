package automation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries take request values as they came (jsbind.Value) and bind
// them as the old console's driver for the database did. So a number for
// a text column is stored as "5.0" on SQLite and as "5" on PostgreSQL, a
// list gives SQLite one value per element, and text PostgreSQL cannot read
// as a number fails there and is stored as text on SQLite.

// record is a row as the old console's driver returned it: the columns
// in the order of the table, each with the value that goes into JSON.
//
// On SQLite a column goes out with whatever it holds: a column declared
// INTEGER or REAL can hold fractions and text. On PostgreSQL a time is an
// ISO text with milliseconds, a flag true or false, and condition_value
// the number its column holds: eight bytes since store.Migrate widened
// the Node console's four-byte real (BUGS AE4).
type record struct {
	columns []string
	values  []any // each a json.Marshaler and an sql.Scanner
}

// MarshalJSON writes the row as an object.
func (r record) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, column := range r.columns {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsjson.AppendQuoted(nil, column))
		b.WriteByte(':')
		value, err := r.values[i].(json.Marshaler).MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// number returns a column as the JavaScript value the old console had for
// it: an id is a number.
func (r record) number(column string) jsbind.Value {
	for i, name := range r.columns {
		if name == column {
			if cell, ok := r.values[i].(*jsbind.Cell); ok {
				return cell.Value()
			}
		}
	}
	return jsbind.Undefined
}

// text returns a column as JavaScript prints it inside a text.
func (r record) text(column string) string { return r.number(column).ToString() }

// query is a statement that is put together from parts, with the values
// for its placeholders.
type query struct {
	text   strings.Builder
	values []jsbind.Value
}

// add appends a part and the values of its placeholders.
func (q *query) add(part string, values ...jsbind.Value) {
	q.text.WriteString(part)
	q.values = append(q.values, values...)
}

// args returns the statement and its parameters as the driver bound them.
func (q *query) args(db *store.DB) (string, []any, error) {
	text := q.text.String()
	args, err := jsbind.Args(db.Dialect(), strings.Count(text, "?"), q.values...)
	return text, args, err
}

// exec runs the statement.
func (q *query) exec(ctx context.Context, db *store.DB, doing string) error {
	_, err := q.changed(ctx, db, doing)
	return err
}

// changed runs the statement and returns the number of rows it changed.
func (q *query) changed(ctx context.Context, db *store.DB, doing string) (int64, error) {
	text, args, err := q.args(db)
	var n int64
	if err == nil {
		var result sql.Result
		if result, err = db.Exec(ctx, text, args...); err == nil {
			n, err = result.RowsAffected()
		}
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", doing, err)
	}
	return n, nil
}

// rows reads the rows of the statement.
func (q *query) rows(ctx context.Context, db *store.DB, doing string) ([]record, error) {
	text, args, err := q.args(db)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	rows, err := store.Select(ctx, db, text, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	out := []record{}
	for rows.Next() {
		values := make([]any, len(columns))
		for i := range values {
			values[i] = new(jsbind.Cell)
			if !db.IsPostgres() {
				continue
			}
			switch strings.ToUpper(types[i].DatabaseTypeName()) {
			case "TIMESTAMPTZ", "TIMESTAMP", "DATE":
				values[i] = new(store.Time)
			case "INT8", "NUMERIC":
				values[i] = new(store.Int64String)
			case "FLOAT4", "FLOAT8":
				values[i] = new(store.Float)
			}
		}
		if err := rows.Scan(values...); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
		out = append(out, record{columns: columns, values: values})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return out, nil
}

// row reads the first row of the statement, nil when it has none.
func (q *query) row(ctx context.Context, db *store.DB, doing string) (*record, error) {
	rows, err := q.rows(ctx, db, doing)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// listRules returns every alert rule, the one created last first, and of
// rules created in the same second the one with the higher id.
func listRules(ctx context.Context, db *store.DB) ([]record, error) {
	var q query
	q.add("SELECT * FROM alert_rules WHERE 1=1 ORDER BY created_at DESC, id DESC")
	return q.rows(ctx, db, "list alert rules")
}

// ruleByID returns the rule with the id, nil when there is none.
func ruleByID(ctx context.Context, db *store.DB, id jsbind.Value) (*record, error) {
	var q query
	q.add("SELECT * FROM alert_rules WHERE id = ?", id)
	return q.row(ctx, db, "load alert rule "+id.ToString())
}

// newRule is what a new rule is made from, with the defaults of the route
// filled in already.
type newRule struct {
	Name, Description, ConditionType, ConditionOp, ConditionValue jsbind.Value
	Severity, ScopeDeviceID, CooldownSecs, NotifyEmails           jsbind.Value
	Enabled                                                       bool
	CreatedBy                                                     string
}

// createRule inserts a rule and returns it as it is stored. A condition
// value that is empty text is stored as 0.
func createRule(ctx context.Context, db *store.DB, rule newRule) (*record, error) {
	var q query
	q.add(`INSERT INTO alert_rules (name, description, enabled, condition_type, condition_op, condition_value,
                    severity, scope_device_id, cooldown_secs, notify_emails, created_by)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.Name, rule.Description, flag(db, rule.Enabled),
		rule.ConditionType, rule.ConditionOp, rule.ConditionValue.Or(jsbind.Number(0)),
		rule.Severity, rule.ScopeDeviceID,
		rule.CooldownSecs, rule.NotifyEmails, jsbind.String(rule.CreatedBy))
	return insertRow(ctx, db, &q, "alert_rules", "create alert rule")
}

// insertRow runs the INSERT of q and returns the new row of the table: on
// PostgreSQL through RETURNING, on SQLite read back by its id, so both
// answer with what is stored.
func insertRow(ctx context.Context, db *store.DB, q *query, table, doing string) (*record, error) {
	if db.IsPostgres() {
		q.add(" RETURNING *")
		return q.row(ctx, db, doing)
	}
	text, args, err := q.args(db)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	id, err := db.Insert(ctx, text, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	var read query
	read.add("SELECT * FROM "+table+" WHERE id = ?", jsbind.Number(float64(id)))
	return read.row(ctx, db, doing)
}

// change is one column an update sets.
type change struct {
	column string
	value  jsbind.Value
}

// updateRule sets the given columns of a rule and the time of the change.
// Without changes it does nothing.
func updateRule(ctx context.Context, db *store.DB, id jsbind.Value, changes []change) error {
	if len(changes) == 0 {
		return nil
	}
	var q query
	q.add("UPDATE alert_rules SET ")
	for _, c := range changes {
		q.add(c.column+" = ?, ", c.value)
	}
	q.add("updated_at = "+store.NowSQL(db.Dialect())+" WHERE id = ?", id)
	return q.exec(ctx, db, "update alert rule "+id.ToString())
}

// deleteRule removes a rule; the database removes its alerts with it.
func deleteRule(ctx context.Context, db *store.DB, id jsbind.Value) error {
	var q query
	q.add("DELETE FROM alert_rules WHERE id = ?", id)
	return q.exec(ctx, db, "delete alert rule "+id.ToString())
}

// alertFilter narrows the alert history. A text that is
// "" does not filter.
type alertFilter struct {
	DeviceID, Severity string
	// Acknowledged is the flag to look for, undefined for both
	Acknowledged jsbind.Value
	// Limit is the number of rows, 0 for all
	Limit int
}

// listAlerts returns the alert history with the name of each alert's
// rule, the alert triggered last first.
func listAlerts(ctx context.Context, db *store.DB, filter alertFilter) ([]record, error) {
	var q query
	q.add("SELECT h.*, r.name as rule_name FROM alert_history h LEFT JOIN alert_rules r ON h.rule_id = r.id WHERE 1=1")
	if filter.DeviceID != "" {
		q.add(" AND h.device_id = ?", jsbind.String(filter.DeviceID))
	}
	if filter.Severity != "" {
		q.add(" AND h.severity = ?", jsbind.String(filter.Severity))
	}
	if !filter.Acknowledged.IsUndefined() {
		q.add(" AND h.acknowledged = ?", filter.Acknowledged)
	}
	q.add(" ORDER BY h.triggered_at DESC")
	if filter.Limit > 0 {
		q.add(" LIMIT ?", jsbind.Number(float64(filter.Limit)))
	}
	return q.rows(ctx, db, "list the alert history")
}

// acknowledgeAlert marks the alert as acknowledged by username, now,
// unless it is acknowledged already: the first acknowledgement stays.
// found is false when no alert has the id.
func acknowledgeAlert(ctx context.Context, db *store.DB, id jsbind.Value, username string) (found bool, err error) {
	var q query
	q.add("UPDATE alert_history SET acknowledged = "+db.Pick("1", "TRUE")+", acknowledged_by = ?, acknowledged_at = "+store.NowSQL(db.Dialect())+
		" WHERE id = ? AND "+db.Pick("COALESCE(acknowledged, 0) = 0", "NOT COALESCE(acknowledged, FALSE)"),
		jsbind.String(username), id)
	n, err := q.changed(ctx, db, "acknowledge alert "+id.ToString())
	if err != nil || n > 0 {
		return n > 0, err
	}
	var exists query
	exists.add("SELECT id FROM alert_history WHERE id = ?", id)
	row, err := exists.row(ctx, db, "load alert "+id.ToString())
	return row != nil, err
}

// newCommand is what a command for a device is made from.
type newCommand struct {
	DeviceID    jsbind.Value
	CommandType string
	Payload     string
	CreatedBy   string
}

// createCommand inserts a pending command and returns it as it is stored.
func createCommand(ctx context.Context, db *store.DB, command newCommand) (*record, error) {
	var q query
	q.add(`INSERT INTO remote_commands (device_id, command_type, payload, status, created_by)
                VALUES (?, ?, ?, 'pending', ?)`,
		command.DeviceID, jsbind.String(command.CommandType), jsbind.String(command.Payload), jsbind.String(command.CreatedBy))
	return insertRow(ctx, db, &q, "remote_commands", "create remote command")
}

// commandFilter narrows the command list. A text that is
// "" does not filter.
type commandFilter struct {
	DeviceID, Status string
	// Limit is the number of rows, 0 for all
	Limit int
}

// listCommands returns the commands, the one created last first, and of
// commands created in the same second the one with the higher id.
func listCommands(ctx context.Context, db *store.DB, filter commandFilter) ([]record, error) {
	var q query
	q.add("SELECT * FROM remote_commands WHERE 1=1")
	if filter.DeviceID != "" {
		q.add(" AND device_id = ?", jsbind.String(filter.DeviceID))
	}
	if filter.Status != "" {
		q.add(" AND status = ?", jsbind.String(filter.Status))
	}
	q.add(" ORDER BY created_at DESC, id DESC")
	if filter.Limit > 0 {
		q.add(" LIMIT ?", jsbind.Number(float64(filter.Limit)))
	}
	return q.rows(ctx, db, "list remote commands")
}

// commandByID returns the command with the id, nil when there is none.
func commandByID(ctx context.Context, db *store.DB, id jsbind.Value) (*record, error) {
	var q query
	q.add("SELECT * FROM remote_commands WHERE id = ?", id)
	return q.row(ctx, db, "load remote command "+id.ToString())
}

// pendingCommandsOf is getPendingCommands(deviceId): the device's
// commands that wait for its agent, the one created first first, and of
// commands created in the same second the one with the lower id.
func pendingCommandsOf(ctx context.Context, db *store.DB, device string) ([]record, error) {
	var q query
	q.add("SELECT * FROM remote_commands WHERE device_id = ? AND status = 'pending' ORDER BY created_at ASC, id ASC", jsbind.String(device))
	return q.rows(ctx, db, "list the pending commands of device "+device)
}

// updateRemoteCommand records an agent's report on a command that is
// pending or running: the status and the result are always set, the time
// it began running with "running", and the time it ended with
// "completed" and "failed" unless it has one. changed is false when the
// command is pending or running no more.
func updateRemoteCommand(ctx context.Context, db *store.DB, id jsbind.Value, status string, result jsbind.Value) (changed bool, err error) {
	var q query
	q.add("UPDATE remote_commands SET status = ?, result = ?", jsbind.String(status), result)
	switch status {
	case "running":
		q.add(", executed_at = " + store.NowSQL(db.Dialect()))
	case "completed", "failed":
		q.add(", completed_at = COALESCE(completed_at, " + store.NowSQL(db.Dialect()) + ")")
	}
	q.add(" WHERE id = ? AND status IN ('pending', 'running')", id)
	n, err := q.changed(ctx, db, "update remote command "+id.ToString())
	return n > 0, err
}
