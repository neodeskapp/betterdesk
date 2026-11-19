package dataguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries take request values as they came (jsbind.Value) and bind
// them as the old console's driver for the database did.
//
// A policy goes out with its columns in one order (policyColumns), also
// from a table made before policy_type, action and scope were added,
// which has those three at its end. Events go out in the order of their
// table.

// policyColumns are the columns of a policy in the order they go out.
const policyColumns = "id, name, description, policy_type, action, scope, enabled, rules, created_at, updated_at"

// row is a row with its columns in order, each as it goes into JSON.
type row struct {
	columns []string
	values  []json.RawMessage
}

// MarshalJSON writes the row as an object.
func (r row) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, column := range r.columns {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(column)
		b.Write(name)
		b.WriteByte(':')
		b.Write(r.values[i])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// value returns the JSON of a column, nil when the row has no such column.
func (r row) value(column string) json.RawMessage {
	for i, name := range r.columns {
		if name == column {
			return r.values[i]
		}
	}
	return nil
}

// set replaces the JSON of a column that is there.
func (r row) set(column string, value json.RawMessage) {
	for i, name := range r.columns {
		if name == column {
			r.values[i] = value
		}
	}
}

// columnKind says how a column is read: as a time, a flag, a JSON
// document, or by whatever value it holds.
type columnKind int

const (
	plain columnKind = iota
	timeColumn
	flagColumn
	documentColumn
)

var columnKinds = map[string]columnKind{
	"created_at": timeColumn, "updated_at": timeColumn,
	"enabled": flagColumn,
	"rules":   documentColumn, "details": documentColumn,
}

// document is a JSON column as the driver handed it over: NULL, the text
// of a text column, or the value of a PostgreSQL jsonb column.
type document struct {
	text  string
	null  bool
	jsonb bool
}

// selectRows reads the rows of a SELECT * with every column as JSON. A
// document column holds the document for the routes to parse.
func selectRows(ctx context.Context, db *store.DB, query string, args ...any) ([]row, []map[string]document, error) {
	rows, err := store.Select(ctx, db, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	out, documents := []row{}, []map[string]document{}
	for rows.Next() {
		dest := make([]any, len(columns))
		for i, column := range columns {
			switch columnKinds[column] {
			case timeColumn:
				dest[i] = new(store.Time)
			case flagColumn:
				dest[i] = new(store.Bool)
			case documentColumn:
				dest[i] = new(store.NullString)
			default:
				dest[i] = new(jsbind.Cell)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		r := row{columns: columns, values: make([]json.RawMessage, len(columns))}
		docs := map[string]document{}
		for i, column := range columns {
			if text, isDocument := dest[i].(*store.NullString); isDocument {
				docs[column] = document{text: text.String, null: !text.Valid, jsonb: db.IsPostgres()}
				r.values[i] = json.RawMessage("null")
				continue
			}
			encoded, err := json.Marshal(dest[i])
			if err != nil {
				return nil, nil, err
			}
			r.values[i] = encoded
		}
		out, documents = append(out, r), append(documents, docs)
	}
	return out, documents, rows.Err()
}

// errNotJSON is the SyntaxError of JSON.parse.
type errNotJSON string

func (e errNotJSON) Error() string { return "the column " + string(e) + " holds text that is not JSON" }

// parsed is what the routes made of a document column:
//
//	typeof value === 'string' ? JSON.parse(value) : (value || fallback)
//
// A text column gives its text, which has to be JSON. A jsonb column
// gives the value: a JSON string is parsed once more, null, false and 0
// are the fallback, everything else is the value. NULL is the fallback.
func (d document) parsed(column, fallback string) (json.RawMessage, error) {
	if d.null {
		return json.RawMessage(fallback), nil
	}
	text := d.text
	if d.jsonb {
		trimmed := strings.TrimSpace(d.text)
		switch {
		case strings.HasPrefix(trimmed, `"`):
			if err := json.Unmarshal([]byte(trimmed), &text); err != nil {
				return nil, errNotJSON(column)
			}
		case trimmed == "null", trimmed == "false", isZero(trimmed):
			return json.RawMessage(fallback), nil
		default:
			text = trimmed
		}
	}
	if !json.Valid([]byte(text)) {
		return nil, errNotJSON(column)
	}
	var restringify store.JSONParsed
	if err := restringify.Scan(text); err != nil {
		return nil, err
	}
	return restringify.MarshalJSON()
}

// unreadable is what a list answers for a document column that holds no
// JSON: the rules as no rules, the details as the text they hold.
func (d document) unreadable(column string) json.RawMessage {
	if column == "rules" {
		return json.RawMessage("[]")
	}
	text := d.text
	if d.jsonb {
		var inner string
		if json.Unmarshal([]byte(strings.TrimSpace(d.text)), &inner) == nil {
			text = inner
		}
	}
	encoded, _ := json.Marshal(text)
	return encoded
}

// isZero reports whether JSON text is the number 0 in any spelling.
func isZero(text string) bool {
	var n float64
	return text != "" && (text[0] == '-' || (text[0] >= '0' && text[0] <= '9')) && json.Unmarshal([]byte(text), &n) == nil && n == 0
}

// policyRows reads policies and puts their parsed rules in place.
func policyRows(ctx context.Context, db *store.DB, query string, args ...any) ([]row, error) {
	return documentRows(ctx, db, "rules", "[]", query, args...)
}

func documentRows(ctx context.Context, db *store.DB, column, fallback, query string, args ...any) ([]row, error) {
	rows, documents, err := selectRows(ctx, db, query, args...)
	if err != nil {
		return nil, err
	}
	for i, r := range rows {
		doc, has := documents[i][column]
		if !has {
			continue
		}
		value, err := doc.parsed(column, fallback)
		var notJSON errNotJSON
		switch {
		case errors.As(err, &notJSON):
			value = doc.unreadable(column)
		case err != nil:
			return nil, err
		}
		r.set(column, value)
	}
	return rows, nil
}

// listPolicies returns every policy, by id.
func listPolicies(ctx context.Context, db *store.DB) ([]row, error) {
	rows, err := policyRows(ctx, db, "SELECT "+policyColumns+" FROM dlp_policies ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	return rows, nil
}

// policyByID returns the policy with the id, nil when there is none.
func policyByID(ctx context.Context, db *store.DB, id jsbind.Value) (*row, error) {
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return nil, fmt.Errorf("load policy %s: %w", id.ToString(), err)
	}
	return onePolicy(ctx, db, "load policy "+id.ToString(), "SELECT "+policyColumns+" FROM dlp_policies WHERE id = ?", args...)
}

// policyExists reports whether there is a policy with the id. It does not
// look at the policy's rules, so it also finds one whose rules cannot be
// read.
func policyExists(ctx context.Context, db *store.DB, id jsbind.Value) (bool, error) {
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return false, fmt.Errorf("load policy %s: %w", id.ToString(), err)
	}
	rows, _, err := selectRows(ctx, db, "SELECT "+policyColumns+" FROM dlp_policies WHERE id = ?", args...)
	if err != nil {
		return false, fmt.Errorf("load policy %s: %w", id.ToString(), err)
	}
	return len(rows) > 0, nil
}

func onePolicy(ctx context.Context, db *store.DB, doing, query string, args ...any) (*row, error) {
	rows, err := policyRows(ctx, db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// newPolicy is what a policy is made from. Empty values have been
// replaced by their defaults already; Enabled is the flag as the database
// takes it.
type newPolicy struct {
	Name, Description, PolicyType, Action, Scope, Enabled, Rules jsbind.Value
}

// createPolicy inserts a policy and returns its row.
func createPolicy(ctx context.Context, db *store.DB, data newPolicy) (*row, error) {
	const insert = "INSERT INTO dlp_policies (name, description, policy_type, action, scope, enabled, rules) VALUES (?, ?, ?, ?, ?, ?, ?)"
	args, err := jsbind.Args(db.Dialect(), 7, data.Name, data.Description, data.PolicyType, data.Action, data.Scope, data.Enabled, data.Rules)
	if err != nil {
		return nil, fmt.Errorf("create policy: %w", err)
	}
	if db.IsPostgres() {
		return onePolicy(ctx, db, "create policy", insert+" RETURNING "+policyColumns, args...)
	}
	res, err := db.Exec(ctx, insert, args...)
	if err != nil {
		return nil, fmt.Errorf("create policy: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create policy: %w", err)
	}
	return policyByID(ctx, db, jsbind.Number(float64(id)))
}

// change is one column an update sets.
type change struct {
	column string
	value  jsbind.Value
}

// updatePolicy sets the given columns and the time of the change, and
// returns the row as it is afterwards. Without changes it returns the row
// as it is.
func updatePolicy(ctx context.Context, db *store.DB, id jsbind.Value, changes []change) (*row, error) {
	if len(changes) == 0 {
		return policyByID(ctx, db, id)
	}
	doing := "update policy " + id.ToString()
	sets := make([]string, 0, len(changes)+1)
	values := make([]jsbind.Value, 0, len(changes)+1)
	for _, c := range changes {
		sets = append(sets, c.column+" = ?")
		values = append(values, c.value)
	}
	sets = append(sets, "updated_at = "+store.NowSQL(db.Dialect()))
	values = append(values, id)
	args, err := jsbind.Args(db.Dialect(), len(values), values...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	update := "UPDATE dlp_policies SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	if db.IsPostgres() {
		return onePolicy(ctx, db, doing, update+" RETURNING "+policyColumns, args...)
	}
	if _, err := db.Exec(ctx, update, args...); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return policyByID(ctx, db, id)
}

// deletePolicy removes a policy and reports whether there was one.
func deletePolicy(ctx context.Context, db *store.DB, id jsbind.Value) (bool, error) {
	doing := "delete policy " + id.ToString()
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	res, err := db.Exec(ctx, "DELETE FROM dlp_policies WHERE id = ?", args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s: %w", doing, err)
	}
	return n > 0, nil
}

// eventFilter is what the event list is narrowed by. A value that is
// empty for JavaScript does not filter.
type eventFilter struct {
	DeviceID, EventSource, EventType, From, To, Limit jsbind.Value
}

// listEvents returns the events that pass the filter, newest first, with
// their details parsed.
func listEvents(ctx context.Context, db *store.DB, f eventFilter) ([]row, error) {
	query := "SELECT * FROM dlp_events WHERE 1=1"
	var values []jsbind.Value
	for _, condition := range []struct {
		sql   string
		value jsbind.Value
	}{
		{" AND device_id = ?", f.DeviceID},
		{" AND event_source = ?", f.EventSource},
		{" AND event_type = ?", f.EventType},
		{" AND created_at >= ?", f.From},
		{" AND created_at <= ?", f.To},
	} {
		if condition.value.Truthy() {
			query += condition.sql
			values = append(values, condition.value)
		}
	}
	query += " ORDER BY created_at DESC"
	if f.Limit.Truthy() {
		query += " LIMIT ?"
		values = append(values, f.Limit)
	}
	args, err := jsbind.Args(db.Dialect(), strings.Count(query, "?"), values...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	rows, err := documentRows(ctx, db, "details", "{}", query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return rows, nil
}

// eventStats is the number of events, all and by action and source.
type eventStats struct {
	Total      jsbind.Cell `json:"total"`
	Blocked    jsbind.Cell `json:"blocked"`
	Logged     jsbind.Cell `json:"logged"`
	USBEvents  jsbind.Cell `json:"usb_events"`
	FileEvents jsbind.Cell `json:"file_events"`
}

func countEvents(ctx context.Context, db *store.DB) (eventStats, error) {
	var s eventStats
	err := db.QueryRow(ctx, db.Pick(`
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN action = 'block' THEN 1 ELSE 0 END), 0) AS blocked,
			COALESCE(SUM(CASE WHEN action = 'log' THEN 1 ELSE 0 END), 0) AS logged,
			COALESCE(SUM(CASE WHEN event_source = 'usb' THEN 1 ELSE 0 END), 0) AS usb_events,
			COALESCE(SUM(CASE WHEN event_source = 'file' THEN 1 ELSE 0 END), 0) AS file_events
		FROM dlp_events`, `
		SELECT
			COUNT(*)::INTEGER AS total,
			COUNT(*) FILTER (WHERE action = 'block')::INTEGER AS blocked,
			COUNT(*) FILTER (WHERE action = 'log')::INTEGER AS logged,
			COUNT(*) FILTER (WHERE event_source = 'usb')::INTEGER AS usb_events,
			COUNT(*) FILTER (WHERE event_source = 'file')::INTEGER AS file_events
		FROM dlp_events`)).Scan(&s.Total, &s.Blocked, &s.Logged, &s.USBEvents, &s.FileEvents)
	if err != nil {
		return s, fmt.Errorf("count events: %w", err)
	}
	return s, nil
}

// activePolicy is a policy as a device gets it.
type activePolicy struct {
	ID    jsbind.Cell     `json:"id"`
	Name  jsbind.Cell     `json:"name"`
	Rules json.RawMessage `json:"rules"`
}

// isOn reads a policy's enabled as one flag on both databases: on is
// true, 1 or the text "1". SQLite keeps what it was given in the INTEGER
// column, so 2, 0.5 and a text such as "yes" can be there; they are off.
func isOn(enabled jsbind.Value) bool {
	switch enabled.JSON() {
	case "true", "1", `"1"`:
		return true
	}
	return false
}

// listActivePolicies returns the policies that are on, by id, with their
// rules parsed. Rules that are no list, or no JSON, go out as no rules.
func listActivePolicies(ctx context.Context, db *store.DB) ([]activePolicy, error) {
	rows, err := store.Select(ctx, db, "SELECT id, name, enabled, rules FROM dlp_policies ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list the policies that are on: %w", err)
	}
	defer rows.Close()
	type candidate struct {
		policy activePolicy
		rules  document
	}
	var on []candidate
	for rows.Next() {
		var id, name, enabled jsbind.Cell
		var rules store.NullString
		if err := rows.Scan(&id, &name, &enabled, &rules); err != nil {
			return nil, fmt.Errorf("read a policy: %w", err)
		}
		if isOn(enabled.Value()) {
			on = append(on, candidate{activePolicy{ID: id, Name: name}, document{text: rules.String, null: !rules.Valid, jsonb: db.IsPostgres()}})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the policies that are on: %w", err)
	}
	out := make([]activePolicy, len(on))
	for i, c := range on {
		rules, err := c.rules.parsed("rules", "[]")
		if err != nil || !bytes.HasPrefix(bytes.TrimSpace(rules), []byte("[")) {
			rules = json.RawMessage("[]")
		}
		out[i] = c.policy
		out[i].Rules = rules
	}
	return out, nil
}

// newEvent is what an event is made from, with the defaults in place.
type newEvent struct {
	DeviceID, EventSource, EventType, PolicyID, PolicyName, Action, Details jsbind.Value
}

// insertEvent stores an event and returns its id as the old console's
// driver gave it.
func insertEvent(ctx context.Context, db *store.DB, e newEvent) (jsbind.Cell, error) {
	const insert = "INSERT INTO dlp_events (device_id, event_source, event_type, policy_id, policy_name, action, details) VALUES (?, ?, ?, ?, ?, ?, ?)"
	args, err := jsbind.Args(db.Dialect(), 7, e.DeviceID, e.EventSource, e.EventType, e.PolicyID, e.PolicyName, e.Action, e.Details)
	if err != nil {
		return jsbind.Cell{}, fmt.Errorf("store an event: %w", err)
	}
	if db.IsPostgres() {
		var id jsbind.Cell
		if err := store.SelectRow(ctx, db, insert+" RETURNING id", args...).Scan(&id); err != nil {
			return jsbind.Cell{}, fmt.Errorf("store an event: %w", err)
		}
		return id, nil
	}
	res, err := db.Exec(ctx, insert, args...)
	if err != nil {
		return jsbind.Cell{}, fmt.Errorf("store an event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return jsbind.Cell{}, fmt.Errorf("store an event: %w", err)
	}
	return jsbind.CellOf(id), nil
}
