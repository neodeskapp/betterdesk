package phase45

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// profileFields are the profile columns of users, in the order the
// profile is written.
var profileFields = []string{"first_name", "last_name", "email", "phone", "role_display", "avatar_url"}

// userRow is what the profile routes read of an account. The cells hold
// what the database holds, which on SQLite need not be text.
type userRow struct {
	ID          store.Int64String
	Username    jsbind.Cell
	Role        jsbind.Cell
	FirstName   jsbind.Cell
	LastName    jsbind.Cell
	Email       jsbind.Cell
	Phone       jsbind.Cell
	RoleDisplay jsbind.Cell
	AvatarURL   jsbind.Cell
}

// getUserByID reads an account, nil when there is none.
func getUserByID(ctx context.Context, db *store.DB, id int64) (*userRow, error) {
	var u userRow
	err := store.SelectRow(ctx, db,
		"SELECT id, username, role, first_name, last_name, email, phone, role_display, avatar_url FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.Role, &u.FirstName, &u.LastName, &u.Email, &u.Phone, &u.RoleDisplay, &u.AvatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user %d: %w", id, err)
	}
	return &u, nil
}

// updateUserProfile writes profile columns of an account: values by
// column, each a name of profileFields. Without values it writes nothing.
func updateUserProfile(ctx context.Context, db *store.DB, id int64, values map[string]string) error {
	var set []string
	var args []any
	for _, column := range profileFields {
		if value, ok := values[column]; ok {
			set = append(set, column+" = ?")
			args = append(args, value)
		}
	}
	if len(set) == 0 {
		return nil
	}
	_, err := db.Exec(ctx, "UPDATE users SET "+strings.Join(set, ", ")+" WHERE id = ?", append(args, id)...)
	if err != nil {
		return fmt.Errorf("update profile of user %d: %w", id, err)
	}
	return nil
}

// templateRow is a row of agent_templates as the template list shows it.
type templateRow struct {
	ID              jsbind.Cell `json:"id"`
	Name            jsbind.Cell `json:"name"`
	Description     jsbind.Cell `json:"description"`
	EnrollmentToken jsbind.Cell `json:"enrollment_token"`
	CreatedAt       store.Time  `json:"created_at"`
	UpdatedAt       store.Time  `json:"updated_at"`
}

// listTemplates is listAgentTemplates: every template, the highest id
// first.
func listTemplates(ctx context.Context, db *store.DB) ([]templateRow, error) {
	rows, err := store.Select(ctx, db, `SELECT id, name, description, enrollment_token, created_at, updated_at
                FROM agent_templates ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list agent templates: %w", err)
	}
	defer rows.Close()
	list := []templateRow{}
	for rows.Next() {
		var t templateRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.EnrollmentToken, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read agent template: %w", err)
		}
		list = append(list, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list agent templates: %w", err)
	}
	return list, nil
}

// insertTemplate is createAgentTemplate. The error keeps the database's
// message, which the route answers with.
func insertTemplate(ctx context.Context, db *store.DB, name, description, configJSON, token string, createdBy *int64) error {
	var by any
	if createdBy != nil {
		by = *createdBy
	}
	_, err := db.Exec(ctx, `INSERT INTO agent_templates (name, description, config_json, enrollment_token, created_by)
                VALUES (?, ?, ?, ?, ?)`, name, description, configJSON, token, by)
	return err
}

// enrollmentTemplate is the part of a template that enrollment reads.
type enrollmentTemplate struct {
	Name       jsbind.Cell
	ConfigJSON jsbind.Cell
}

// templateByToken is getAgentTemplateByToken: the template of a token,
// nil for an empty token, one no template has, and a table or column that
// is not there.
func templateByToken(ctx context.Context, db *store.DB, token string) (*enrollmentTemplate, error) {
	if token == "" {
		return nil, nil
	}
	var t enrollmentTemplate
	err := store.SelectRow(ctx, db, "SELECT name, config_json FROM agent_templates WHERE enrollment_token = ?", token).Scan(&t.Name, &t.ConfigJSON)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		message := jsbind.Message(err)
		if strings.Contains(message, "no such table") || strings.Contains(message, "no such column") || strings.Contains(message, "does not exist") {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}
