package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/unitronix/betterdesk-server/logging"
)

// The console's tables, one file per table and database. schema.md lists
// every step Migrate runs with them.
//
//go:embed schema/sqlite/*.sql schema/postgres/*.sql
var schemaFiles embed.FS

// logComponent is the prefix of the lines Migrate logs.
const logComponent = "DB"

// advisoryLockClass is the first key of the PostgreSQL advisory lock Migrate
// holds; the second key is a hash of the schema name, so two processes
// that migrate the same schema wait for each other.
const advisoryLockClass = 1651794787

// Migrate brings the console's tables to the current shape: it creates the
// tables and indexes that are missing, adds the columns later versions
// added and runs the one-time data fixes that went with them. Every step
// checks what is there first, so running it again changes nothing.
//
// It only adds. A table that exists is never recreated, also when the
// signal server made it first with other column types; Shape tells which
// variant is there.
//
// panel_sessions is not created here. It belongs to the session package.
func (d *DB) Migrate(ctx context.Context) error {
	conn, err := d.write.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate console schema: %w", err)
	}
	defer conn.Close()

	if d.dialect == Postgres {
		unlock, err := lockSchema(ctx, conn)
		if err != nil {
			return fmt.Errorf("migrate console schema: %w", err)
		}
		defer unlock()
	}

	m := &migration{dialect: d.dialect}
	steps := migrationSteps(d.dialect)
	for len(steps) > 0 {
		// Steps that only run a schema file share a transaction with their
		// neighbours of the same kind: a fresh database is set up with a
		// handful of commits instead of one per table.
		n := 1
		for steps[0].file && n < len(steps) && steps[n].file {
			n++
		}
		failed, err := m.run(ctx, conn, steps[:n])
		steps = steps[n:]
		if err == nil {
			continue
		}
		if !failed.lenient || ctx.Err() != nil {
			return fmt.Errorf("migrate console schema: %s: %w", failed.name, err)
		}
		logging.Logf(logComponent, "WARN: Migration %s error: %v", failed.name, err)
	}
	if len(m.created) > 0 {
		logging.Logf(logComponent, "Migration: created %d tables: %s", len(m.created), strings.Join(m.created, ", "))
	}

	shape, err := inspect(ctx, conn, d.dialect)
	if err != nil {
		return fmt.Errorf("migrate console schema: %w", err)
	}
	d.setShape(shape)
	return nil
}

// lockSchema takes the advisory lock on conn and returns the function that
// releases it. A connection whose lock cannot be released is closed for
// good, which releases it too.
func lockSchema(ctx context.Context, conn *sql.Conn) (func(), error) {
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, hashtext(current_schema()))`, advisoryLockClass); err != nil {
		return nil, fmt.Errorf("take the schema lock: %w", err)
	}
	return func() {
		_, err := conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1, hashtext(current_schema()))`, advisoryLockClass)
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}, nil
}

// step is one unit of the migration. It runs in a transaction.
type step struct {
	name string
	// file steps only run a schema file. They are never lenient.
	file bool
	// lenient steps log a warning when they fail and the migration goes on,
	// so one table in an unexpected state does not keep the console from
	// starting.
	lenient bool
	run     func(ctx context.Context, tx *Tx, m *migration) error
}

type migration struct {
	dialect Dialect
	created []string
}

// run runs the steps in one transaction. When one fails nothing of the
// transaction stays, and run returns that step with the error. A
// transaction that cannot begin or commit is never a failure to tolerate.
func (m *migration) run(ctx context.Context, conn *sql.Conn, steps []step) (failed step, err error) {
	sqlTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return step{name: steps[0].name}, fmt.Errorf("begin: %w", err)
	}
	created := len(m.created)
	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
		if err != nil {
			m.created = m.created[:created]
			_ = sqlTx.Rollback()
			return
		}
		if cErr := sqlTx.Commit(); cErr != nil {
			m.created = m.created[:created]
			failed, err = step{name: steps[len(steps)-1].name}, fmt.Errorf("commit: %w", cErr)
		}
	}()
	tx := &Tx{tx: sqlTx, dialect: m.dialect}
	for _, st := range steps {
		if err := st.run(ctx, tx, m); err != nil {
			return st, err
		}
	}
	return step{}, nil
}

func migrationSteps(dialect Dialect) []step {
	if dialect == Postgres {
		return postgresSteps()
	}
	return sqliteSteps()
}

// sqliteSteps is the order in which the console set up a SQLite file.
func sqliteSteps() []step {
	var s []step
	s = append(s, createTable("peer"))
	s = append(s, addColumns(lenient, "peer",
		"status_online", "INTEGER DEFAULT 0",
		"last_online", "TEXT",
		"is_deleted", "INTEGER DEFAULT 0",
		"user", "TEXT DEFAULT ''",
		"is_banned", "INTEGER DEFAULT 0",
		"banned_at", "TEXT",
		"banned_reason", "TEXT DEFAULT ''",
		"folder_id", "INTEGER DEFAULT NULL",
		"tags", "TEXT DEFAULT ''",
	)...)
	s = append(s, createTables(
		"device_inventory", "device_telemetry",
		"activity_sessions", "activity_summaries",
		"tickets", "ticket_comments", "ticket_attachments",
		"alert_rules", "alert_history", "remote_commands",
		"network_targets", "network_checks",
		"dlp_policies", "dlp_events",
	)...)
	s = append(s, addColumns(lenient, "dlp_policies",
		"policy_type", "TEXT DEFAULT ''",
		"action", "TEXT DEFAULT 'log'",
		"scope", "TEXT DEFAULT ''",
	)...)
	s = append(s, createTables(
		"saved_reports", "studio_flows",
		"tenants", "tenant_devices", "tenant_users",
		"pending_registrations",
		"agent_bundles", "agent_templates", "agent_bundle_builds",
	)...)
	s = append(s, addColumns(strict, "agent_bundles",
		"config_fingerprint", "TEXT NOT NULL DEFAULT ''",
		"client_commit", "TEXT DEFAULT NULL",
		"active_generation_hash", "TEXT DEFAULT NULL",
	)...)
	s = append(s, addColumns(strict, "agent_bundle_builds",
		"client_commit", "TEXT DEFAULT NULL",
		"generation_id", "TEXT DEFAULT NULL",
		"config_fingerprint", "TEXT DEFAULT NULL",
		"progress_percent", "INTEGER NOT NULL DEFAULT 0",
		"progress_phase", "TEXT DEFAULT ''",
	)...)
	s = append(s, agentBundleSlugs(), agentBundleProductTypes())
	s = append(s, createTables(
		"users", "audit_log", "access_tokens", "login_attempts", "account_lockouts",
		"folders", "address_books", "settings", "branding_config", "branding_profiles",
		"relay_sessions", "device_folder_assignments", "peer_sysinfo", "peer_metrics",
		"audit_connections", "audit_files", "audit_alarms",
		"user_groups", "panel_access_policies", "client_feature_policies", "user_group_members",
		"device_groups", "device_group_members", "device_group_user_access", "device_group_user_group_access",
		"strategies", "strategy_assignments", "user_peer_grants",
		"notification_reads", "email_notification_log",
	)...)
	s = append(s, addColumns(lenient, "users",
		"last_login", "TEXT",
		"auth_provider", "TEXT DEFAULT 'local'",
		"preferred_language", "TEXT DEFAULT NULL",
		"totp_secret", "TEXT DEFAULT NULL",
		"totp_enabled", "INTEGER DEFAULT 0",
		"totp_recovery_codes", "TEXT DEFAULT NULL",
		"first_name", "TEXT DEFAULT ''",
		"last_name", "TEXT DEFAULT ''",
		"email", "TEXT DEFAULT ''",
		"phone", "TEXT DEFAULT ''",
		"role_display", "TEXT DEFAULT ''",
		"avatar_url", "TEXT DEFAULT ''",
		"guid", "TEXT DEFAULT ''",
	)...)
	avatars := createTable("user_avatars")
	avatars.file, avatars.lenient = false, true
	s = append(s, avatars)
	s = append(s, addTimeColumn("settings", "updated_at"), addTimeColumn("branding_config", "updated_at"))
	s = append(s, addColumns(lenient, "device_groups",
		"source_type", "TEXT DEFAULT 'manual'",
		"tag_filter", "TEXT DEFAULT ''",
	)...)
	s = append(s, seedDefaultGroup("user_groups", "Default user group"), seedDefaultGroup("device_groups", "Default device group"))
	s = append(s, accessTokenHashes())
	s = append(s, accessTokenKind(), accessTokensAsHashes(), dropBackupShareLogin())
	s = append(s, userDisabled("INTEGER NOT NULL DEFAULT 0"))
	return s
}

// postgresSteps is the order in which the console set up a PostgreSQL
// database.
func postgresSteps() []step {
	var s []step
	s = append(s, createTables("peer", "users", "audit_log", "access_tokens")...)
	s = append(s, accessTokenHashes())
	s = append(s, createTables(
		"login_attempts", "account_lockouts", "folders", "address_books",
		"notification_reads", "email_notification_log",
		"settings", "branding_config", "branding_profiles", "relay_sessions",
		"device_inventory", "device_telemetry",
		"tickets", "ticket_comments", "ticket_attachments",
		"activity_sessions", "activity_summaries",
		"alert_rules", "alert_history", "remote_commands",
		"network_targets", "network_checks",
	)...)
	s = append(s, runScript("indexes_core"))
	s = append(s, createTable("dlp_policies"))
	s = append(s, addColumns(strict, "dlp_policies",
		"policy_type", "TEXT DEFAULT ''",
		"action", "TEXT DEFAULT 'log'",
		"scope", "TEXT DEFAULT ''",
	)...)
	s = append(s, createTables("dlp_events", "saved_reports", "studio_flows", "tenants", "tenant_devices", "tenant_users")...)
	s = append(s, runScript("indexes_tenants"))
	s = append(s, createTable("pending_registrations"))
	s = append(s, createTable("agent_templates"))
	s = append(s, addColumns(strict, "agent_templates",
		"enrolled_device_id", "TEXT DEFAULT NULL",
		"enrolled_at", "TIMESTAMPTZ DEFAULT NULL",
	)...)
	s = append(s, runScript("indexes_agent_templates"))
	s = append(s, createTable("agent_bundles"))
	s = append(s, addColumns(strict, "agent_bundles",
		"config_fingerprint", "TEXT NOT NULL DEFAULT ''",
		"client_commit", "TEXT DEFAULT NULL",
		"active_generation_hash", "TEXT DEFAULT NULL",
	)...)
	s = append(s, runScript("indexes_agent_bundles"))
	s = append(s, agentBundleSlugs(), agentBundleProductTypes())
	s = append(s, createTable("agent_bundle_builds"))
	s = append(s, addColumns(strict, "agent_bundle_builds",
		"client_commit", "TEXT DEFAULT NULL",
		"generation_id", "TEXT DEFAULT NULL",
		"config_fingerprint", "TEXT DEFAULT NULL",
		"progress_percent", "INTEGER NOT NULL DEFAULT 0",
		"progress_phase", "TEXT DEFAULT ''",
	)...)
	s = append(s, runScript("indexes_agent_bundle_builds"))
	s = append(s, createTables(
		"device_folder_assignments", "peer_sysinfo", "peer_metrics",
		"audit_connections", "audit_files", "audit_alarms",
		"user_groups", "panel_access_policies", "client_feature_policies", "user_group_members",
		"device_groups", "device_group_members", "device_group_user_access", "device_group_user_group_access",
		"strategies", "strategy_assignments", "user_peer_grants",
	)...)
	s = append(s, seedDefaultGroup("user_groups", "Default user group"), seedDefaultGroup("device_groups", "Default device group"))
	s = append(s, addColumns(strict, "users",
		"last_login", "TIMESTAMPTZ",
		"auth_provider", "TEXT DEFAULT 'local'",
		"preferred_language", "TEXT DEFAULT NULL",
		"totp_secret", "TEXT DEFAULT NULL",
		"totp_enabled", "BOOLEAN DEFAULT FALSE",
		"totp_recovery_codes", "TEXT DEFAULT NULL",
		"guid", "TEXT DEFAULT ''",
	)...)
	s = append(s, addColumns(strict, "device_groups",
		"source_type", "TEXT DEFAULT 'manual'",
		"tag_filter", "TEXT DEFAULT ''",
	)...)
	s = append(s, addColumns(strict, "users",
		"first_name", "TEXT DEFAULT ''",
		"last_name", "TEXT DEFAULT ''",
		"email", "TEXT DEFAULT ''",
		"phone", "TEXT DEFAULT ''",
		"role_display", "TEXT DEFAULT ''",
		"avatar_url", "TEXT DEFAULT ''",
	)...)
	s = append(s, createTable("user_avatars"))
	s = append(s, addColumns(lenient, "settings", "updated_at", "TIMESTAMPTZ DEFAULT NOW()")...)
	s = append(s, widenConditionValue())
	s = append(s, accessTokenKind(), accessTokensAsHashes(), dropBackupShareLogin())
	s = append(s, userDisabled("BOOLEAN NOT NULL DEFAULT FALSE"))
	return s
}

// accessTokenKind adds access_tokens.token_kind: "device" for a token the
// console issued for a device, "account" for the token of an account's
// sign-in. Only a device token proves a device. Every row that is there
// when the column comes was made by a sign-in (no console issued device
// tokens before the column), so the default gives each "account"; so does
// it to the rows of anything that writes the table without naming the
// kind.
func accessTokenKind() step {
	return addColumn(strict, "access_tokens", "token_kind", "TEXT NOT NULL DEFAULT 'account'")
}

// userDisabled adds users.disabled: an account that may not sign in, whose
// tokens are refused, and which keeps its row and history. Every account
// that is there when the column comes is enabled. The signal server adds
// the same column when it makes or finds the table first.
func userDisabled(definition string) step {
	return addColumn(strict, "users", "disabled", definition)
}

// accessTokensAsHashes keeps every access token as its hash only (BUGS
// RV128): a row whose token column holds the token itself gets the hash of
// that token and StoredAccessToken(hash) in place of the token. The console
// writes rows that way; the rows it finds otherwise come from before, or
// from an older console after a rollback, and are rewritten at the next
// start. A row without a token stays as it is.
func accessTokensAsHashes() step {
	return step{
		name: "keep access tokens as their hash",
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			type tokenRow struct {
				id    int64
				token string
			}
			rows, err := tx.Query(ctx, `SELECT id, token FROM access_tokens WHERE token <> '' AND token NOT LIKE ?`, storedTokenPrefix+"%")
			if err != nil {
				return err
			}
			var plain []tokenRow
			for rows.Next() {
				var r tokenRow
				if err := rows.Scan(&r.id, &r.token); err != nil {
					rows.Close()
					return err
				}
				plain = append(plain, r)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			for _, r := range plain {
				hash := HashAccessToken(r.token)
				if _, err := tx.Exec(ctx, `UPDATE access_tokens SET token_hash = ?, token = ? WHERE id = ?`, hash, StoredAccessToken(hash), r.id); err != nil {
					return err
				}
			}
			if len(plain) > 0 {
				logging.Logf(logComponent, "Migration: kept %d access tokens as their hash only", len(plain))
			}
			return nil
		},
	}
}

// dropBackupShareLogin deletes the scheduled backup's network share
// login, the setting scheduled_backup_smb, which held the share's password
// in clear (BUGS L123). The console no longer has the login: a backup goes
// to a folder, where the operating system mounts a share (DECISIONS A155),
// and nothing writes the setting again.
func dropBackupShareLogin() step {
	return step{
		name: "drop the backup share login",
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			res, err := tx.Exec(ctx, `DELETE FROM settings WHERE key = 'scheduled_backup_smb'`)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				logging.Logf(logComponent, "Migration: deleted the scheduled backup's network share login")
			}
			return nil
		},
	}
}

// widenConditionValue makes alert_rules.condition_value double precision
// where the Node console made it a four-byte real, which kept 16777217 as
// 16777216 (BUGS AE4). Every stored limit keeps the value the console read:
// the shortest number that names the four bytes, as PostgreSQL prints a
// real (0.1), not the wider binary value the cast alone gives
// (0.10000000149011612). An older version reads the wider column as it
// read the real.
func widenConditionValue() step {
	return step{
		name: "widen alert_rules.condition_value",
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			var dataType string
			err := tx.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'alert_rules' AND column_name = 'condition_value'`).Scan(&dataType)
			if errors.Is(err, sql.ErrNoRows) || err == nil && dataType != "real" {
				return nil
			}
			if err != nil {
				return err
			}
			type limit struct {
				id    int64
				value float64
			}
			rows, err := tx.Query(ctx, `SELECT id, condition_value FROM alert_rules`)
			if err != nil {
				return err
			}
			var limits []limit
			for rows.Next() {
				var l limit
				if err := rows.Scan(&l.id, &l.value); err != nil {
					rows.Close()
					return err
				}
				limits = append(limits, l)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `ALTER TABLE alert_rules ALTER COLUMN condition_value TYPE DOUBLE PRECISION`); err != nil {
				return err
			}
			for _, l := range limits {
				if math.IsNaN(l.value) || math.IsInf(l.value, 0) {
					continue
				}
				short, err := strconv.ParseFloat(strconv.FormatFloat(l.value, 'g', -1, 32), 64)
				if err != nil || short == l.value {
					continue
				}
				if _, err := tx.Exec(ctx, `UPDATE alert_rules SET condition_value = ? WHERE id = ?`, short, l.id); err != nil {
					return err
				}
			}
			logging.Logf(logComponent, "Migration: widened alert_rules.condition_value to double precision")
			return nil
		},
	}
}

// createTable runs the table's file: CREATE TABLE IF NOT EXISTS and the
// indexes that go with it.
func createTable(table string) step {
	return step{
		name: "create " + table,
		file: true,
		run: func(ctx context.Context, tx *Tx, m *migration) error {
			existed, err := hasTable(ctx, tx, table)
			if err != nil {
				return err
			}
			if err := execFile(ctx, tx, table); err != nil {
				return err
			}
			if !existed {
				m.created = append(m.created, table)
			}
			return nil
		},
	}
}

func createTables(tables ...string) []step {
	steps := make([]step, 0, len(tables))
	for _, t := range tables {
		steps = append(steps, createTable(t))
	}
	return steps
}

// runScript runs a file of statements that belongs to no single table.
func runScript(name string) step {
	return step{
		name: "run " + name,
		file: true,
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			return execFile(ctx, tx, name)
		},
	}
}

func execFile(ctx context.Context, tx *Tx, name string) error {
	path := "schema/" + tx.dialect.String() + "/" + name + ".sql"
	text, err := schemaFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for _, stmt := range splitStatements(strings.ReplaceAll(string(text), "\r\n", "\n")) {
		if _, err := tx.tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// splitStatements cuts a schema file at the semicolons that end a statement
// and drops comments. The text of each statement is otherwise untouched:
// SQLite stores it.
func splitStatements(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if stmt := strings.TrimSpace(b.String()); stmt != "" {
			out = append(out, stmt)
		}
		b.Reset()
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\'' || c == '"':
			end := closingQuote(text, i)
			b.WriteString(text[i:end])
			i = end - 1
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			i += end - 1
		case c == ';':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}

func firstLine(stmt string) string {
	line, _, _ := strings.Cut(stmt, "\n")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "("))
}

const (
	strict  = false
	lenient = true
)

// addColumns makes one step per column: pairs of name and definition, in the
// order they are added.
func addColumns(tolerant bool, table string, pairs ...string) []step {
	steps := make([]step, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		steps = append(steps, addColumn(tolerant, table, pairs[i], pairs[i+1]))
	}
	return steps
}

// addColumn adds a column when the table exists and does not have it yet.
func addColumn(tolerant bool, table, column, definition string) step {
	return step{
		name:    "add " + table + "." + column,
		lenient: tolerant,
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			return ensureColumn(ctx, tx, table, column, definition)
		},
	}
}

// addTimeColumn adds a time column to a SQLite table, which cannot get a
// column whose default is an expression (datetime('now')) once it has rows:
// the column comes as TEXT without a default, and the rows the table has
// get the time of the migration, as such a default would have given them.
func addTimeColumn(table, column string) step {
	return step{
		name:    "add " + table + "." + column,
		lenient: lenient,
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			cols, err := tableColumns(ctx, tx, table)
			if err != nil {
				return err
			}
			if _, ok := cols[column]; ok || len(cols) == 0 {
				return nil
			}
			if err := ensureColumn(ctx, tx, table, column, "TEXT"); err != nil {
				return err
			}
			_, err = tx.tx.ExecContext(ctx, "UPDATE "+table+" SET "+column+" = datetime('now')")
			return err
		},
	}
}

// ensureColumn adds the column unless the table has it or does not exist.
func ensureColumn(ctx context.Context, tx *Tx, table, column, definition string) error {
	cols, err := tableColumns(ctx, tx, table)
	if err != nil {
		return err
	}
	if _, ok := cols[column]; ok || len(cols) == 0 {
		return nil
	}
	ddl := "ALTER TABLE " + table + " ADD COLUMN " + tx.Pick("", "IF NOT EXISTS ") + column + " " + definition
	if _, err := tx.tx.ExecContext(ctx, ddl); err != nil {
		return err
	}
	logging.Logf(logComponent, "Migration: added %s.%s", table, column)
	return nil
}

// columnState is what the migration needs to know about an existing column.
type columnState struct {
	nullable   bool
	defaultSQL string
}

// tableColumns returns the columns of a table in the current schema, and
// nothing when the table does not exist.
func tableColumns(ctx context.Context, tx *Tx, table string) (map[string]columnState, error) {
	query := `SELECT name, "notnull" = 0, COALESCE(dflt_value, '') FROM pragma_table_info(?)`
	if tx.dialect == Postgres {
		query = `SELECT column_name, is_nullable = 'YES', COALESCE(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ?`
	}
	rows, err := tx.Query(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	defer rows.Close()
	cols := map[string]columnState{}
	for rows.Next() {
		var name string
		var c columnState
		if err := rows.Scan(&name, &c.nullable, &c.defaultSQL); err != nil {
			return nil, fmt.Errorf("read the columns of %s: %w", table, err)
		}
		cols[name] = c
	}
	return cols, rows.Err()
}

func hasTable(ctx context.Context, tx *Tx, table string) (bool, error) {
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if tx.dialect == Postgres {
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?`
	}
	var n int
	if err := tx.QueryRow(ctx, query, table).Scan(&n); err != nil {
		return false, fmt.Errorf("look for table %s: %w", table, err)
	}
	return n > 0, nil
}

// seedDefaultGroup gives an empty group table its "Default" group.
func seedDefaultGroup(table, note string) step {
	return step{
		name: "seed " + table,
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			var n int
			if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				return nil
			}
			if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (guid, name, note) VALUES (?, ?, ?)", uuid.NewString(), "Default", note); err != nil {
				return err
			}
			logging.Logf(logComponent, "Migration: added the default row to %s", table)
			return nil
		},
	}
}

// HashAccessToken is how an access token of a desktop client is stored for
// lookup: the SHA-256 of its text, in hex.
func HashAccessToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// storedTokenPrefix starts what access_tokens.token holds in place of a
// token.
const storedTokenPrefix = "sha256 "

// StoredAccessToken is what access_tokens.token holds for the token with
// this hash: the token is kept as its hash only. The column takes no NULL
// and no value twice, so it holds "sha256 " and the hash; the space keeps
// it from ever being a token, which holds no white space.
func StoredAccessToken(hash string) string { return storedTokenPrefix + hash }

// accessTokenHashIndex is written with the line break and indent the Node
// console's statement had, because SQLite stores the text.
const accessTokenHashIndex = "CREATE UNIQUE INDEX IF NOT EXISTS idx_access_tokens_hash ON access_tokens (token_hash)\n" +
	"            WHERE token_hash IS NOT NULL"

// accessTokenHashes adds access_tokens.token_hash with its unique index and
// fills it for the tokens that were stored before the column existed.
func accessTokenHashes() step {
	return step{
		name: "hash access tokens",
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			if err := ensureColumn(ctx, tx, "access_tokens", "token_hash", "TEXT"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, accessTokenHashIndex); err != nil {
				return err
			}
			type tokenRow struct {
				id    int64
				token string
			}
			rows, err := tx.Query(ctx, `SELECT id, token FROM access_tokens
				WHERE (token_hash IS NULL OR token_hash = '') AND token <> ''`)
			if err != nil {
				return err
			}
			var missing []tokenRow
			for rows.Next() {
				var r tokenRow
				if err := rows.Scan(&r.id, &r.token); err != nil {
					rows.Close()
					return err
				}
				missing = append(missing, r)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			for _, r := range missing {
				if _, err := tx.Exec(ctx, `UPDATE access_tokens SET token_hash = ? WHERE id = ?`, HashAccessToken(r.token), r.id); err != nil {
					return err
				}
			}
			if len(missing) > 0 {
				logging.Logf(logComponent, "Migration: hashed %d access tokens", len(missing))
			}
			return nil
		},
	}
}

// agentBundleSlugs adds agent_bundles.slug with its unique index and gives
// every bundle without a slug one made from its name.
func agentBundleSlugs() step {
	return step{
		name:    "agent_bundles.slug",
		lenient: true,
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			if err := ensureColumn(ctx, tx, "agent_bundles", "slug", "TEXT DEFAULT NULL"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_bundles_slug ON agent_bundles (slug) WHERE slug IS NOT NULL AND slug != ''`); err != nil {
				return err
			}
			taken := map[string]bool{}
			rows, err := tx.Query(ctx, `SELECT slug FROM agent_bundles WHERE slug IS NOT NULL AND slug != ''`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var slug string
				if err := rows.Scan(&slug); err != nil {
					rows.Close()
					return err
				}
				taken[slug] = true
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}

			type bundleRow struct {
				id       int64
				bundleID string
				name     string
			}
			rows, err = tx.Query(ctx, `SELECT id, bundle_id, name FROM agent_bundles WHERE slug IS NULL OR slug = ''`)
			if err != nil {
				return err
			}
			var missing []bundleRow
			for rows.Next() {
				var r bundleRow
				if err := rows.Scan(&r.id, &r.bundleID, &r.name); err != nil {
					rows.Close()
					return err
				}
				missing = append(missing, r)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			for _, r := range missing {
				slug := AllocateBundleSlug("", r.name, r.bundleID, func(s string) bool { return taken[s] })
				if _, err := tx.Exec(ctx, `UPDATE agent_bundles SET slug = ? WHERE id = ?`, slug, r.id); err != nil {
					return err
				}
				taken[slug] = true
			}
			if len(missing) > 0 {
				logging.Logf(logComponent, "Migration: gave %d agent bundles a slug", len(missing))
			}
			return nil
		},
	}
}

// productTypeFix sets every product type that is not the one current
// product to it.
const productTypeFix = `
	UPDATE agent_bundles
	SET product_type = CASE LOWER(TRIM(COALESCE(product_type, '')))
		WHEN 'betterdesk-support' THEN 'betterdesk-support'
		WHEN 'betterdesk_support' THEN 'betterdesk-support'
		WHEN 'rdclient' THEN 'betterdesk-support'
		WHEN 'agent-client' THEN 'betterdesk-support'
		WHEN 'agent_client' THEN 'betterdesk-support'
		WHEN 'support-agent' THEN 'betterdesk-support'
		WHEN 'support_agent' THEN 'betterdesk-support'
		WHEN 'agent' THEN 'betterdesk-support'
		ELSE 'betterdesk-support'
	END
	WHERE product_type IS NULL
	   OR LOWER(TRIM(product_type)) NOT IN ('betterdesk-support')`

// agentBundleProductTypes adds agent_bundles.product_type and rewrites old
// values. On SQLite the rewrite runs at every start. On PostgreSQL it runs
// only when the column was already there, and the column then also gets
// NOT NULL and its default when an older version made it without them.
func agentBundleProductTypes() step {
	const column, definition = "product_type", "TEXT NOT NULL DEFAULT 'betterdesk-support'"
	return step{
		name:    "agent_bundles.product_type",
		lenient: true,
		run: func(ctx context.Context, tx *Tx, _ *migration) error {
			cols, err := tableColumns(ctx, tx, "agent_bundles")
			if err != nil {
				return err
			}
			state, existed := cols[column]
			if err := ensureColumn(ctx, tx, "agent_bundles", column, definition); err != nil {
				return err
			}
			if tx.dialect == Postgres && !existed {
				return nil
			}
			res, err := tx.Exec(ctx, productTypeFix)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				logging.Logf(logComponent, "Migration: set product_type of %d agent bundles", n)
			}
			if tx.dialect != Postgres {
				return nil
			}
			if state.nullable {
				if _, err := tx.Exec(ctx, `ALTER TABLE agent_bundles ALTER COLUMN product_type SET NOT NULL`); err != nil {
					return err
				}
				logging.Logf(logComponent, "Migration: agent_bundles.product_type is now NOT NULL")
			}
			if !strings.Contains(state.defaultSQL, "betterdesk-support") {
				if _, err := tx.Exec(ctx, `ALTER TABLE agent_bundles ALTER COLUMN product_type SET DEFAULT 'betterdesk-support'`); err != nil {
					return err
				}
				logging.Logf(logComponent, "Migration: agent_bundles.product_type has its default")
			}
			return nil
		},
	}
}

// Limits of the public name of an agent bundle.
const (
	maxSlugLength = 32
	minSlugLength = 2
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// slugLetters maps letters with diacritics to plain ones before a name
// becomes a slug.
var slugLetters = map[rune]string{
	'ą': "a", 'ć': "c", 'ę': "e", 'ł': "l", 'ń': "n", 'ó': "o", 'ś': "s", 'ź': "z", 'ż': "z",
	'ä': "a", 'ö': "o", 'ü': "u", 'ß': "ss", 'æ': "ae", 'ø': "o", 'å': "a",
	'č': "c", 'ď': "d", 'ě': "e", 'ň': "n", 'ř': "r", 'š': "s", 'ť': "t", 'ů': "u", 'ý': "y", 'ž': "z",
}

// SlugifyBundleName turns a label into a slug: lower case, letters with
// diacritics replaced, every other run of characters a single dash, at most
// 32 characters. The result can be empty.
func SlugifyBundleName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range lowerLikeJS(name) {
		out := string(r)
		if repl, ok := slugLetters[r]; ok {
			out = repl
		}
		for _, c := range out {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				if dash && b.Len() > 0 {
					b.WriteByte('-')
				}
				dash = false
				b.WriteRune(c)
			} else {
				dash = true
			}
		}
	}
	slug := b.String()
	if len(slug) > maxSlugLength {
		slug = strings.TrimSuffix(slug[:maxSlugLength], "-")
	}
	return slug
}

// lowerLikeJS lower-cases the way JavaScript's toLowerCase does where that
// changes the ASCII letters of a slug: the dotted capital I becomes "i"
// followed by a combining dot.
func lowerLikeJS(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
}

func validSlug(slug string) bool {
	return len(slug) >= minSlugLength && len(slug) <= maxSlugLength && slugPattern.MatchString(slug)
}

// AllocateBundleSlug picks a slug nobody has: from preferred, else from
// name, else from fallbackID, with -2, -3 and so on appended while taken
// reports the candidate as used.
func AllocateBundleSlug(preferred, name, fallbackID string, taken func(string) bool) string {
	base := ""
	for _, candidate := range []string{preferred, name, fallbackID} {
		if slug := SlugifyBundleName(candidate); validSlug(slug) {
			base = slug
			break
		}
	}
	if base == "" && fallbackID != "" {
		base = sliceUTF16(fallbackID, maxSlugLength)
	}
	if base == "" {
		var id [6]byte
		_, _ = rand.Read(id[:])
		base = hex.EncodeToString(id[:])
	}
	candidate := base
	for suffix := 2; taken(candidate); suffix++ {
		tail := fmt.Sprintf("-%d", suffix)
		candidate = sliceUTF16(base, max(minSlugLength, maxSlugLength-len(tail))) + tail
	}
	return candidate
}

// sliceUTF16 returns the first n UTF-16 units of s, the unit JavaScript
// counts string length in.
func sliceUTF16(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s
	}
	return string(utf16.Decode(units[:n]))
}
