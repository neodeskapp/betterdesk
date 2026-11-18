package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// The account typeahead of the sign-in page: a prefix of 2 to 64 UTF-16
// units finds at most 8 accounts.
const (
	lookupMinLength = 2
	lookupMaxLength = 64
	lookupMatches   = 8
)

// lookupMatch is an account the typeahead offers: its name and its role,
// "operator" for none.
type lookupMatch struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type lookupAnswer struct {
	Success bool          `json:"success"`
	Matches []lookupMatch `json:"matches"`
}

// loginLookup answers GET /login/lookup?q=<prefix>: the accounts whose
// name starts with the prefix, in the order of their ids, compared in
// lower case after the prefix's white space is trimmed. A prefix that is
// missing, given twice or in brackets, too short or too long finds
// nothing, and so does a failed read.
func (h *handlers) loginLookup(w http.ResponseWriter, r *http.Request) {
	query := ""
	if q := jsbind.Query(r.URL.RawQuery, "q"); q.IsString() {
		query = jsLower(jsbind.Trim(q.Str()))
	}
	matches := []lookupMatch{}
	if n := utf16Length(query); n >= lookupMinLength && n <= lookupMaxLength {
		found, err := lookupAccounts(r.Context(), h, query)
		if err != nil {
			h.log.Warn("Sign-in lookup failed:", err)
		} else {
			matches = found
		}
	}
	kit.JSON(w, r, http.StatusOK, lookupAnswer{Success: true, Matches: matches})
}

// lookupAccounts reads every account and keeps the first ones whose name
// in lower case starts with the prefix.
func lookupAccounts(ctx context.Context, h *handlers, prefix string) ([]lookupMatch, error) {
	rows, err := h.d.Store.Query(ctx, `SELECT username, role FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load the accounts: %w", err)
	}
	defer rows.Close()
	matches := []lookupMatch{}
	for rows.Next() {
		var username, role sql.NullString
		if err := rows.Scan(&username, &role); err != nil {
			return nil, fmt.Errorf("read an account: %w", err)
		}
		if len(matches) == lookupMatches || !strings.HasPrefix(jsLower(username.String), prefix) {
			continue
		}
		match := lookupMatch{Username: username.String, Role: role.String}
		if match.Role == "" {
			match.Role = "operator"
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load the accounts: %w", err)
	}
	return matches, nil
}
