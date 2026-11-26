// Package people names the console's accounts the one way the console
// shows a person: the display name, which is the first and last name of
// the profile joined, and the username where both are empty.
//
// There is no column of its own for it. Your account edits first_name and
// last_name; LDAP and single sign-on write the whole name the directory
// or the identity provider gives into first_name and empty last_name
// (db/provider_fields.go), so their accounts show that name unchanged.
package people

import (
	"context"
	"database/sql"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Name is the display name of an account with the names and the username:
// first and last name joined by a space, each trimmed, else the username
// as it is.
func Name(first, last, username string) string {
	if name := strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last)); name != "" {
		return name
	}
	return username
}

// FullName is first and last name joined as Name joins them, "" when both
// are empty: the display name an account has besides its username.
func FullName(first, last string) string {
	return strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
}

// NameOf reads the display name of the account with the id; fallback when
// the account is gone or cannot be read.
func NameOf(ctx context.Context, q store.Querier, id int64, fallback string) string {
	if q == nil || id == 0 {
		return fallback
	}
	var username, first, last sql.NullString
	if err := q.QueryRow(ctx, `SELECT username, first_name, last_name FROM users WHERE id = ?`, id).Scan(&username, &first, &last); err != nil {
		return fallback
	}
	if name := Name(first.String, last.String, username.String); name != "" {
		return name
	}
	return fallback
}

// FullNameSQL is the SQL of FullName for the users table under the alias:
// TRIM(first_name || ' ' || last_name) with NULL as "", which both SQLite
// and PostgreSQL read.
func FullNameSQL(alias string) string {
	p := alias + "."
	return `TRIM(TRIM(COALESCE(` + p + `first_name, '')) || ' ' || TRIM(COALESCE(` + p + `last_name, '')))`
}
