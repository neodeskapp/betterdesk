package db

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

// accountName is a users row as the directory name migration reads it.
type accountName struct {
	id       int64
	name     string
	provider string
}

// directoryName is the name a directory signs an account in under: auth's
// LDAP result names it by the entry, trimmed and lower-cased.
func directoryName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// lowerCaseDirectoryAccounts gives every directory account (provider
// "ldap") whose name has capital letters or spaces at its ends the name
// its directory signs it in under, renamed with what keys on the name
// (rename). An account made before the sign-in used that name kept the
// spelling typed at its first sign-in and no longer matches it.
//
// A name that another account has in that form already, or that two
// accounts would share, stays as it is and is named in the log, for an
// admin to rename or delete one of them; so does one the rename refuses.
// Running it again changes nothing.
func lowerCaseDirectoryAccounts(accounts []accountName, rename func(id int64, name string) error) error {
	holders := map[string]int{}
	for _, a := range accounts {
		holders[directoryName(a.name)]++
	}
	for _, a := range accounts {
		want := directoryName(a.name)
		if !strings.EqualFold(strings.TrimSpace(a.provider), AuthProviderLDAP) || want == a.name {
			continue
		}
		if holders[want] > 1 {
			log.Printf("[db] WARN: directory account %q keeps its name: another account is also %q in lower case. Rename or delete one of them", a.name, want)
			continue
		}
		err := rename(a.id, want)
		switch {
		case err == nil:
			log.Printf("[db] directory account %q renamed to %q, the name its directory signs it in under", a.name, want)
		case errors.Is(err, ErrUsernameTaken), errors.Is(err, ErrInvalidUsername):
			log.Printf("[db] WARN: directory account %q keeps its name: %q cannot be used (%v). Rename or delete it", a.name, want, err)
		default:
			return fmt.Errorf("db: rename directory account %d: %w", a.id, err)
		}
	}
	return nil
}

// lowerCaseDirectoryAccounts runs the directory name migration on SQLite.
func (s *SQLiteDB) lowerCaseDirectoryAccounts() error {
	accounts, err := s.accountNames()
	if err != nil {
		return err
	}
	return lowerCaseDirectoryAccounts(accounts, s.RenameUser)
}

func (s *SQLiteDB) accountNames() ([]accountName, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT id, username, COALESCE(auth_provider, '') FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("db: list the account names: %w", err)
	}
	defer rows.Close()
	var out []accountName
	for rows.Next() {
		var a accountName
		if err := rows.Scan(&a.id, &a.name, &a.provider); err != nil {
			return nil, fmt.Errorf("db: list the account names: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// lowerCaseDirectoryAccounts runs the directory name migration on
// PostgreSQL.
func (pg *PostgresDB) lowerCaseDirectoryAccounts() error {
	rows, err := pg.pool.Query(pg.ctx, `SELECT id, username, COALESCE(auth_provider, '') FROM users ORDER BY id`)
	if err != nil {
		return fmt.Errorf("db: list the account names: %w", err)
	}
	var accounts []accountName
	for rows.Next() {
		var a accountName
		if err := rows.Scan(&a.id, &a.name, &a.provider); err != nil {
			rows.Close()
			return fmt.Errorf("db: list the account names: %w", err)
		}
		accounts = append(accounts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: list the account names: %w", err)
	}
	return lowerCaseDirectoryAccounts(accounts, pg.RenameUser)
}
