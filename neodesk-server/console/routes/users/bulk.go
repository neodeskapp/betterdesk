package users

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// Acting on several accounts at once. Each action has its own route under
// the permission of the route that does it to one account, and takes the
// body {ids, dry_run?, ...}: up to maxBulkAccounts account ids, as numbers
// or as text of digits. Every account goes through the checks of the
// route for one account, and the answer says for each id, in the order
// given, what happened:
//
//	changed     the action was done (with dry_run: it would be)
//	unchanged   the account was so already, or was named twice
//	refused     a check refused it; error says why
//	failed      the server could not do it; error says so, the rest go on
//
// With dry_run nothing is written and no mail goes out: the answer is the
// preview the users page shows before it runs the action. The caller's
// own account is refused by every action: it is changed on its own.
// Every account that changed gets its own audit entry about it.

// maxBulkAccounts is the most accounts one request names.
const maxBulkAccounts = 500

// bulkResult is what happened to one account of a request for several.
type bulkResult struct {
	ID       any    `json:"id"`
	Username string `json:"username,omitempty"`
	Outcome  string `json:"outcome"`
	Error    string `json:"error,omitempty"`
}

// The outcomes of one account.
const (
	outcomeChanged   = "changed"
	outcomeUnchanged = "unchanged"
	outcomeRefused   = "refused"
	outcomeFailed    = "failed"
)

// errUnchanged is what an action's apply returns for an account the
// action found was so already.
var errUnchanged = errors.New("unchanged")

// bulkIDs reads the body's "ids": a list of account ids, 1 to
// maxBulkAccounts of them.
func bulkIDs(r *http.Request) ([]jsbind.Value, error) {
	given := member(kit.Body(r), "ids")
	list := given.Items()
	if !given.IsArray() || len(list) == 0 {
		return nil, sayKey(r, http.StatusBadRequest, "users.bulk_ids_required")
	}
	if len(list) > maxBulkAccounts {
		return nil, say(http.StatusBadRequest, kit.T(r, "users.bulk_too_many", kit.Vars("max", maxBulkAccounts)))
	}
	return list, nil
}

// bulkID is an id of the list as a number, false for one that is none.
func bulkID(v jsbind.Value) (float64, bool) {
	switch {
	case v.IsNumber():
		n := v.ToNumber()
		return n, n > 0 && n == float64(int64(n))
	case v.IsString():
		n, ok := jsreq.ParseInt(v.Str(), 10)
		return n, ok && n > 0 && jsreq.NumberString(n) == strings.TrimSpace(v.Str())
	}
	return 0, false
}

// bulkFlag reads a true or false member of the body, fallback when it is
// not there. Anything else is answered 400 and ok is false.
func bulkFlag(w http.ResponseWriter, r *http.Request, name string, fallback bool) (value, ok bool) {
	v := member(kit.Body(r), name)
	if v.IsUndefined() {
		return fallback, true
	}
	switch v.JSON() {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	fail(w, r, http.StatusBadRequest, "Invalid value for "+name)
	return false, false
}

// bulkAction is one action for several accounts.
type bulkAction struct {
	// what names the action in the server log.
	what string
	// prepare checks the request once before the accounts: a refusal of
	// it is the answer. Optional.
	prepare func(r *http.Request) error
	// ownChecked says check refuses the caller's own account with a text
	// of its own; else the run refuses it first.
	ownChecked bool
	// check says whether the action would change the account, or returns
	// the refusal (a reply) or a server error. It writes nothing.
	check func(r *http.Request, user *account, run *bulkRun) (bool, error)
	// apply does it to an account check let through; errUnchanged says it
	// was so already.
	apply func(r *http.Request, user *account, run *bulkRun) error
}

// bulkRun is what one request for several accounts keeps between them.
type bulkRun struct {
	h   *handlers
	dry bool
	// supers are the Super admins that could sign in when a dry run asked
	// first; leaving are the ones the run takes that from.
	supers  map[int64]bool
	leaving map[int64]bool
}

// lastSuperAdmin reports whether the account is the last Super admin that
// can sign in, counting the ones the run has already taken away (a dry run
// changed nothing, so it counts them itself).
func (run *bulkRun) lastSuperAdmin(r *http.Request, user *account) (bool, error) {
	if !access.IsSuperAdminRole(user.role()) {
		return false, nil
	}
	h := run.h
	if !run.dry {
		return isLastSuperAdmin(r.Context(), h.d.Store, user.ID.V, user.role(), h.now())
	}
	if run.supers == nil {
		var err error
		if run.supers, err = activeSuperAdminIDs(r.Context(), h.d.Store, h.now()); err != nil {
			return false, err
		}
	}
	for id := range run.supers {
		if id != user.ID.V && !run.leaving[id] {
			return false, nil
		}
	}
	return true, nil
}

// leaves notes that the account stops counting as a Super admin that can
// sign in.
func (run *bulkRun) leaves(id int64) {
	if run.leaving == nil {
		run.leaving = map[int64]bool{}
	}
	run.leaving[id] = true
}

// bulkAnswer is the data of the answer.
type bulkAnswer struct {
	Results []bulkResult `json:"results"`
	DryRun  bool         `json:"dry_run,omitempty"`
}

// runBulk runs the action over the body's ids and answers what happened
// to each.
func (h *handlers) runBulk(w http.ResponseWriter, r *http.Request, a bulkAction) {
	ids, err := bulkIDs(r)
	if err != nil {
		sent(w, r, err)
		return
	}
	dry, ok := bulkFlag(w, r, "dry_run", false)
	if !ok {
		return
	}
	if a.prepare != nil {
		if err := a.prepare(r); err != nil {
			if !sent(w, r, err) {
				h.serverError(w, r, a.what+" error:", err)
			}
			return
		}
	}
	ctx := r.Context()
	run := &bulkRun{h: h, dry: dry}
	seen := map[int64]bool{}
	results := make([]bulkResult, 0, len(ids))
	for _, raw := range ids {
		res := bulkResult{ID: json.RawMessage(raw.JSON()), Outcome: outcomeRefused}
		id, ok := bulkID(raw)
		if !ok {
			res.Error = "Invalid user ID"
			results = append(results, res)
			continue
		}
		user, err := accountByID(ctx, h.d.Store, id)
		switch {
		case err != nil:
			res.Outcome, res.Error = h.failedOne(r, a.what, err)
		case user == nil:
			res.Error = kit.T(r, "users.not_found", nil)
		case seen[user.ID.V]:
			res.ID, res.Username, res.Outcome = user.ID, user.username(), outcomeUnchanged
		default:
			seen[user.ID.V] = true
			res.ID, res.Username = user.ID, user.username()
			res.Outcome, res.Error = h.bulkOne(r, a, run, user)
		}
		results = append(results, res)
	}
	answer(w, r, http.StatusOK, struct {
		Success bool       `json:"success"`
		Data    bulkAnswer `json:"data"`
	}{true, bulkAnswer{Results: results, DryRun: dry}})
}

// bulkOne runs the action on one account and says what happened.
func (h *handlers) bulkOne(r *http.Request, a bulkAction, run *bulkRun, user *account) (outcome, reason string) {
	if !a.ownChecked && isSessionAccount(r, user.ID.V) {
		return outcomeRefused, kit.T(r, "users.bulk_skip_own", nil)
	}
	change, err := a.check(r, user, run)
	if err != nil {
		return h.outcomeOf(r, a.what, err)
	}
	if !change {
		return outcomeUnchanged, ""
	}
	if run.dry {
		return outcomeChanged, ""
	}
	if err := a.apply(r, user, run); err != nil {
		if errors.Is(err, errUnchanged) {
			return outcomeUnchanged, ""
		}
		return h.outcomeOf(r, a.what, err)
	}
	return outcomeChanged, ""
}

// outcomeOf is a refusal with its text, or a failure the server logs.
func (h *handlers) outcomeOf(r *http.Request, what string, err error) (outcome, reason string) {
	if text, ok := refusedText(r, err); ok {
		return outcomeRefused, text
	}
	return h.failedOne(r, what, err)
}

func (h *handlers) failedOne(r *http.Request, what string, err error) (outcome, reason string) {
	h.log.Error(what+" error:", err)
	return outcomeFailed, kit.T(r, "errors.server_error", nil)
}

// mayChangeRefusal is mayChange as the refusal of a route.
func (h *handlers) mayChangeRefusal(r *http.Request, user *account) error {
	ok, err := h.mayChange(r, user)
	if err != nil || !ok {
		return refusedOr(err)
	}
	return nil
}

// ---- The actions ----

// bulkRole answers POST /api/users/bulk/role with {ids, role, dry_run?}:
// the accounts get the role. The checks are the ones of a role change in
// PATCH /api/users/{id}; a directory account keeps the role its directory
// gives it. Their sessions end and their access tokens are revoked.
func (h *handlers) bulkRole(w http.ResponseWriter, r *http.Request) {
	role := member(kit.Body(r), "role")
	if !validRole(role) {
		failKey(w, r, http.StatusBadRequest, "users.invalid_role")
		return
	}
	to := role.Str()
	h.runBulk(w, r, bulkAction{
		what: "Change role",
		prepare: func(r *http.Request) error {
			ok, err := h.canAssignRole(r, to)
			if err != nil || !ok {
				return refusedOr(err)
			}
			return nil
		},
		check: func(r *http.Request, user *account, run *bulkRun) (bool, error) {
			if err := h.mayChangeRefusal(r, user); err != nil {
				return false, err
			}
			if provider, _ := providerOf(user); provider == "ldap" {
				return false, sayKey(r, http.StatusBadRequest, "users.bulk_role_directory")
			}
			if user.role() == to {
				return false, nil
			}
			if err := h.checkRole(r, user, role, func() (bool, error) { return run.lastSuperAdmin(r, user) }); err != nil {
				return false, err
			}
			if !access.IsSuperAdminRole(to) {
				run.leaves(user.ID.V)
			}
			return true, nil
		},
		apply: func(r *http.Request, user *account, _ *bulkRun) error {
			from := user.role()
			if err := updateRole(r.Context(), h.d.Store, user.ID.V, to); err != nil {
				return err
			}
			if err := h.endSessions(r, user.ID.V, false); err != nil {
				return err
			}
			return h.d.LogAccountAction(r, user.ID.V, "user_role_changed", "Changed the role of user "+user.username()+" from "+from+" to "+to)
		},
	})
}

// bulkDisabled answers POST /api/users/bulk/disable and /enable with
// {ids, dry_run?}, as "disabled" does in PATCH /api/users/{id}: never the
// caller's own account or the last Super admin that can sign in.
// Disabling ends the accounts' sessions and revokes their access tokens.
func (h *handlers) bulkDisabled(disable bool) http.HandlerFunc {
	what := "Enable users"
	if disable {
		what = "Disable users"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h.runBulk(w, r, bulkAction{
			what: what,
			check: func(r *http.Request, user *account, run *bulkRun) (bool, error) {
				if err := h.mayChangeRefusal(r, user); err != nil {
					return false, err
				}
				if user.Disabled.True() == disable {
					return false, nil
				}
				if disable {
					last, err := run.lastSuperAdmin(r, user)
					if err != nil {
						return false, err
					}
					if last {
						return false, sayKey(r, http.StatusBadRequest, "users.last_super_admin")
					}
					run.leaves(user.ID.V)
				}
				return true, nil
			},
			apply: func(r *http.Request, user *account, _ *bulkRun) error {
				return h.setDisabled(r, user, disable)
			},
		})
	}
}

// bulkTwoFactorRequired answers POST /api/users/bulk/two-factor-required
// with {ids, required, dry_run?}: as PUT /api/users/{id}/two-factor-required
// for each local account.
func (h *handlers) bulkTwoFactorRequired(w http.ResponseWriter, r *http.Request) {
	var required bool
	switch member(kit.Body(r), "required").JSON() {
	case "true":
		required = true
	case "false":
	default:
		fail(w, r, http.StatusBadRequest, "Invalid value for required")
		return
	}
	var policies map[int64]authn.AccountPolicy
	h.runBulk(w, r, bulkAction{
		what:       "Require two-factor sign-in",
		ownChecked: true,
		prepare: func(r *http.Request) error {
			var err error
			policies, err = h.d.Authn.AccountPolicies(r.Context())
			return err
		},
		check: func(r *http.Request, user *account, _ *bulkRun) (bool, error) {
			if err := h.securityRefusal(r, user, false); err != nil {
				return false, err
			}
			if err := externalTwoFactor(r, user); err != nil {
				return false, err
			}
			return policies[user.ID.V].TwoFactorRequired != required, nil
		},
		apply: func(r *http.Request, user *account, _ *bulkRun) error {
			return h.applyTwoFactorRequired(r, user, required)
		},
	})
}

// bulkResetLinks answers POST /api/users/bulk/password-reset-link with
// {ids, dry_run?}: as POST /api/users/{id}/password-reset-link for each
// local account with an address. It needs the email settings.
func (h *handlers) bulkResetLinks(w http.ResponseWriter, r *http.Request) {
	h.runBulk(w, r, bulkAction{
		what: "Reset links",
		prepare: func(r *http.Request) error {
			if !h.mailReady(r.Context()) {
				return sayKey(r, http.StatusConflict, "users.invite_needs_email")
			}
			return nil
		},
		check: func(r *http.Request, user *account, _ *bulkRun) (bool, error) {
			if err := h.mayChangeRefusal(r, user); err != nil {
				return false, err
			}
			return true, resetLinkRefusal(r, user)
		},
		apply: func(r *http.Request, user *account, _ *bulkRun) error {
			_, err := h.mailResetLink(r, user)
			return err
		},
	})
}

// bulkDelete answers POST /api/users/bulk/delete with {ids, dry_run?}: as
// DELETE /api/users/{id} without a hand-over for each account. Never the
// caller's own account or the last Super admin that can sign in.
func (h *handlers) bulkDelete(w http.ResponseWriter, r *http.Request) {
	h.runBulk(w, r, bulkAction{
		what:       "Delete users",
		ownChecked: true,
		check: func(r *http.Request, user *account, run *bulkRun) (bool, error) {
			if isSessionAccount(r, user.ID.V) {
				return false, sayKey(r, http.StatusBadRequest, "users.cannot_delete_self")
			}
			if err := h.mayChangeRefusal(r, user); err != nil {
				return false, err
			}
			last, err := run.lastSuperAdmin(r, user)
			if err != nil {
				return false, err
			}
			if last {
				return false, sayKey(r, http.StatusBadRequest, "users.last_admin")
			}
			run.leaves(user.ID.V)
			return true, nil
		},
		apply: func(r *http.Request, user *account, _ *bulkRun) error {
			return h.deleteChecked(r, user, nil)
		},
	})
}

// ---- Organizations ----

// orgRef is the organization of a request, as the API server names it.
type orgRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	segment string
}

// bulkOrganization answers POST /api/users/bulk/organization-add and
// /organization-remove with {ids, org_id, dry_run?}: each account becomes
// a member of the organization, or stops being one. The API server checks
// each as it does POST /api/users/{id}/organizations and DELETE
// /api/panel/org/{id}/members/{userId}.
func (h *handlers) bulkOrganization(add bool) http.HandlerFunc {
	what := "Remove from organization"
	if add {
		what = "Add to organization"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var org orgRef
		h.runBulk(w, r, bulkAction{
			what: what,
			prepare: func(r *http.Request) error {
				var err error
				org, err = h.organizationOf(r, member(kit.Body(r), "org_id"))
				return err
			},
			check: func(r *http.Request, user *account, _ *bulkRun) (bool, error) {
				in, err := h.memberOf(r, user, org.ID)
				if err != nil {
					return false, err
				}
				return in != add, nil
			},
			apply: func(r *http.Request, user *account, _ *bulkRun) error {
				id := accountIDText(user.ID)
				if add {
					_, err := h.d.GoAPI.Post(r.Context(), "/users/"+id+"/organizations", goapi.Body(map[string]string{"org_id": org.ID}))
					if err := apiRefusal(err, http.StatusConflict); err != nil {
						return err
					}
					return h.d.LogAccountAction(r, user.ID.V, "user_org_assigned", "Added user "+user.username()+" to organization "+org.Name)
				}
				_, err := h.d.GoAPI.Delete(r.Context(), "/org/"+org.segment+"/members/"+id)
				if err := apiRefusal(err, http.StatusNotFound); err != nil {
					return err
				}
				return h.d.LogAccountAction(r, user.ID.V, "org_user_unlinked", "Removed user "+user.username()+" from organization "+org.Name)
			},
		})
	}
}

// organizationOf reads the organization an id names from the API server:
// a missing id is refused, one the API server doesn't show the caller is
// answered as it answered.
func (h *handlers) organizationOf(r *http.Request, given jsbind.Value) (orgRef, error) {
	if !given.IsString() || strings.TrimSpace(given.Str()) == "" {
		return orgRef{}, sayKey(r, http.StatusBadRequest, "users.bulk_org_required")
	}
	segment, err := goapipath.SafeSegment(strings.TrimSpace(given.Str()), "org_id")
	if err != nil {
		return orgRef{}, say(http.StatusBadRequest, err.Error())
	}
	resp, err := h.d.GoAPI.Get(r.Context(), "/org/"+segment)
	if err := apiRefusal(err, 0); err != nil {
		return orgRef{}, err
	}
	var org orgRef
	if err := resp.Decode(&org); err != nil {
		return orgRef{}, err
	}
	if org.ID == "" {
		org.ID = strings.TrimSpace(given.Str())
	}
	if org.Name == "" {
		org.Name = org.ID
	}
	org.segment = segment
	return org, nil
}

// memberOf asks the API server whether the account belongs to the
// organization.
func (h *handlers) memberOf(r *http.Request, user *account, orgID string) (bool, error) {
	resp, err := h.d.GoAPI.Get(r.Context(), "/users/"+accountIDText(user.ID)+"/organizations")
	if err := apiRefusal(err, 0); err != nil {
		return false, err
	}
	var out struct {
		Organizations []struct {
			ID    string `json:"id"`
			OrgID string `json:"org_id"`
		} `json:"organizations"`
	}
	if err := resp.Decode(&out); err != nil {
		return false, err
	}
	for _, o := range out.Organizations {
		if o.ID == orgID || o.OrgID == orgID {
			return true, nil
		}
	}
	return false, nil
}

// apiRefusal is what a call to the API server that failed means for one
// account: nil when it worked, errUnchanged for the status that says the
// account was so already, the API server's refusal with its text for an
// answer below 500, and the error itself (a failure) otherwise.
func apiRefusal(err error, unchanged int) error {
	if err == nil {
		return nil
	}
	failed := goapi.AsError(err)
	status := failed.Status()
	if unchanged != 0 && status == unchanged {
		return errUnchanged
	}
	if status == 0 || status >= http.StatusInternalServerError {
		return err
	}
	text := strings.TrimSpace(failed.Response.Text())
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(text), &body) == nil && body.Error != "" {
		text = body.Error
	}
	return say(status, text)
}
