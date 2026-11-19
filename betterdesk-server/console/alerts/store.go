package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Rule is a row of alert_rules as the engine reads it. The columns are
// JavaScript values, because what a rule does depends on the type the
// database driver gave each one:
//
//   - On SQLite a column holds what was written to it. condition_value is
//     a number, or text when the text is no number ("abc", "0x10");
//     cooldown_secs is a whole number, a fraction or text. The text
//     columns are text or NULL.
//   - On PostgreSQL every column has its type. condition_value is a
//     four-byte real and reads as the shortest number that names it (0.1,
//     not 0.10000000149011612), and it can be NaN or an infinity.
type Rule struct {
	ID             jsbind.Value
	Name           jsbind.Value
	Description    jsbind.Value
	ConditionType  jsbind.Value
	ConditionOp    jsbind.Value
	ConditionValue jsbind.Value
	Severity       jsbind.Value
	ScopeDeviceID  jsbind.Value
	CooldownSecs   jsbind.Value
	NotifyEmails   jsbind.Value
}

// RuleFilter narrows GetAlertRules.
type RuleFilter struct {
	// Enabled keeps the rules that are on (true) or off (false); nil
	// keeps both. On SQLite "on" is the number 1 in the column and "off"
	// the number 0: a rule with another number or with text there is
	// neither.
	Enabled *bool
	// ConditionType keeps the rules of one condition type; "" keeps all.
	ConditionType string
}

// GetAlertRules returns the alert rules, newest first (dbAdapter
// getAlertRules).
func GetAlertRules(ctx context.Context, q store.Querier, filter RuleFilter) ([]Rule, error) {
	query := `SELECT id, name, description, condition_type, condition_op, condition_value,
		severity, scope_device_id, cooldown_secs, notify_emails FROM alert_rules WHERE 1=1`
	var args []any
	if filter.Enabled != nil {
		query += " AND enabled = ?"
		column := store.TypeInteger
		if q.Dialect() == store.Postgres {
			column = store.TypeBoolean
		}
		args = append(args, store.BoolArg(column, *filter.Enabled))
	}
	if filter.ConditionType != "" {
		query += " AND condition_type = ?"
		args = append(args, filter.ConditionType)
	}
	query += " ORDER BY created_at DESC"

	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read the alert rules: %w", err)
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		var cells [10]jsbind.Cell
		dest := make([]any, len(cells))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("read an alert rule: %w", err)
		}
		rule := Rule{
			ID:             cells[0].Value(),
			Name:           cells[1].Value(),
			Description:    cells[2].Value(),
			ConditionType:  cells[3].Value(),
			ConditionOp:    cells[4].Value(),
			ConditionValue: cells[5].Value(),
			Severity:       cells[6].Value(),
			ScopeDeviceID:  cells[7].Value(),
			CooldownSecs:   cells[8].Value(),
			NotifyEmails:   cells[9].Value(),
		}
		if q.Dialect() == store.Postgres && rule.ConditionValue.IsNumber() {
			rule.ConditionValue = jsbind.Number(shortReal(rule.ConditionValue.ToNumber()))
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the alert rules: %w", err)
	}
	return rules, nil
}

// shortReal is a PostgreSQL real as JavaScript read it. The server prints
// a four-byte number with the fewest digits that name it, and the driver
// parsed that text: 0.1 came out as 0.1, where widening the four bytes
// gives 0.10000000149011612.
func shortReal(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	short, err := strconv.ParseFloat(strconv.FormatFloat(f, 'g', -1, 32), 64)
	if err != nil {
		return f
	}
	return short
}

// GetRecentAlert reports whether the rule raised an alert for the device
// in the last cooldown seconds, by the database's clock (dbAdapter
// getRecentAlert). The three values are bound as the Node database
// drivers bound them, so a device id or a cooldown of an odd type fails
// or matches nothing as it did.
func GetRecentAlert(ctx context.Context, q store.Querier, ruleID, deviceID, cooldown jsbind.Value) (bool, error) {
	query := q.Pick(
		`SELECT id FROM alert_history
		 WHERE rule_id = ? AND device_id = ?
		   AND datetime(triggered_at) > datetime('now', '-' || ? || ' seconds')
		 ORDER BY triggered_at DESC LIMIT 1`,
		`SELECT id FROM alert_history
		 WHERE rule_id = ? AND device_id = ?
		   AND triggered_at > NOW() - INTERVAL '1 second' * ?
		 ORDER BY triggered_at DESC LIMIT 1`)
	args, err := jsbind.Args(q.Dialect(), 3, ruleID, deviceID, cooldown)
	if err != nil {
		return false, err
	}
	var id int64
	err = q.QueryRow(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look for a recent alert: %w", err)
	}
	return true, nil
}

// NewAlert is an alert to write to alert_history.
type NewAlert struct {
	RuleID jsbind.Value
	// DeviceID is stored as NULL when it is not set.
	DeviceID jsbind.Value
	// Severity is "warning" when it is not set.
	Severity jsbind.Value
	Message  jsbind.Value
	// TriggeredAt is the time as ISO text; when it is not set the alert
	// gets the database's time, the clock GetRecentAlert checks against.
	TriggeredAt jsbind.Value
}

// CreateAlert writes an alert (dbAdapter createAlert), with the values
// bound as the Node database drivers bound them, and returns its time as
// ISO text.
func CreateAlert(ctx context.Context, q store.Querier, alert NewAlert) (string, error) {
	deviceID := alert.DeviceID.Or(jsbind.Null)
	severity := alert.Severity.Or(jsbind.String("warning"))
	message := alert.Message.Or(jsbind.String(""))
	triggeredAt := alert.TriggeredAt
	if !triggeredAt.Truthy() {
		var now string
		err := q.QueryRow(ctx, q.Pick(`SELECT strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
			`SELECT to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`)).Scan(&now)
		if err != nil {
			return "", fmt.Errorf("read the database's time: %w", err)
		}
		triggeredAt = jsbind.String(now)
	}
	args, err := jsbind.Args(q.Dialect(), 5, alert.RuleID, deviceID, severity, message, triggeredAt)
	if err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `INSERT INTO alert_history (rule_id, device_id, severity, message, triggered_at)
		VALUES (?, ?, ?, ?, ?)`, args...)
	if err != nil {
		return "", fmt.Errorf("write the alert: %w", err)
	}
	return triggeredAt.Str(), nil
}

// ruleStore is the engine's view of the database.
type ruleStore interface {
	// enabledRules is getAlertRules({ enabled: true, condition_type }).
	enabledRules(ctx context.Context, conditionType string) ([]Rule, error)
	recentAlert(ctx context.Context, ruleID, deviceID, cooldown jsbind.Value) (bool, error)
	// createAlert writes an alert and returns its time as ISO text.
	createAlert(ctx context.Context, alert NewAlert) (string, error)
}

type dbStore struct{ db *store.DB }

func (s dbStore) enabledRules(ctx context.Context, conditionType string) ([]Rule, error) {
	on := true
	return GetAlertRules(ctx, s.db, RuleFilter{Enabled: &on, ConditionType: conditionType})
}

func (s dbStore) recentAlert(ctx context.Context, ruleID, deviceID, cooldown jsbind.Value) (bool, error) {
	return GetRecentAlert(ctx, s.db, ruleID, deviceID, cooldown)
}

func (s dbStore) createAlert(ctx context.Context, alert NewAlert) (string, error) {
	return CreateAlert(ctx, s.db, alert)
}
