package access

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/store"
)

// truthyColumn reads a flag column: PostgreSQL stores it as a boolean,
// SQLite as an integer where anything but 0 is on.
func truthyColumn(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case []byte:
		return true
	}
	return false
}

// PanelAccessPolicies returns the stored section rows of a subject, ordered
// by resource key.
func (s *Service) PanelAccessPolicies(ctx context.Context, subjectType, subjectID string) ([]SectionRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT resource_key, access_level
		FROM panel_access_policies
		WHERE subject_type = ? AND subject_id = ?
		ORDER BY resource_key ASC`, subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("load panel access policies of %s %s: %w", subjectType, subjectID, err)
	}
	defer rows.Close()
	out := []SectionRow{}
	for rows.Next() {
		var row SectionRow
		if err := rows.Scan(&row.ResourceKey, &row.AccessLevel); err != nil {
			return nil, fmt.Errorf("read panel access policy of %s %s: %w", subjectType, subjectID, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load panel access policies of %s %s: %w", subjectType, subjectID, err)
	}
	return out, nil
}

// ClientFeaturePolicies returns the stored client feature rows of a
// subject, ordered by feature key.
func (s *Service) ClientFeaturePolicies(ctx context.Context, subjectType, subjectID string) ([]FeatureRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT feature_key, enabled
		FROM client_feature_policies
		WHERE subject_type = ? AND subject_id = ?
		ORDER BY feature_key ASC`, subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("load client feature policies of %s %s: %w", subjectType, subjectID, err)
	}
	defer rows.Close()
	out := []FeatureRow{}
	for rows.Next() {
		var row FeatureRow
		var enabled any
		if err := rows.Scan(&row.FeatureKey, &enabled); err != nil {
			return nil, fmt.Errorf("read client feature policy of %s %s: %w", subjectType, subjectID, err)
		}
		row.Enabled = truthyColumn(enabled)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load client feature policies of %s %s: %w", subjectType, subjectID, err)
	}
	return out, nil
}

// setPanelAccessPolicies replaces the section rows of a subject.
func (s *Service) setPanelAccessPolicies(ctx context.Context, subjectType, subjectID string, rows []SectionRow) error {
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM panel_access_policies WHERE subject_type = ? AND subject_id = ?`,
			subjectType, subjectID); err != nil {
			return err
		}
		insert := `INSERT INTO panel_access_policies
				(subject_type, subject_id, resource_key, access_level, updated_at)
			VALUES (?, ?, ?, ?, ` + tx.Pick("datetime('now')", "NOW()") + `)`
		for _, row := range rows {
			if _, err := tx.Exec(ctx, insert, subjectType, subjectID, row.ResourceKey, row.AccessLevel); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save panel access policies of %s %s: %w", subjectType, subjectID, err)
	}
	return nil
}

// setClientFeaturePolicies replaces the client feature rows of a subject.
func (s *Service) setClientFeaturePolicies(ctx context.Context, subjectType, subjectID string, rows []FeatureRow) error {
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM client_feature_policies WHERE subject_type = ? AND subject_id = ?`,
			subjectType, subjectID); err != nil {
			return err
		}
		insert := `INSERT INTO client_feature_policies
				(subject_type, subject_id, feature_key, enabled, updated_at)
			VALUES (?, ?, ?, ?, ` + tx.Pick("datetime('now')", "NOW()") + `)`
		for _, row := range rows {
			if _, err := tx.Exec(ctx, insert, subjectType, subjectID, row.FeatureKey, flagValue(tx, row.Enabled)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save client feature policies of %s %s: %w", subjectType, subjectID, err)
	}
	return nil
}

// flagValue is a flag as each database stores it: 1 or 0 on SQLite, a
// boolean on PostgreSQL.
func flagValue(q store.Querier, on bool) any {
	if q.Dialect() == store.Postgres {
		return on
	}
	if on {
		return 1
	}
	return 0
}

// UserGroup is a user group as the policy and scope rules need it.
type UserGroup struct {
	ID   int64
	GUID string
	Name string
}

// UserGroupsForUser returns the user groups an account belongs to, ordered
// by name.
func (s *Service) UserGroupsForUser(ctx context.Context, userID int64) ([]UserGroup, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ug.id, ug.guid, ug.name FROM user_groups ug
		INNER JOIN user_group_members ugm ON ug.id = ugm.user_group_id
		WHERE ugm.user_id = ?
		ORDER BY ug.name ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("load user groups of user %d: %w", userID, err)
	}
	defer rows.Close()
	var out []UserGroup
	for rows.Next() {
		var g UserGroup
		if err := rows.Scan(&g.ID, &g.GUID, &g.Name); err != nil {
			return nil, fmt.Errorf("read user group of user %d: %w", userID, err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load user groups of user %d: %w", userID, err)
	}
	return out, nil
}

// UserGroupByGUID returns the user group with the guid, or nil when there
// is none.
func (s *Service) UserGroupByGUID(ctx context.Context, guid string) (*UserGroup, error) {
	var g UserGroup
	err := s.db.QueryRow(ctx, `SELECT id, guid, name FROM user_groups WHERE guid = ?`, guid).Scan(&g.ID, &g.GUID, &g.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load user group %q: %w", guid, err)
	}
	return &g, nil
}

// PolicyUserByID returns the id and role of an account for ResolveForUser,
// or nil when there is no such account. An account without a stored role
// has "".
func (s *Service) PolicyUserByID(ctx context.Context, id int64) (*PolicyUser, error) {
	var role sql.NullString
	user := PolicyUser{}
	err := s.db.QueryRow(ctx, `SELECT id, role FROM users WHERE id = ?`, id).Scan(&user.ID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load user %d: %w", id, err)
	}
	user.Role = role.String
	return &user, nil
}
