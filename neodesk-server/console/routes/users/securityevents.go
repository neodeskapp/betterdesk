package users

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// An account's sign-in history ("Recent sign-ins and security events" on
// Your account, and the user's page): the audit entries about the
// account's sign-in and its security, newest first.
//
// securityEvents is the one list of the actions it shows. own says the
// account itself writes the entry (user_id is the account, and older
// entries have no target_user_id); the others are found by
// target_user_id alone, so an admin's own changes to someone else never
// show in the admin's history. The web app labels an action with
// audit.action_<action> and for impersonation_started with its own text.
var securityEvents = []struct {
	action string
	own    bool
}{
	{"login", true},
	{"logout", true},
	{"totp_failed", true},
	{"password_changed", true},
	{"totp_enabled", true},
	{"totp_disabled", true},
	{"totp_recovery_codes", true},
	{"session_signed_out", true},
	{"session_replaced", true},
	{"session_limit", true},
	{"password_reset", false},
	{"password_reset_link_sent", false},
	{"totp_disabled_by_admin", false},
	{"session_ended_by_admin", false},
	{"user_disabled", false},
	{"user_enabled", false},
	{"impersonation_started", false},
}

// securityEvent is one entry of the history.
type securityEvent struct {
	ID     store.Int64String `json:"id"`
	Action string            `json:"action"`
	At     store.Time        `json:"at"`
	// IP is the address of an entry the account itself wrote, "" for the
	// others.
	IP string `json:"ip"`
	// Actor is who did it when that was someone else, "" when it was the
	// account; Reason the reason an admin gave to view NeoDesk as it.
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type securityEventsAnswer struct {
	Success bool `json:"success"`
	Data    struct {
		Events []securityEvent `json:"events"`
	} `json:"data"`
}

// ownSecurityEvents answers GET /api/users/me/security-events.
func (h *handlers) ownSecurityEvents(w http.ResponseWriter, r *http.Request) {
	h.answerSecurityEvents(w, r, kit.UserID(r))
}

// userSecurityEvents answers GET /api/users/{id}/security-events, for the
// user's page.
func (h *handlers) userSecurityEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	user, err := accountByID(r.Context(), h.d.Store, id)
	if err != nil {
		h.serverError(w, r, "Security events error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	h.answerSecurityEvents(w, r, user.ID.V)
}

func (h *handlers) answerSecurityEvents(w http.ResponseWriter, r *http.Request, id int64) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	events, err := h.securityEventsOf(r.Context(), id, limit)
	if err != nil {
		h.serverError(w, r, "Security events error:", err)
		return
	}
	var out securityEventsAnswer
	out.Success = true
	out.Data.Events = events
	answer(w, r, http.StatusOK, out)
}

// securityEventsOf reads the account's history, newest first.
func (h *handlers) securityEventsOf(ctx context.Context, id int64, limit int) ([]securityEvent, error) {
	var all, own []string
	args := []any{}
	for _, e := range securityEvents {
		all = append(all, "?")
		args = append(args, e.action)
	}
	args = append(args, id, id)
	for _, e := range securityEvents {
		if e.own {
			own = append(own, "?")
			args = append(args, e.action)
		}
	}
	args = append(args, limit)
	rows, err := store.Select(ctx, h.d.Store,
		`SELECT a.id, a.action, a.created_at, COALESCE(a.ip_address, ''), COALESCE(a.details, ''),
		        COALESCE(a.user_id, 0), COALESCE(u.username, ''), `+people.FullNameSQL("u")+`
		 FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		 WHERE a.action IN (`+strings.Join(all, ", ")+`)
		   AND (a.target_user_id = ? OR (a.target_user_id IS NULL AND a.user_id = ? AND a.action IN (`+strings.Join(own, ", ")+`)))
		 ORDER BY a.id DESC
		 LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []securityEvent{}
	for rows.Next() {
		var e securityEvent
		var details, actor, actorName string
		var actorID sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Action, &e.At, &e.IP, &details, &actorID, &actor, &actorName); err != nil {
			return nil, err
		}
		if actorID.Int64 != id {
			e.Actor, e.IP = people.Name(actorName, "", actor), ""
		}
		if e.Action == "impersonation_started" {
			if _, reason, found := strings.Cut(details, " for: "); found {
				e.Reason = reason
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
