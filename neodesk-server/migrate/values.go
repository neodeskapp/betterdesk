package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Text forms of a time on SQLite. Each column keeps the form the server's
// own code writes into it there: the console sends the stored text in its
// JSON as it is, and some queries compare the text with a bound value or
// with datetime('now').
const (
	// sqliteTimeForm is what datetime('now') writes, the form of almost
	// every time column.
	sqliteTimeForm = "2006-01-02 15:04:05"
	// isoTimeForm is JavaScript's toISOString, store.ISOTime.
	isoTimeForm = "2006-01-02T15:04:05.000Z"
	// millisTimeForm is store.DateTimeMillis, the expiry form of tokens
	// and lockouts.
	millisTimeForm = "2006-01-02 15:04:05.000"
	// rfc3339Form is what the signal server writes into its organization
	// tables and device token expiries.
	rfc3339Form = time.RFC3339
)

// sqliteTimeForms names the time columns whose SQLite text is not in
// datetime('now')'s form, with the form their writers use.
var sqliteTimeForms = map[string]string{
	"access_tokens.expires_at":                millisTimeForm,
	"account_lockouts.locked_until":           millisTimeForm,
	"activity_sessions.started_at":            isoTimeForm,
	"activity_sessions.ended_at":              isoTimeForm,
	"activity_summaries.reported_at":          isoTimeForm,
	"alert_history.triggered_at":              isoTimeForm,
	"device_inventory.collected_at":           isoTimeForm,
	"device_telemetry.timestamp":              isoTimeForm,
	"device_telemetry_snapshots.collected_at": isoTimeForm,
	"network_targets.last_check_at":           isoTimeForm,
	"studio_flows.created_at":                 isoTimeForm,
	"studio_flows.updated_at":                 isoTimeForm,
	"tickets.sla_due_at":                      isoTimeForm,
	"device_tokens.expires_at":                rfc3339Form,
	"org_invitations.expires_at":              rfc3339Form,
	"org_invitations.used_at":                 rfc3339Form,
	"org_users.created_at":                    rfc3339Form,
	"org_users.last_login":                    rfc3339Form,
	"organizations.created_at":                rfc3339Form,
}

// convert turns a value read from column src of the source into the value
// column dst of the destination takes. dstPG says the destination is
// PostgreSQL; field is "table.column" of the destination, for the time
// forms. A nil result is NULL.
func convert(v any, src, dst column, dstPG bool, field string) (any, error) {
	if v == nil {
		return nil, nil
	}
	if dstPG {
		return toPostgres(v, src, dst)
	}
	return toSQLite(v, src, dst, field)
}

func toPostgres(v any, src, dst column) (any, error) {
	switch dst.kind {
	case kTime:
		switch x := v.(type) {
		case time.Time:
			return x.UTC(), nil
		case int64:
			if src.kind == kInteger {
				// panel_sessions.expires_at: milliseconds on SQLite.
				return time.UnixMilli(x).UTC(), nil
			}
		case string, []byte:
			s := strings.TrimSpace(asString(x))
			if s == "" {
				return nil, nil
			}
			if t := parseTime(s); t != nil {
				return t.UTC(), nil
			}
			return nil, fmt.Errorf("%q is no time", s)
		}
	case kBool:
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			return x != 0, nil
		case float64:
			return x != 0, nil
		case string, []byte:
			switch strings.ToLower(strings.TrimSpace(asString(x))) {
			case "1", "true", "t":
				return true, nil
			case "0", "false", "f":
				return false, nil
			}
		}
	case kInteger:
		switch x := v.(type) {
		case int64:
			return x, nil
		case bool:
			return boolToInt64(x), nil
		case float64:
			if x == math.Trunc(x) && !math.IsInf(x, 0) {
				return int64(x), nil
			}
		case string, []byte:
			if n, err := strconv.ParseInt(strings.TrimSpace(asString(x)), 10, 64); err == nil {
				return n, nil
			}
		case time.Time:
			return x.UnixMilli(), nil
		}
	case kReal, kFloat4:
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		case string, []byte:
			if f, err := strconv.ParseFloat(strings.TrimSpace(asString(x)), 64); err == nil {
				return f, nil
			}
		}
	case kNumeric:
		switch x := v.(type) {
		case float64:
			return strconv.FormatFloat(x, 'g', -1, 64), nil
		case int64:
			return strconv.FormatInt(x, 10), nil
		case string, []byte:
			return strings.TrimSpace(asString(x)), nil
		}
	case kJSON:
		switch x := v.(type) {
		case string, []byte:
			s := asString(x)
			if strings.TrimSpace(s) == "" {
				return nil, nil
			}
			if !json.Valid([]byte(s)) {
				return nil, fmt.Errorf("%q is no JSON", clip(s))
			}
			return s, nil
		}
	case kBlob:
		switch x := v.(type) {
		case []byte:
			return x, nil
		case string:
			return []byte(x), nil
		}
	case kText:
		return toText(v, src)
	default:
		return v, nil
	}
	return nil, fmt.Errorf("a %T (%s) does not go into %s", v, clip(fmt.Sprint(v)), dst.declared)
}

func toSQLite(v any, src, dst column, field string) (any, error) {
	if t, ok := v.(time.Time); ok {
		if dst.kind == kInteger {
			// panel_sessions.expires_at: milliseconds on SQLite.
			return t.UnixMilli(), nil
		}
		form := sqliteTimeForm
		if f, ok := sqliteTimeForms[field]; ok && dst.kind != kDateTimeText {
			form = f
		}
		return t.UTC().Format(form), nil
	}
	switch x := v.(type) {
	case bool:
		return boolToInt64(x), nil
	case float64:
		if src.kind == kFloat4 {
			// The shortest number that names the four bytes, the value
			// PostgreSQL prints and the console reads (0.1, not
			// 0.10000000149011612).
			if short, err := strconv.ParseFloat(strconv.FormatFloat(x, 'g', -1, 32), 64); err == nil {
				x = short
			}
		}
		if dst.kind == kText {
			return strconv.FormatFloat(x, 'g', -1, 64), nil
		}
		return x, nil
	case int64:
		if dst.kind == kText {
			return strconv.FormatInt(x, 10), nil
		}
		return x, nil
	case []byte:
		switch {
		case src.kind == kJSON && src.declared == "jsonb":
			// PostgreSQL prints jsonb with a space after every colon and
			// comma; the console writes JSON.stringify's form.
			var b bytes.Buffer
			if err := json.Compact(&b, x); err != nil {
				return nil, fmt.Errorf("jsonb %q: %w", clip(string(x)), err)
			}
			return b.String(), nil
		case src.kind == kBlob || dst.kind == kBlob:
			return x, nil
		}
		return string(x), nil
	case string:
		if src.kind == kNumeric && (dst.kind == kReal || dst.kind == kInteger) {
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f, nil
			}
		}
		return x, nil
	}
	return v, nil
}

// toText turns a value into the text PostgreSQL takes for a text column.
func toText(v any, src column) (any, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []byte:
		if !utf8.Valid(x) {
			return nil, fmt.Errorf("%d bytes that are not UTF-8 text", len(x))
		}
		return string(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case bool:
		return strconv.FormatBool(x), nil
	case time.Time:
		if src.kind == kDateTimeText {
			return x.UTC().Format(sqliteTimeForm), nil
		}
		return x.UTC().Format(time.RFC3339Nano), nil
	}
	return nil, fmt.Errorf("a %T does not go into text", v)
}

func asString(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	s, _ := v.(string)
	return s
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// clip shortens a value for an error message.
func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
