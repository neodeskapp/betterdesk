package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The queries on accounts and their strategy assignments that only this
// group makes.

// account is a row of the users table with the columns the routes read,
// each as the database's driver handed it to the old console.
type account struct {
	ID           store.Int64String
	Username     jsbind.Cell
	Role         jsbind.Cell
	AuthProvider jsbind.Cell
	CreatedAt    store.Time
	LastLogin    store.Time
	TOTPEnabled  jsbind.Cell
	Email        jsbind.Cell
	Disabled     store.Bool
}

const accountColumns = `id, username, role, auth_provider, created_at, last_login, totp_enabled, email, disabled`

// username is the account's username as text, "" for none.
func (a *account) username() string {
	if v := a.Username.Value(); v.Truthy() {
		return v.ToString()
	}
	return ""
}

// role is the account's role as text, "" for none.
func (a *account) role() string {
	if v := a.Role.Value(); v.IsString() {
		return v.Str()
	}
	return ""
}

func readAccounts(ctx context.Context, q store.Querier, query string, args ...any) ([]*account, error) {
	rows, err := store.Select(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*account{}
	for rows.Next() {
		a := &account{}
		if err := rows.Scan(&a.ID, &a.Username, &a.Role, &a.AuthProvider, &a.CreatedAt, &a.LastLogin, &a.TOTPEnabled, &a.Email, &a.Disabled); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// allAccounts returns every account, ordered by id (getAllUsers).
func allAccounts(ctx context.Context, db *store.DB) ([]*account, error) {
	accounts, err := readAccounts(ctx, db, `SELECT `+accountColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load users: %w", err)
	}
	return accounts, nil
}

// accountByID returns the account with the id, or nil (getUserById). The
// id is bound as the driver bound the number: a double on SQLite, text on
// PostgreSQL, where an id beyond the column's range is an error.
func accountByID(ctx context.Context, db *store.DB, id float64) (*account, error) {
	args, err := jsbind.Args(db.Dialect(), 1, jsbind.Number(id))
	if err != nil {
		return nil, err
	}
	accounts, err := readAccounts(ctx, db, `SELECT `+accountColumns+` FROM users WHERE id = ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	if len(accounts) == 0 {
		return nil, nil
	}
	return accounts[0], nil
}

// usernameTaken reports whether an account has the username
// (getUserByUsername).
func usernameTaken(ctx context.Context, db *store.DB, username string) (bool, error) {
	var one int
	err := db.QueryRow(ctx, `SELECT 1 FROM users WHERE username = ?`, username).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load user by name: %w", err)
	}
	return true, nil
}

// insertAccount stores a local account and returns its id as the users
// table's id column sends it (createUser).
func insertAccount(ctx context.Context, db *store.DB, username, passwordHash, role string) (store.Int64String, error) {
	id, err := db.Insert(ctx, `INSERT INTO users (username, password_hash, role, auth_provider) VALUES (?, ?, ?, 'local')`, username, passwordHash, role)
	if err != nil {
		return store.Int64String{}, fmt.Errorf("create user: %w", err)
	}
	return store.NewInt64String(id, db.Shape().Table("users").Type("id")), nil
}

// updateRole writes an account's role (updateUserRole).
func updateRole(ctx context.Context, db *store.DB, id int64, role string) error {
	if _, err := db.Exec(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id); err != nil {
		return fmt.Errorf("update role of user %d: %w", id, err)
	}
	return nil
}

// updatePasswordHash writes an account's password hash (updateUserPassword).
func updatePasswordHash(ctx context.Context, db *store.DB, id int64, hash string) error {
	if _, err := db.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
		return fmt.Errorf("update password of user %d: %w", id, err)
	}
	return nil
}

// setDisabled disables or enables an account.
func setDisabled(ctx context.Context, db *store.DB, id int64, disabled bool) error {
	flag := db.Pick("0", "FALSE")
	if disabled {
		flag = db.Pick("1", "TRUE")
	}
	if _, err := db.Exec(ctx, `UPDATE users SET disabled = `+flag+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("set disabled of user %d: %w", id, err)
	}
	return nil
}

// updateEmail writes an account's email address (updateUserProfile with
// the one field email). The routes hand it an address normalizeEmail
// checked, which is at most 200 UTF-16 units: the cut to 200 the profile
// writer made never changes it.
func updateEmail(ctx context.Context, db *store.DB, id int64, email string) error {
	if _, err := db.Exec(ctx, `UPDATE users SET email = ? WHERE id = ?`, email, id); err != nil {
		return fmt.Errorf("update email of user %d: %w", id, err)
	}
	return nil
}

// deleteAccount deletes an account with its user group memberships, its
// places on device group access lists and its picture (deleteUser). Its
// sessions, grants and strategy assignment stay.
func deleteAccount(ctx context.Context, db *store.DB, id int64) error {
	for _, query := range []string{
		`DELETE FROM user_group_members WHERE user_id = ?`,
		`DELETE FROM device_group_user_access WHERE user_id = ?`,
		`DELETE FROM user_avatars WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := db.Exec(ctx, query, id); err != nil {
			return fmt.Errorf("delete user %d: %w", id, err)
		}
	}
	return nil
}

// nameTakenByOther reports whether another account than the one with the
// id has the username.
func nameTakenByOther(ctx context.Context, db *store.DB, id int64, username string) (bool, error) {
	var n int64
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE username = ? AND id <> ?`, username, id).Scan(&n); err != nil {
		return false, fmt.Errorf("look up user name: %w", err)
	}
	return n > 0, nil
}

// renameOutcome is how a rename ended when no error stopped it.
type renameOutcome int

const (
	renamed renameOutcome = iota
	// renameMissing: no account has the id.
	renameMissing
	// renameTaken: another account has the name.
	renameTaken
)

var (
	// renameConflictSQLite and renameConflictPostgres are the messages of a
	// failed rename that count as a name taken in the meantime.
	renameConflictSQLite   = regexp.MustCompile(`(?i)unique|constraint`)
	renameConflictPostgres = regexp.MustCompile(`(?i)unique|duplicate key`)
	// missingTable is the message of a statement on a table that an older
	// database does not have.
	missingTable = regexp.MustCompile(`(?i)no such table|does not exist`)
)

// renameAccount gives an account a new username in one transaction,
// together with what is keyed by the name (renameUser and
// renameUserCascade). The name is not checked here.
func renameAccount(ctx context.Context, db *store.DB, id int64, newName string) (renameOutcome, error) {
	outcome := renamed
	err := db.InTx(ctx, func(tx *store.Tx) error {
		var old jsbind.Cell
		err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id = ?`, id).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			outcome = renameMissing
			return nil
		}
		if err != nil {
			return err
		}
		if v := old.Value(); v.IsString() && v.Str() == newName {
			return nil
		}
		var others int64
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE username = ? AND id <> ?`, newName, id).Scan(&others); err != nil {
			return err
		}
		if others > 0 {
			outcome = renameTaken
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET username = ? WHERE id = ?`, newName, id); err != nil {
			return err
		}
		return renameCascade(ctx, tx, db.Shape().Table("address_books").Has("username"), old.Value(), newName)
	})
	if err == nil {
		return outcome, nil
	}
	conflict := renameConflictSQLite
	if db.IsPostgres() {
		conflict = renameConflictPostgres
	}
	if conflict.MatchString(jsbind.Message(err)) {
		return renameTaken, nil
	}
	return renamed, fmt.Errorf("rename user %d: %w", id, err)
}

// renameCascade moves what is keyed by an account's name to its new name:
// its address books when booksByName says address_books is keyed by name
// (a stale book under the new name goes first; a table keyed by the
// account's id needs nothing), its organization memberships, the strategy
// assignments made for the name, and its sign-in attempts. A lockout of
// the new name is removed. Both names are trimmed first; nothing happens
// when one is empty or they are the same. A table the database does not
// have is skipped.
func renameCascade(ctx context.Context, tx *store.Tx, booksByName bool, oldValue jsbind.Value, newUsername string) error {
	oldName := ""
	if oldValue.Truthy() {
		oldName = jsbind.Trim(oldValue.ToString())
	}
	newName := jsbind.Trim(newUsername)
	if oldName == "" || newName == "" || oldName == newName {
		return nil
	}
	type step struct {
		query string
		args  []any
	}
	var steps []step
	if booksByName {
		steps = append(steps,
			step{`DELETE FROM address_books WHERE username = ?`, []any{newName}},
			step{`UPDATE address_books SET username = ? WHERE username = ?`, []any{newName, oldName}})
	}
	steps = append(steps,
		step{`UPDATE org_users SET username = ? WHERE username = ?`, []any{newName, oldName}},
		step{`UPDATE strategy_assignments SET target_key = ? WHERE target_key = ? AND target_type = 'user'`, []any{newName, oldName}},
		step{`DELETE FROM account_lockouts WHERE username = ?`, []any{newName}},
		step{`UPDATE login_attempts SET username = ? WHERE username = ?`, []any{newName, oldName}})
	for _, step := range steps {
		if err := cascadeStep(ctx, tx, step.query, step.args...); err != nil {
			return err
		}
	}
	return nil
}

// cascadeStep runs one statement of the cascade. A missing table is no
// error. On PostgreSQL a failed statement would end the transaction, so
// there the statement runs behind a savepoint that a missing table rolls
// back to.
func cascadeStep(ctx context.Context, tx *store.Tx, query string, args ...any) error {
	postgres := tx.Dialect() == store.Postgres
	if postgres {
		if _, err := tx.Exec(ctx, `SAVEPOINT rename_cascade`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, query, args...)
	if postgres {
		end := `RELEASE SAVEPOINT rename_cascade`
		if err != nil {
			end = `ROLLBACK TO SAVEPOINT rename_cascade`
		}
		if _, endErr := tx.Exec(ctx, end); endErr != nil {
			return endErr
		}
	}
	if err != nil && !missingTable.MatchString(jsbind.Message(err)) {
		return err
	}
	return nil
}

// newGUID is a random version 4 UUID, as crypto.randomUUID writes it.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make a guid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ensureAccountGUID returns the guid strategies are assigned to an account
// by, and gives the account a random one when it has none (ensureUserGuid).
// It is "" for an id that is no positive number and for an unknown
// account.
func ensureAccountGUID(ctx context.Context, db *store.DB, id int64) (string, error) {
	if id <= 0 {
		return "", nil
	}
	var rowID int64
	var guid string
	err := db.QueryRow(ctx, `SELECT id, COALESCE(guid, '') AS guid FROM users WHERE id = ?`, id).Scan(&rowID, &guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load guid of user %d: %w", id, err)
	}
	if guid != "" {
		return guid, nil
	}
	if guid, err = newGUID(); err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `UPDATE users SET guid = ? WHERE id = ?`, guid, rowID); err != nil {
		return "", fmt.Errorf("store guid of user %d: %w", id, err)
	}
	return guid, nil
}

// strategyOf returns the guid of the strategy assigned to an account, ""
// for none (getUserStrategyGuid). It writes nothing: an account without a
// guid has no strategy, and gets its guid when one is assigned.
func strategyOf(ctx context.Context, db *store.DB, id int64) (string, error) {
	var guid sql.NullString
	err := db.QueryRow(ctx, `SELECT guid FROM users WHERE id = ?`, id).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load guid of user %d: %w", id, err)
	}
	if guid.String == "" {
		return "", nil
	}
	var strategy sql.NullString
	err = db.QueryRow(ctx, `SELECT strategy_guid FROM strategy_assignments WHERE target_type = 'user' AND target_key = ? LIMIT 1`, guid.String).Scan(&strategy)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load strategy of user %d: %w", id, err)
	}
	return strategy.String, nil
}

// errUserNotFound and errStrategyNotFound are what setUserStrategyAssignment
// threw.
var (
	errUserNotFound     = errors.New("user not found")
	errStrategyNotFound = badRequestKey("users.strategy_not_found")
)

// userGroupExists reports whether a user group has the guid.
func userGroupExists(ctx context.Context, db *store.DB, guid string) (bool, error) {
	var one int
	err := db.QueryRow(ctx, `SELECT 1 FROM user_groups WHERE guid = ?`, guid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load user group %q: %w", guid, err)
	}
	return true, nil
}

// strategyExists reports whether a strategy has the guid.
func strategyExists(ctx context.Context, db *store.DB, guid string) (bool, error) {
	var one int
	err := db.QueryRow(ctx, `SELECT 1 FROM strategies WHERE guid = ?`, guid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load strategy %q: %w", guid, err)
	}
	return true, nil
}

// assignStrategy assigns a strategy that checkScope found to an account,
// or takes the account's assignment away for ""
// (setUserStrategyAssignment). It returns what is assigned now.
func assignStrategy(ctx context.Context, db *store.DB, id int64, strategyGUID string) (string, error) {
	guid, err := ensureAccountGUID(ctx, db, id)
	if err != nil {
		return "", err
	}
	if guid == "" {
		return "", errUserNotFound
	}
	if strategyGUID == "" {
		if _, err := db.Exec(ctx, `DELETE FROM strategy_assignments WHERE target_type = 'user' AND target_key = ?`, guid); err != nil {
			return "", fmt.Errorf("remove strategy of user %d: %w", id, err)
		}
		return "", nil
	}
	if _, err := db.Exec(ctx, `INSERT INTO strategy_assignments (target_type, target_key, strategy_guid, updated_at)
		VALUES ('user', ?, ?, `+store.NowSQL(db.Dialect())+`)
		ON CONFLICT (target_type, target_key) DO UPDATE SET
			strategy_guid = excluded.strategy_guid,
			updated_at = excluded.updated_at`, guid, strategyGUID); err != nil {
		return "", fmt.Errorf("assign strategy to user %d: %w", id, err)
	}
	return strategyGUID, nil
}

// assignmentKey returns the guid an account is assigned strategies by,
// found by its guid, its username or its id as text, and gives the account
// a random guid when it has none (resolveUserAssignmentKey).
func assignmentKey(ctx context.Context, db *store.DB, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("empty user reference")
	}
	var id int64
	var guid string
	err := db.QueryRow(ctx, `SELECT id, COALESCE(guid, '') AS guid FROM users
		WHERE guid = ? OR username = ? OR CAST(id AS TEXT) = ?
		LIMIT 1`, ref, ref, ref).Scan(&id, &guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load user %q: %w", ref, err)
	}
	if guid != "" {
		return guid, nil
	}
	if guid, err = newGUID(); err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `UPDATE users SET guid = ? WHERE id = ?`, guid, id); err != nil {
		return "", fmt.Errorf("store guid of user %d: %w", id, err)
	}
	return guid, nil
}
