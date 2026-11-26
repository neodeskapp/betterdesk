package users

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Deleting an account, and what it hands over.
//
// What goes with the account (wipe, then the rows that point at it):
// its console sessions (ended), its client sign-ins and their access
// tokens, its API tokens (revoked, with what they made, unless handed
// over), its organization memberships (unless handed over), its user
// group memberships, its places on device group lists, the devices given
// to it one by one, its folder access and panel access, its picture, its
// password links, its end date and address change, and the failed
// sign-ins and lockout of its name (a new account with the name would get
// them otherwise). Saved reports and open tickets assigned to it go to
// the account it hands over to; without one, open tickets are unassigned
// and saved reports stay under its name.
//
// What stays: the audit log (its entries keep their text and the id of
// the account), the tickets it opened and the comments it wrote, chat
// messages, and everything else that names it as its author.
//
// A hand-over (DELETE /api/users/{id}?handover_to=<id>) gives its API
// tokens, organization memberships, saved reports and open tickets to
// another account. That account must be one the caller may change,
// enabled, and able to use the console (no pro or device account). An
// API token keeps only the permissions the new account holds; a token
// left with none is revoked. Organization memberships move only when the
// caller has org.manage_users; where the new account is a member of the
// organization already, its own role stays.

// deleteCounts are what a delete would do to the account's things.
type deleteCounts struct {
	// Goes
	Sessions      int64 `json:"sessions"`
	ClientSignIns int64 `json:"client_sign_ins"`
	APITokens     int64 `json:"api_tokens"`
	Organizations int64 `json:"organizations"`
	UserGroups    int64 `json:"user_groups"`
	DeviceGrants  int64 `json:"device_grants"`
	// Moves or stays
	SavedReports int64 `json:"saved_reports"`
	OpenTickets  int64 `json:"open_tickets"`
	// Stays
	AuditEntries   int64 `json:"audit_entries"`
	TicketsOpened  int64 `json:"tickets_opened"`
	TicketComments int64 `json:"ticket_comments"`
	ChatMessages   int64 `json:"chat_messages"`
}

// handoverCandidate is an account the things can go to.
type handoverCandidate struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type deletePreview struct {
	ID       int64        `json:"id"`
	Username string       `json:"username"`
	Counts   deleteCounts `json:"counts"`
	// Candidates are the accounts the caller may hand over to.
	Candidates []handoverCandidate `json:"candidates"`
	// Organizations says whether a hand-over moves the organization
	// memberships (the caller has org.manage_users).
	Organizations bool `json:"organizations_move"`
}

// countOf runs a COUNT query; a table the database does not have counts
// nothing.
func countOf(ctx context.Context, db *store.DB, query string, args ...any) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, query, args...).Scan(&n)
	if err != nil && missingTable.MatchString(err.Error()) {
		return 0, nil
	}
	return n, err
}

// openTicket is the condition of a ticket that is not resolved or closed.
const openTicket = `COALESCE(status, 'open') NOT IN ('resolved', 'closed')`

func (h *handlers) deleteCounts(ctx context.Context, user *account) (deleteCounts, error) {
	db := h.d.Store
	id, name := user.ID.V, user.username()
	var c deleteCounts
	queries := []struct {
		into  *int64
		query string
		args  []any
	}{
		{&c.Sessions, `SELECT COUNT(*) FROM panel_sessions WHERE user_id = ? AND is_active = ` + db.Pick("1", "TRUE"), []any{id}},
		{&c.ClientSignIns, `SELECT COUNT(*) FROM access_tokens WHERE user_id = ? AND ` + db.Pick("COALESCE(revoked, 0) = 0", "COALESCE(revoked, FALSE) = FALSE"), []any{id}},
		{&c.APITokens, `SELECT COUNT(*) FROM admin_tokens WHERE user_id = ? AND revoked_at IS NULL`, []any{id}},
		{&c.Organizations, `SELECT COUNT(*) FROM org_users WHERE server_user_id = ?`, []any{id}},
		{&c.UserGroups, `SELECT COUNT(*) FROM user_group_members WHERE user_id = ?`, []any{id}},
		{&c.DeviceGrants, `SELECT COUNT(*) FROM user_peer_grants WHERE user_id = ?`, []any{id}},
		{&c.SavedReports, `SELECT COUNT(*) FROM saved_reports WHERE created_by = ?`, []any{name}},
		{&c.OpenTickets, `SELECT COUNT(*) FROM tickets WHERE assigned_to = ? AND ` + openTicket, []any{name}},
		{&c.AuditEntries, `SELECT COUNT(*) FROM audit_log WHERE user_id = ? OR target_user_id = ?`, []any{id, id}},
		{&c.TicketsOpened, `SELECT COUNT(*) FROM tickets WHERE created_by = ?`, []any{name}},
		{&c.TicketComments, `SELECT COUNT(*) FROM ticket_comments WHERE author = ?`, []any{name}},
		{&c.ChatMessages, `SELECT COUNT(*) FROM chat_messages WHERE from_user_id = ?`, []any{id}},
	}
	for _, q := range queries {
		n, err := countOf(ctx, db, q.query, q.args...)
		if err != nil {
			return c, fmt.Errorf("count for the delete of %d: %w", id, err)
		}
		*q.into = n
	}
	return c, nil
}

// mayReceive reports whether the account can take over what the deleted
// one hands over.
func (h *handlers) mayReceive(r *http.Request, from, to *account) (bool, error) {
	role := to.role()
	if to.ID.V == from.ID.V || to.Disabled.True() || role == "" || auth.IsProRole(role) || auth.IsDeviceRole(role) {
		return false, nil
	}
	return h.mayChange(r, to)
}

// deletePreview answers GET /api/users/{id}/delete-preview: what a delete
// of the account removes and what stays, as counts, and the accounts it
// may hand over to.
func (h *handlers) deletePreview(w http.ResponseWriter, r *http.Request) {
	user := h.deletable(w, r)
	if user == nil {
		return
	}
	ctx := r.Context()
	counts, err := h.deleteCounts(ctx, user)
	if err != nil {
		h.serverError(w, r, "Delete preview error:", err)
		return
	}
	accounts, err := allAccounts(ctx, h.d.Store)
	if err != nil {
		h.serverError(w, r, "Delete preview error:", err)
		return
	}
	candidates := []handoverCandidate{}
	for _, a := range accounts {
		ok, err := h.mayReceive(r, user, a)
		if err != nil {
			h.serverError(w, r, "Delete preview error:", err)
			return
		}
		if ok {
			candidates = append(candidates, handoverCandidate{ID: a.ID.V, Username: a.username(), Role: a.role()})
		}
	}
	answer(w, r, http.StatusOK, struct {
		Success bool          `json:"success"`
		Data    deletePreview `json:"data"`
	}{true, deletePreview{
		ID: user.ID.V, Username: user.username(), Counts: counts, Candidates: candidates,
		Organizations: kit.Access(r).HasPermission(auth.PermOrgManageUsers),
	}})
}

// deletable loads the account of the path for a delete and answers the
// refusals: not found, the caller's own, one the caller may not change,
// the last Super admin that can sign in. It is nil when it answered.
func (h *handlers) deletable(w http.ResponseWriter, r *http.Request) *account {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return nil
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return nil
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return nil
	}
	if isSessionAccount(r, user.ID.V) {
		failKey(w, r, http.StatusBadRequest, "users.cannot_delete_self")
		return nil
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return nil
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return nil
	}
	if last, err := isLastSuperAdmin(ctx, h.d.Store, user.ID.V, user.role(), h.now()); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return nil
	} else if last {
		failKey(w, r, http.StatusBadRequest, "users.last_admin")
		return nil
	}
	return user
}

// handoverTarget reads ?handover_to= of a delete: nil without one.
func (h *handlers) handoverTarget(w http.ResponseWriter, r *http.Request, from *account) (*account, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("handover_to"))
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		failKey(w, r, http.StatusBadRequest, "users.handover_invalid")
		return nil, false
	}
	to, err := accountByID(r.Context(), h.d.Store, float64(id))
	if err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return nil, false
	}
	if to == nil {
		failKey(w, r, http.StatusBadRequest, "users.handover_invalid")
		return nil, false
	}
	ok, err := h.mayReceive(r, from, to)
	if err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return nil, false
	}
	if !ok {
		failKey(w, r, http.StatusForbidden, "users.handover_refused")
		return nil, false
	}
	return to, true
}

// handedOver is what a hand-over moved, for the audit entry.
type handedOver struct {
	tokens, tokensRevoked, organizations, reports, tickets int
}

func (m handedOver) String() string {
	return fmt.Sprintf("%d API tokens (%d revoked for want of permissions), %d organization memberships, %d saved reports, %d open tickets",
		m.tokens, m.tokensRevoked, m.organizations, m.reports, m.tickets)
}

// handOver gives the account's things to another account before the
// delete.
func (h *handlers) handOver(r *http.Request, from, to *account) (handedOver, error) {
	ctx := r.Context()
	db := h.d.Store
	var moved handedOver
	// API tokens, capped to what the new account holds
	if h.d.AdminTokens != nil {
		tokens, err := h.d.AdminTokens.List(ctx, from.ID.V)
		if err != nil {
			return moved, err
		}
		for _, tok := range tokens {
			if !tok.RevokedAt.IsZero() {
				continue
			}
			var kept []string
			for _, p := range tok.Permissions {
				has, err := h.d.Access.RoleHasEffectivePermission(ctx, to.role(), p)
				if err != nil {
					return moved, err
				}
				if has {
					kept = append(kept, p)
				}
			}
			if len(kept) == 0 {
				if _, err := h.d.AdminTokens.Revoke(ctx, tok.ID, callerName(r)); err != nil {
					return moved, err
				}
				moved.tokensRevoked++
				continue
			}
			ok, err := h.d.AdminTokens.HandOver(ctx, tok.ID, to.ID.V, kept)
			if err != nil {
				return moved, err
			}
			if ok {
				moved.tokens++
			}
		}
	}
	// Organization memberships, for a caller who manages them
	if kit.Access(r).HasPermission(auth.PermOrgManageUsers) {
		res, err := db.Exec(ctx, `UPDATE org_users SET server_user_id = ?, username = ? WHERE server_user_id = ?
			AND org_id NOT IN (SELECT org_id FROM org_users WHERE server_user_id = ? OR username = ?)`,
			to.ID.V, to.username(), from.ID.V, to.ID.V, to.username())
		if err != nil && !missingTable.MatchString(err.Error()) {
			return moved, err
		}
		if res != nil {
			n, _ := res.RowsAffected()
			moved.organizations = int(n)
		}
	}
	// Saved reports and open tickets go by name
	for _, step := range []struct {
		into  *int
		query string
	}{
		{&moved.reports, `UPDATE saved_reports SET created_by = ? WHERE created_by = ?`},
		{&moved.tickets, `UPDATE tickets SET assigned_to = ? WHERE assigned_to = ? AND ` + openTicket},
	} {
		res, err := db.Exec(ctx, step.query, to.username(), from.username())
		if err != nil && !missingTable.MatchString(err.Error()) {
			return moved, err
		}
		if res != nil {
			n, _ := res.RowsAffected()
			*step.into = int(n)
		}
	}
	return moved, nil
}

// leftovers removes what still points at a deleted account and is not
// handed over: its API tokens (revoked, which ends what they made), its
// organization memberships, the failed sign-ins and lockout of its name,
// an address book keyed by its name, and open tickets assigned to it.
func (h *handlers) leftovers(r *http.Request, user *account) error {
	ctx := r.Context()
	if h.d.AdminTokens != nil {
		tokens, err := h.d.AdminTokens.List(ctx, user.ID.V)
		if err != nil {
			return err
		}
		for _, tok := range tokens {
			if tok.RevokedAt.IsZero() {
				if _, err := h.d.AdminTokens.Revoke(ctx, tok.ID, callerName(r)); err != nil {
					return err
				}
			}
		}
	}
	name := user.username()
	steps := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM org_users WHERE server_user_id = ? AND server_user_id > 0`, []any{user.ID.V}},
		{`DELETE FROM login_attempts WHERE username = ?`, []any{name}},
		{`DELETE FROM account_lockouts WHERE username = ?`, []any{name}},
		{`UPDATE tickets SET assigned_to = NULL WHERE assigned_to = ? AND ` + openTicket, []any{name}},
	}
	if h.d.Store.Shape().Table("address_books").Has("username") {
		steps = append(steps, struct {
			query string
			args  []any
		}{`DELETE FROM address_books WHERE username = ?`, []any{name}})
	}
	for _, s := range steps {
		if name == "" && len(s.args) == 1 && s.args[0] == name {
			continue
		}
		if _, err := h.d.Store.Exec(ctx, s.query, s.args...); err != nil && !missingTable.MatchString(err.Error()) {
			return err
		}
	}
	return nil
}

// callerName is the signed-in account's username.
func callerName(r *http.Request) string {
	if u := kit.User(r); u != nil {
		return u.Username
	}
	return ""
}
