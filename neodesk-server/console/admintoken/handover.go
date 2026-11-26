package admintoken

import (
	"context"
	"encoding/json"
	"fmt"
)

// HandOver gives the token to another account when the account that made
// it is deleted: the token keeps its value, name, expiry and allow list,
// and its permissions become the ones given, which the caller has capped
// to what the new account holds. A revoked token is left alone. It
// reports whether the token was moved.
func (s *Service) HandOver(ctx context.Context, id string, toUser int64, permissions []string) (bool, error) {
	if permissions == nil {
		permissions = []string{}
	}
	perms, err := json.Marshal(permissions)
	if err != nil {
		return false, err
	}
	res, err := s.db.Exec(ctx, `UPDATE admin_tokens SET user_id = ?, permissions = ? WHERE public_id = ? AND revoked_at IS NULL`, toUser, string(perms), id)
	if err != nil {
		return false, fmt.Errorf("hand over token %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
