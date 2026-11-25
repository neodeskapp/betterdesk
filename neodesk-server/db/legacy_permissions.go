package db

import (
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// legacyPermissionStatements rewrite role overrides that name an older
// permission (chat.access, before A242) under the permissions it stands
// for now: each gets the old override's grant unless the role has one of
// its own, then the old row goes. A second run finds nothing to do.
func legacyPermissionStatements() []string {
	var out []string
	for old, now := range auth.LegacyPermissions {
		for _, p := range now {
			out = append(out, fmt.Sprintf(`INSERT INTO role_permissions (role, permission, granted)
				SELECT o.role, '%s', o.granted FROM role_permissions o
				WHERE o.permission = '%s' AND NOT EXISTS (
					SELECT 1 FROM role_permissions n WHERE n.role = o.role AND n.permission = '%s')`, p, old, p))
		}
		out = append(out, fmt.Sprintf(`DELETE FROM role_permissions WHERE permission = '%s'`, old))
	}
	return out
}

// renameLegacyPermissions runs legacyPermissionStatements on SQLite.
func (s *SQLiteDB) renameLegacyPermissions() error {
	for _, q := range legacyPermissionStatements() {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("rename legacy role permissions: %w", err)
		}
	}
	return nil
}

// renameLegacyPermissions runs legacyPermissionStatements on PostgreSQL.
func (pg *PostgresDB) renameLegacyPermissions() error {
	for _, q := range legacyPermissionStatements() {
		if _, err := pg.pool.Exec(pg.ctx, q); err != nil {
			return fmt.Errorf("rename legacy role permissions: %w", err)
		}
	}
	return nil
}
