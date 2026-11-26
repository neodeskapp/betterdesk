package chatstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// A team is a standing conversation that people with chat.teams_manage
// set up: a name, people picked by hand (chat_members) and user groups
// (chat_team_groups) whose members belong to it for as long as they are
// in the user group. Members and IsMember read both, so adding someone to
// a user group in People adds them to its teams at once.

// TeamGroup is a user group a team includes.
type TeamGroup struct {
	GUID string
	Name string
}

// memberSelect are the accounts of a conversation (both arguments its id):
// the ones in chat_members and, for a team, the members of its user
// groups.
const memberSelect = `SELECT account_id FROM chat_members WHERE conversation_id = ?
	UNION SELECT ugm.user_id FROM chat_team_groups tg
	JOIN user_groups ug ON ug.guid = tg.group_guid
	JOIN user_group_members ugm ON ugm.user_group_id = ug.id
	WHERE tg.conversation_id = ?`

// memberOf is the condition that a conversation's id is one the account
// (both arguments its id) is a member of.
const memberOf = `(id IN (SELECT conversation_id FROM chat_members WHERE account_id = ?)
	OR id IN (SELECT tg.conversation_id FROM chat_team_groups tg
		JOIN user_groups ug ON ug.guid = tg.group_guid
		JOIN user_group_members ugm ON ugm.user_group_id = ug.id
		WHERE ugm.user_id = ?))`

// NewTeam makes a team with a title, people and user groups (guids). The
// one who makes it is no member unless named: a manager may set up a
// team for others.
func (s *Store) NewTeam(ctx context.Context, title string, creator int64, people []int64, groups []string) (Conversation, error) {
	title = cutRunes(strings.TrimSpace(title), 120)
	if title == "" {
		return Conversation{}, ErrEmpty
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return Conversation{}, err
	}
	id := "team:" + hex.EncodeToString(b)
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_conversations (id, kind, state, title, created_by, opened_ms) VALUES (?, 'team', 'open', ?, ?, ?)`,
			id, title, creator, s.nowMs()); err != nil {
			return err
		}
		return setTeamMembers(ctx, tx, id, people, groups)
	})
	if err != nil {
		return Conversation{}, fmt.Errorf("chat: make a team: %w", err)
	}
	return s.Conversation(ctx, id)
}

// SetTeam changes a team's title (kept when empty), people and user
// groups. A nil list leaves that list as it is.
func (s *Store) SetTeam(ctx context.Context, id, title string, people []int64, groups []string) (Conversation, error) {
	c, err := s.Conversation(ctx, id)
	if err != nil {
		return c, err
	}
	if c.Kind != ConvTeam {
		return c, ErrNotFound
	}
	if t := cutRunes(strings.TrimSpace(title), 120); t != "" {
		c.Title = t
	}
	if err := s.save(ctx, c); err != nil {
		return c, err
	}
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if people != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM chat_members WHERE conversation_id = ?`, id); err != nil {
				return err
			}
		}
		if groups != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM chat_team_groups WHERE conversation_id = ?`, id); err != nil {
				return err
			}
		}
		return setTeamMembers(ctx, tx, id, people, groups)
	})
	if err != nil {
		return c, fmt.Errorf("chat: change team %s: %w", id, err)
	}
	return c, nil
}

func setTeamMembers(ctx context.Context, tx *store.Tx, id string, people []int64, groups []string) error {
	member := tx.Pick(`INSERT OR IGNORE INTO chat_members (conversation_id, account_id) VALUES (?, ?)`,
		`INSERT INTO chat_members (conversation_id, account_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	for _, p := range people {
		if p <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, member, id, p); err != nil {
			return err
		}
	}
	group := tx.Pick(`INSERT OR IGNORE INTO chat_team_groups (conversation_id, group_guid) VALUES (?, ?)`,
		`INSERT INTO chat_team_groups (conversation_id, group_guid) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	for _, g := range groups {
		if g = strings.TrimSpace(g); g == "" {
			continue
		}
		if _, err := tx.Exec(ctx, group, id, g); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTeam deletes a team with its messages, read marks, reactions
// and trail. A team that is gone already is ErrNotFound.
func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	if KindOf(id) != ConvTeam {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.InTx(ctx, func(tx *store.Tx) error {
		res, err := tx.Exec(ctx, `DELETE FROM chat_conversations WHERE id = ? AND kind = 'team'`, id)
		if err != nil {
			return fmt.Errorf("chat: delete team %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		for _, q := range []string{
			`DELETE FROM chat_reactions WHERE message_id IN (SELECT id FROM chat_messages WHERE conversation_id = ?)`,
			`DELETE FROM chat_message_edits WHERE message_id IN (SELECT id FROM chat_messages WHERE conversation_id = ?)`,
			`DELETE FROM chat_messages WHERE conversation_id = ?`,
			`DELETE FROM chat_reads WHERE conversation_id = ?`,
			`DELETE FROM chat_members WHERE conversation_id = ?`,
			`DELETE FROM chat_team_groups WHERE conversation_id = ?`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return fmt.Errorf("chat: delete team %s: %w", id, err)
			}
		}
		return nil
	})
}

// TeamPeople are the accounts picked for a team by hand, without the
// members of its user groups.
func (s *Store) TeamPeople(ctx context.Context, id string) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT account_id FROM chat_members WHERE conversation_id = ? ORDER BY account_id`, id)
	if err != nil {
		return nil, fmt.Errorf("chat: people of %s: %w", id, err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// TeamGroups are the user groups of a team that still exist, by name.
func (s *Store) TeamGroups(ctx context.Context, id string) ([]TeamGroup, error) {
	rows, err := s.db.Query(ctx, `SELECT ug.guid, ug.name FROM chat_team_groups tg
		JOIN user_groups ug ON ug.guid = tg.group_guid
		WHERE tg.conversation_id = ? ORDER BY ug.name, ug.guid`, id)
	if err != nil {
		return nil, fmt.Errorf("chat: user groups of %s: %w", id, err)
	}
	defer rows.Close()
	out := []TeamGroup{}
	for rows.Next() {
		var g TeamGroup
		if err := rows.Scan(&g.GUID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UserGroups are every user group, by name, with the accounts in each:
// what a team can include.
func (s *Store) UserGroups(ctx context.Context) ([]TeamGroup, map[string][]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT ug.guid, ug.name, ugm.user_id FROM user_groups ug
		LEFT JOIN user_group_members ugm ON ugm.user_group_id = ug.id
		ORDER BY ug.name, ug.guid, ugm.user_id`)
	if err != nil {
		return nil, nil, fmt.Errorf("chat: user groups: %w", err)
	}
	defer rows.Close()
	var groups []TeamGroup
	members := map[string][]int64{}
	for rows.Next() {
		var g TeamGroup
		var user *int64
		if err := rows.Scan(&g.GUID, &g.Name, &user); err != nil {
			return nil, nil, err
		}
		if len(groups) == 0 || groups[len(groups)-1].GUID != g.GUID {
			groups = append(groups, g)
			members[g.GUID] = []int64{}
		}
		if user != nil && !slices.Contains(members[g.GUID], *user) {
			members[g.GUID] = append(members[g.GUID], *user)
		}
	}
	return groups, members, rows.Err()
}
