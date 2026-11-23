package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Rule is a row of alert_rules as the engine reads it. The columns are
// JavaScript values, because what a rule does depends on the type the
// database driver gave each one:
//
//   - On SQLite a column holds what was written to it. condition_value is
//     a number, or text when the text is no number ("abc", "0x10");
//     cooldown_secs is a whole number, a fraction or text. The text
//     columns are text or NULL.
//   - On PostgreSQL every column has its type. condition_value is double
//     precision (store.Migrate widens the four-byte real of the Node
//     console), and it can be NaN or an infinity.
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
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the alert rules: %w", err)
	}
	return rules, nil
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
	// cooldownStep runs fn as one step for a rule and a device: no other
	// step for the same rule and device, in this console or another one
	// on the same database, checks or writes alerts while fn runs. fn's
	// error ends the step without its writes.
	cooldownStep(ctx context.Context, ruleID, deviceID jsbind.Value, fn func(alertStore) error) error
}

// alertStore checks and writes alerts inside a cooldown step.
type alertStore interface {
	recentAlert(ctx context.Context, ruleID, deviceID, cooldown jsbind.Value) (bool, error)
	// createAlert writes an alert and returns its time as ISO text.
	createAlert(ctx context.Context, alert NewAlert) (string, error)
}

type dbStore struct{ db *store.DB }

func (s dbStore) enabledRules(ctx context.Context, conditionType string) ([]Rule, error) {
	on := true
	return GetAlertRules(ctx, s.db, RuleFilter{Enabled: &on, ConditionType: conditionType})
}

// cooldownStep is a transaction. On SQLite the console's transactions
// take the write lock when they begin, so they run one after the other.
// On PostgreSQL the transaction takes an advisory lock for the rule and
// the device first; two pairs that share a lock key just wait for each
// other.
func (s dbStore) cooldownStep(ctx context.Context, ruleID, deviceID jsbind.Value, fn func(alertStore) error) error {
	return s.db.InTx(ctx, func(tx *store.Tx) error {
		if tx.Dialect() == store.Postgres {
			if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(?)", cooldownLockKey(ruleID, deviceID)); err != nil {
				return fmt.Errorf("lock the alert cooldown: %w", err)
			}
		}
		return fn(txStore{tx: tx})
	})
}

// cooldownLockKey is the advisory lock of a rule and a device: a hash of
// both as text, in the key space of the alert engine.
func cooldownLockKey(ruleID, deviceID jsbind.Value) int64 {
	h := fnv.New64a()
	h.Write([]byte("neodesk alert cooldown\x00" + ruleID.ToString() + "\x00" + deviceID.ToString()))
	return int64(h.Sum64())
}

type txStore struct{ tx *store.Tx }

// recentAlert runs the check under a savepoint on PostgreSQL: a check
// that fails does not hold the alert back, and the insert after it must
// not find the transaction aborted.
func (s txStore) recentAlert(ctx context.Context, ruleID, deviceID, cooldown jsbind.Value) (bool, error) {
	if s.tx.Dialect() != store.Postgres {
		return GetRecentAlert(ctx, s.tx, ruleID, deviceID, cooldown)
	}
	if _, err := s.tx.Exec(ctx, "SAVEPOINT cooldown_check"); err != nil {
		return false, fmt.Errorf("look for a recent alert: %w", err)
	}
	recent, err := GetRecentAlert(ctx, s.tx, ruleID, deviceID, cooldown)
	end := "RELEASE SAVEPOINT cooldown_check"
	if err != nil {
		end = "ROLLBACK TO SAVEPOINT cooldown_check"
	}
	if _, endErr := s.tx.Exec(ctx, end); endErr != nil && err == nil {
		err = fmt.Errorf("look for a recent alert: %w", endErr)
	}
	return recent, err
}

func (s txStore) createAlert(ctx context.Context, alert NewAlert) (string, error) {
	return CreateAlert(ctx, s.tx, alert)
}
