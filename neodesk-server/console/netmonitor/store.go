package netmonitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/unitronix/betterdesk-server/console/store"
)

// A Param is a value on its way into a statement, as the JavaScript value
// the old console handed to its database driver. The routes pass on what
// the client sent without checking its type, and the two drivers treated
// such values differently, so the value keeps its JavaScript type until it
// is bound:
//
//   - SQLite (better-sqlite3): every number is bound as a float, so a
//     number in a text column reads back as "42.0". A list stands for its
//     elements, one parameter each. An object stands for named parameters,
//     of which the statements here have none. A boolean cannot be bound.
//   - PostgreSQL (node-postgres): every value is sent as text: a number
//     and a boolean as String(v), a list as an array literal, an object as
//     its JSON.
type Param struct {
	kind  paramKind
	num   float64
	text  string
	items []Param
}

type paramKind uint8

const (
	paramNull paramKind = iota
	paramNumber
	paramText
	paramBool
	paramList
	paramObject
)

// Null is null or undefined.
func Null() Param { return Param{} }

// Num is a number.
func Num(n Number) Param { return Param{kind: paramNumber, num: float64(n)} }

// Text is a string.
func Text(s string) Param { return Param{kind: paramText, text: s} }

// Bool is true or false.
func Bool(b bool) Param {
	p := Param{kind: paramBool}
	if b {
		p.num = 1
	}
	return p
}

// List is an array.
func List(items ...Param) Param { return Param{kind: paramList, items: items} }

// Object is an object; json is what JSON.stringify gives for it.
func Object(json string) Param { return Param{kind: paramObject, text: json} }

// BindError is the error of the SQLite driver for values it cannot bind
// to a statement. Message is the text the driver gave.
type BindError struct {
	Message string
}

func (e *BindError) Error() string { return e.Message }

const (
	bindWrongType = "SQLite3 can only bind numbers, strings, bigints, buffers, and null"
	bindTooMany   = "Too many parameter values were provided"
	bindTooFew    = "Too few parameter values were provided"
	bindTwoNamed  = "You cannot specify named parameters in two different objects"
)

// sqliteArgs turns params into the arguments of a statement with count
// placeholders, the way better-sqlite3 bound them.
func sqliteArgs(count int, params []Param) ([]any, error) {
	args := make([]any, 0, count)
	bindOne := func(p Param) error {
		var value any
		switch p.kind {
		case paramNull:
		case paramNumber:
			value = p.num
		case paramText:
			value = p.text
		default:
			return &BindError{Message: bindWrongType}
		}
		if len(args) >= count {
			return &BindError{Message: bindTooMany}
		}
		args = append(args, value)
		return nil
	}
	named := false
	for _, p := range params {
		switch p.kind {
		case paramList:
			for _, item := range p.items {
				if err := bindOne(item); err != nil {
					return nil, err
				}
			}
		case paramObject:
			if named {
				return nil, &BindError{Message: bindTwoNamed}
			}
			named = true
		default:
			if err := bindOne(p); err != nil {
				return nil, err
			}
		}
	}
	if len(args) < count {
		return nil, &BindError{Message: bindTooFew}
	}
	return args, nil
}

// postgresArgs turns params into the text node-postgres sent for them.
func postgresArgs(params []Param) []any {
	args := make([]any, len(params))
	for i, p := range params {
		if p.kind != paramNull {
			args[i] = p.postgresText()
		}
	}
	return args
}

func (p Param) postgresText() string {
	switch p.kind {
	case paramNumber:
		return Number(p.num).String()
	case paramBool:
		if p.num != 0 {
			return "true"
		}
		return "false"
	case paramList:
		var b strings.Builder
		b.WriteByte('{')
		for i, item := range p.items {
			if i > 0 {
				b.WriteByte(',')
			}
			switch item.kind {
			case paramNull:
				b.WriteString("NULL")
			case paramList:
				b.WriteString(item.postgresText())
			default:
				b.WriteByte('"')
				b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(item.postgresText()))
				b.WriteByte('"')
			}
		}
		b.WriteByte('}')
		return b.String()
	}
	return p.text
}

// args binds params for the database of q. count is the number of
// placeholders in the statement.
func bindArgs(q store.Querier, count int, params []Param) ([]any, error) {
	if q.Dialect() == store.Postgres {
		return postgresArgs(params), nil
	}
	return sqliteArgs(count, params)
}

// sqliteErrorNames are the general descriptions SQLite's Go driver puts
// in front of the message of an error.
var sqliteErrorNames = []string{
	"SQL logic error", "access permission denied", "query aborted", "database is locked",
	"database table is locked", "out of memory", "attempt to write a readonly database", "interrupted",
	"disk I/O error", "database disk image is malformed", "unknown operation", "database or disk is full",
	"unable to open database file", "locking protocol", "database schema has changed",
	"string or blob too big", "constraint failed", "datatype mismatch", "bad parameter or other API misuse",
	"authorization denied", "column index out of range", "file is not a database",
}

var sqliteErrorCode = regexp.MustCompile(` \(\d+\)( \(SQLITE_BUSY\))?$`)

// DriverMessage is the message the old console's database driver gave for
// err, which the network routes send to the client: the server's message
// on PostgreSQL, SQLite's own message without what the Go driver adds
// around it.
func DriverMessage(err error) string {
	var bind *BindError
	if errors.As(err, &bind) {
		return bind.Message
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Message
	}
	// The innermost error is the driver's
	inner := err
	for next := errors.Unwrap(inner); next != nil; next = errors.Unwrap(inner) {
		inner = next
	}
	message := inner.Error()
	if !sqliteErrorCode.MatchString(message) {
		return message
	}
	message = sqliteErrorCode.ReplaceAllString(message, "")
	for _, name := range sqliteErrorNames {
		if rest, ok := strings.CutPrefix(message, name+": "); ok {
			return rest
		}
	}
	return message
}

// Target is a row of network_targets, with its members in the order of
// the table's columns.
type Target struct {
	ID          store.Int64String `json:"id"`
	Name        store.NullString  `json:"name"`
	Host        store.NullString  `json:"host"`
	Port        store.Float       `json:"port"`
	URL         store.NullString  `json:"url"`
	CheckType   store.NullString  `json:"check_type"`
	TimeoutMs   store.Float       `json:"timeout_ms"`
	IntervalMs  store.Float       `json:"interval_ms"`
	Enabled     store.Bool        `json:"enabled"`
	LastStatus  store.NullString  `json:"last_status"`
	LastCheckAt store.Time        `json:"last_check_at"`
	LastRTT     store.Float       `json:"last_rtt_ms"`
	CreatedAt   store.Time        `json:"created_at"`
	UpdatedAt   store.Time        `json:"updated_at"`
}

// Check is a row of network_checks.
type Check struct {
	ID         store.Int64String `json:"id"`
	TargetID   store.Int64String `json:"target_id"`
	Status     store.NullString  `json:"status"`
	RTT        store.Float       `json:"rtt_ms"`
	StatusCode store.Float       `json:"status_code"`
	ErrorMsg   store.NullString  `json:"error_msg"`
	CheckedAt  store.Time        `json:"checked_at"`
}

const (
	targetColumns = "id, name, host, port, url, check_type, timeout_ms, interval_ms, enabled, last_status, last_check_at, last_rtt_ms, created_at, updated_at"
	checkColumns  = "id, target_id, status, rtt_ms, status_code, error_msg, checked_at"
)

func (t *Target) fields() []any {
	return []any{
		&t.ID, &t.Name, &t.Host, &t.Port, &t.URL, &t.CheckType, &t.TimeoutMs, &t.IntervalMs,
		&t.Enabled, &t.LastStatus, &t.LastCheckAt, &t.LastRTT, &t.CreatedAt, &t.UpdatedAt,
	}
}

// selectTargets runs a query that returns target rows.
func selectTargets(ctx context.Context, q store.Querier, query string, args []any) ([]Target, error) {
	rows, err := store.Select(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []Target{}
	for rows.Next() {
		var t Target
		if err := rows.Scan(t.fields()...); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

// selectTarget runs a query that returns at most one target row, nil for
// none.
func selectTarget(ctx context.Context, q store.Querier, query string, args []any) (*Target, error) {
	var t Target
	err := store.SelectRow(ctx, q, query, args...).Scan(t.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TargetFilter narrows ListTargets. Enabled is nil for both states;
// CheckType is nil for every type.
type TargetFilter struct {
	Enabled   *bool
	CheckType *Param
}

// ListTargets is getNetworkTargets(filters): the targets by name, targets
// of one name in the order of their ids.
func ListTargets(ctx context.Context, db store.Querier, filter TargetFilter) ([]Target, error) {
	query := "SELECT " + targetColumns + " FROM network_targets WHERE 1=1"
	var params []Param
	count := 0
	if filter.Enabled != nil {
		query += " AND enabled = ?"
		count++
		if db.Dialect() == store.Postgres {
			params = append(params, Bool(*filter.Enabled))
		} else if *filter.Enabled {
			params = append(params, Num(1))
		} else {
			params = append(params, Num(0))
		}
	}
	if filter.CheckType != nil {
		query += " AND check_type = ?"
		count++
		params = append(params, *filter.CheckType)
	}
	args, err := bindArgs(db, count, params)
	if err != nil {
		return nil, err
	}
	targets, err := selectTargets(ctx, db, query+" ORDER BY name, id", args)
	if err != nil {
		return nil, fmt.Errorf("list network targets: %w", err)
	}
	return targets, nil
}

// GetTarget is getNetworkTargetById(id): the target, nil when there is
// none. id is what the caller has: the text of a path, or a number.
func GetTarget(ctx context.Context, db store.Querier, id Param) (*Target, error) {
	args, err := bindArgs(db, 1, []Param{id})
	if err != nil {
		return nil, err
	}
	target, err := selectTarget(ctx, db, "SELECT "+targetColumns+" FROM network_targets WHERE id = ?", args)
	if err != nil {
		return nil, fmt.Errorf("load network target: %w", err)
	}
	return target, nil
}

// NewTarget is what createNetworkTarget stores.
type NewTarget struct {
	Name, Host, Port, URL, CheckType Param
	TimeoutMs, IntervalMs            Number
	Enabled                          bool
}

// CreateTarget is createNetworkTarget(data): it stores a target and
// returns its row.
func CreateTarget(ctx context.Context, db *store.DB, data NewTarget) (*Target, error) {
	const insert = "INSERT INTO network_targets (name, host, port, url, check_type, timeout_ms, interval_ms, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
	enabled := Num(0)
	if data.Enabled {
		enabled = Num(1)
	}
	if db.IsPostgres() {
		enabled = Bool(data.Enabled)
	}
	params := []Param{
		data.Name, data.Host, data.Port, data.URL, data.CheckType,
		Num(data.TimeoutMs.Or(DefaultTimeoutMs)), Num(data.IntervalMs.Or(DefaultPollIntervalMs)), enabled,
	}
	args, err := bindArgs(db, 8, params)
	if err != nil {
		return nil, err
	}
	if db.IsPostgres() {
		target, err := selectTarget(ctx, db, insert+" RETURNING "+targetColumns, args)
		if err != nil {
			return nil, fmt.Errorf("create network target: %w", err)
		}
		return target, nil
	}
	res, err := db.Exec(ctx, insert, args...)
	if err != nil {
		return nil, fmt.Errorf("create network target: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create network target: %w", err)
	}
	return GetTarget(ctx, db, Num(Number(id)))
}

// TargetChanges are the columns updateNetworkTarget sets; nil leaves a
// column as it is.
type TargetChanges struct {
	Name, Host, Port, URL, CheckType, TimeoutMs, IntervalMs *Param
	// Enabled is the truth of what the caller had (data.enabled ? 1 : 0).
	Enabled                          *bool
	LastStatus, LastCheckAt, LastRTT *Param
}

// UpdateTarget is updateNetworkTarget(id, data): it changes the named
// columns and returns the row. With nothing to change it returns the row
// as it is. It returns nil when no target has the id.
func UpdateTarget(ctx context.Context, db *store.DB, id Param, data TargetChanges) (*Target, error) {
	var sets []string
	var params []Param
	set := func(column string, p *Param) {
		if p != nil {
			sets = append(sets, column+" = ?")
			params = append(params, *p)
		}
	}
	set("name", data.Name)
	set("host", data.Host)
	set("port", data.Port)
	set("url", data.URL)
	set("check_type", data.CheckType)
	set("timeout_ms", data.TimeoutMs)
	set("interval_ms", data.IntervalMs)
	if data.Enabled != nil {
		enabled := Num(0)
		if *data.Enabled {
			enabled = Num(1)
		}
		if db.IsPostgres() {
			enabled = Bool(*data.Enabled)
		}
		set("enabled", &enabled)
	}
	set("last_status", data.LastStatus)
	set("last_check_at", data.LastCheckAt)
	set("last_rtt_ms", data.LastRTT)
	if len(sets) == 0 {
		return GetTarget(ctx, db, id)
	}
	sets = append(sets, "updated_at = "+store.NowSQL(db.Dialect()))
	params = append(params, id)
	args, err := bindArgs(db, len(params), params)
	if err != nil {
		return nil, err
	}
	update := "UPDATE network_targets SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	if db.IsPostgres() {
		target, err := selectTarget(ctx, db, update+" RETURNING "+targetColumns, args)
		if err != nil {
			return nil, fmt.Errorf("update network target: %w", err)
		}
		return target, nil
	}
	if _, err := db.Exec(ctx, update, args...); err != nil {
		return nil, fmt.Errorf("update network target: %w", err)
	}
	return GetTarget(ctx, db, id)
}

// DeleteTarget is deleteNetworkTarget(id). It reports whether a target
// was deleted. The target's checks go with it.
func DeleteTarget(ctx context.Context, db *store.DB, id Param) (bool, error) {
	args, err := bindArgs(db, 1, []Param{id})
	if err != nil {
		return false, err
	}
	res, err := db.Exec(ctx, "DELETE FROM network_targets WHERE id = ?", args...)
	if err != nil {
		return false, fmt.Errorf("delete network target: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete network target: %w", err)
	}
	return n > 0, nil
}

// NewCheck is what insertNetworkCheck stores.
type NewCheck struct {
	TargetID, Status, RTT, StatusCode, ErrorMsg Param
}

// InsertCheck is insertNetworkCheck(data).
func InsertCheck(ctx context.Context, db *store.DB, data NewCheck) error {
	args, err := bindArgs(db, 5, []Param{data.TargetID, data.Status, data.RTT, data.StatusCode, data.ErrorMsg})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, "INSERT INTO network_checks (target_id, status, rtt_ms, status_code, error_msg) VALUES (?, ?, ?, ?, ?)", args...)
	if err != nil {
		return fmt.Errorf("store network check: %w", err)
	}
	return nil
}

// MaxChecksPerTarget is how many checks a target keeps: its newest ones.
// At the default interval of a minute that is about a week of history.
const MaxChecksPerTarget = 10000

// PruneChecks removes the checks of a target before its newest keep ones.
func PruneChecks(ctx context.Context, db *store.DB, targetID Param, keep int) error {
	args, err := bindArgs(db, 3, []Param{targetID, targetID, Num(Number(keep - 1))})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, "DELETE FROM network_checks WHERE target_id = ? AND id < (SELECT id FROM network_checks WHERE target_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?)", args...)
	if err != nil {
		return fmt.Errorf("prune network checks: %w", err)
	}
	return nil
}

// HistoryFilter narrows CheckHistory; nil leaves a bound out.
type HistoryFilter struct {
	Limit, From, To *Param
}

// CheckHistory is getNetworkCheckHistory(targetId, {limit, from, to}):
// the checks of a target, the newest first; checks of the same second in
// the order they were stored, the last first.
func CheckHistory(ctx context.Context, db store.Querier, targetID Param, filter HistoryFilter) ([]Check, error) {
	query := "SELECT " + checkColumns + " FROM network_checks WHERE target_id = ?"
	params := []Param{targetID}
	if filter.From != nil {
		query += " AND checked_at >= ?"
		params = append(params, *filter.From)
	}
	if filter.To != nil {
		query += " AND checked_at <= ?"
		params = append(params, *filter.To)
	}
	query += " ORDER BY checked_at DESC, id DESC"
	if filter.Limit != nil {
		query += " LIMIT ?"
		params = append(params, *filter.Limit)
	}
	args, err := bindArgs(db, len(params), params)
	if err != nil {
		return nil, err
	}
	rows, err := store.Select(ctx, db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list network checks: %w", err)
	}
	defer rows.Close()
	checks := []Check{}
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.ID, &c.TargetID, &c.Status, &c.RTT, &c.StatusCode, &c.ErrorMsg, &c.CheckedAt); err != nil {
			return nil, fmt.Errorf("read a network check: %w", err)
		}
		checks = append(checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list network checks: %w", err)
	}
	return checks, nil
}
