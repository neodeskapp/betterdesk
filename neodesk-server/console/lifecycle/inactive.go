package lifecycle

import (
	"context"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The rule for inactive accounts: an account that has not signed in for
// the set number of days is disabled, after a notice to the person
// InactiveNoticeDays before. The server keeps the number of days in its
// configuration (server_config); 0, nothing or anything else is off.
const (
	InactiveDaysKey    = "inactive_disable_days"
	MinInactiveDays    = 14
	MaxInactiveDays    = 3650
	InactiveNoticeDays = 7
)

// ParseInactiveDays is the rule's number of days of a stored value: 0 for
// off and for a value outside the allowed range.
func ParseInactiveDays(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < MinInactiveDays || n > MaxInactiveDays {
		return 0
	}
	return n
}

// InactiveDays reads the rule's number of days from the server's
// configuration; 0 when it is off or the table is missing.
func InactiveDays(ctx context.Context, db *store.DB) int {
	var value string
	if err := db.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, InactiveDaysKey).Scan(&value); err != nil {
		return 0
	}
	return ParseInactiveDays(value)
}
