// Package panelaccess answers the panel access routes: the catalog of
// sections and client features, the signed-in account's effective policy,
// and the stored policy of an account or a user group, which user
// managers read and replace. The rules live in console/access.
package panelaccess

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("PANEL-ACCESS")}
	r := d.Router
	view, edit := d.RequirePermission("user.view"), d.RequirePermission("user.edit")
	r.Get("/api/panel/access/catalog", h.catalog, d.RequireAuth, view)
	r.Get("/api/panel/access/effective", h.effective, d.RequireAuth)
	r.Get("/api/panel/access/users/{id}", h.user, d.RequireAuth, view)
	r.Put("/api/panel/access/users/{id}", h.saveUser, d.RequireAuth, edit)
	r.Get("/api/panel/access/groups/{guid}", h.group, d.RequireAuth, view)
	r.Put("/api/panel/access/groups/{guid}", h.saveGroup, d.RequireAuth, edit)
}

// fail answers an error: one with a status of its own (not found, a
// refused subject) with its text, everything else with 500 and the general
// message. The cause of a 500 goes to the log.
func (h *handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := access.ErrorStatus(err)
	if status == http.StatusInternalServerError {
		h.log.Error("Panel access error:", err)
		kit.Fail(w, r, status, kit.T(r, "errors.server_error", nil))
		return
	}
	kit.Fail(w, r, status, err.Error())
}

// audit writes the audit entry of a stored policy. The policy is stored
// by then, so a failed entry is logged and the save still answers.
func (h *handlers) audit(r *http.Request, action, details string) {
	if err := h.d.LogAction(r, action, details); err != nil {
		h.log.Error("Panel access audit entry failed:", err)
	}
}

// catalog answers GET /api/panel/access/catalog.
func (h *handlers) catalog(w http.ResponseWriter, r *http.Request) {
	kit.OK(w, r, access.GetCatalog())
}

// effective answers GET /api/panel/access/effective: what the signed-in
// account may use, resolved now.
func (h *handlers) effective(w http.ResponseWriter, r *http.Request) {
	who := access.PolicyUser{ID: kit.UserID(r)}
	if user := kit.User(r); user != nil {
		who.Role = user.Role
	}
	policy, err := h.d.Access.ResolveForUser(r.Context(), who)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, policy)
}

// userID reads the account id of the path: digits only, and at most the
// largest id the account columns hold on every database. ok is false for
// anything else ("3x", "3.9", " 3", "+3"), which names no account.
func userID(r *http.Request) (id int64, ok bool) {
	raw := r.PathValue("id")
	if raw == "" || strings.TrimLeft(raw, "0123456789") != "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id > math.MaxInt32 {
		return 0, false
	}
	return id, true
}

// findUser loads the account of the id in the path, and answers 404 when
// there is none or the id is no account id.
func (h *handlers) findUser(w http.ResponseWriter, r *http.Request) (*access.PolicyUser, bool) {
	id, ok := userID(r)
	var user *access.PolicyUser
	if ok {
		var err error
		if user, err = h.d.Access.PolicyUserByID(r.Context(), id); err != nil {
			h.fail(w, r, err)
			return nil, false
		}
	}
	if user == nil {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "users.not_found", nil))
		return nil, false
	}
	return user, true
}

type userPolicy struct {
	UserID    int64               `json:"user_id"`
	Sections  []access.SectionRow `json:"sections"`
	Features  []access.FeatureRow `json:"features"`
	Effective *access.Policy      `json:"effective"`
}

// user answers GET /api/panel/access/users/{id}: the rows stored for the
// account and its effective policy.
func (h *handlers) user(w http.ResponseWriter, r *http.Request) {
	user, ok := h.findUser(w, r)
	if !ok {
		return
	}
	out := userPolicy{UserID: user.ID}
	var err error
	subject := jsreq.String(float64(user.ID))
	if out.Sections, err = h.d.Access.PanelAccessPolicies(r.Context(), access.SubjectUser, subject); err == nil {
		if out.Features, err = h.d.Access.ClientFeaturePolicies(r.Context(), access.SubjectUser, subject); err == nil {
			out.Effective, err = h.d.Access.ResolveForUser(r.Context(), *user)
		}
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, out)
}

// errBadPolicy refuses a save whose body is no object, or whose sections
// or features are there and no list: storing it would delete every row.
var errBadPolicy = &access.StatusError{Status: http.StatusBadRequest, Message: "Invalid access policy"}

// submitted is the "sections" and "features" of the body, as decoded JSON.
// Either may be left out (or null); when given it is a list.
func submitted(r *http.Request) (sections, features any, err error) {
	var body map[string]any
	if err := kit.Bind(r, &body); err != nil {
		return nil, nil, errBadPolicy
	}
	for _, key := range []string{"sections", "features"} {
		if value := body[key]; value != nil {
			if _, isList := value.([]any); !isList {
				return nil, nil, errBadPolicy
			}
		}
	}
	return body["sections"], body["features"], nil
}

// errNotYours refuses a save for an account the caller may not change,
// with the users routes' answer.
var errNotYours = &access.StatusError{Status: http.StatusForbidden, Message: "Cannot assign a role higher than your own"}

// keepsOwnWrite reports whether the caller keeps write access to Users,
// where panel access is saved, once the subject's section rows are
// replaced by sections. It answers 400 with the reason, or the error,
// when it does not.
func (h *handlers) keepsOwnWrite(w http.ResponseWriter, r *http.Request, subjectType, subjectID string, sections any) bool {
	caller := access.PolicyUser{ID: kit.UserID(r)}
	if user := kit.User(r); user != nil {
		caller.Role = user.Role
	}
	keeps, err := h.d.Access.KeepsUsersWrite(r.Context(), caller, subjectType, subjectID, sections)
	if err != nil {
		h.fail(w, r, err)
		return false
	}
	if !keeps {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "users.access_keep_own_write", nil))
	}
	return keeps
}

// mayChange reports whether the caller may change the account
// (access.MayChangeAccount).
func mayChange(r *http.Request, target access.PolicyUser) bool {
	role := ""
	if user := kit.User(r); user != nil {
		role = user.Role
	}
	return access.MayChangeAccount(kit.UserID(r), role, target)
}

// saveUser answers PUT /api/panel/access/users/{id} with the body
// {sections, features}: it replaces the account's stored rows, for an
// account the caller may change, unless the caller would lose write
// access to Users by it. The answer holds the rows as they were stored
// and the new effective policy.
func (h *handlers) saveUser(w http.ResponseWriter, r *http.Request) {
	user, ok := h.findUser(w, r)
	if !ok {
		return
	}
	if !mayChange(r, *user) {
		h.fail(w, r, errNotYours)
		return
	}
	subject := jsreq.String(float64(user.ID))
	sections, features, err := submitted(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.keepsOwnWrite(w, r, access.SubjectUser, subject, sections) {
		return
	}
	saved, err := h.d.Access.SavePolicy(r.Context(), access.SubjectUser, subject, sections, features)
	if err == nil {
		h.audit(r, "user_access_policy_updated", "Updated access policy for user "+subject)
	}
	out := userPolicy{UserID: user.ID, Sections: saved.Sections, Features: saved.Features}
	if err == nil {
		out.Effective, err = h.d.Access.ResolveForUser(r.Context(), *user)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, out)
}

// findGroup loads the user group of the guid in the path, and answers 404
// when there is none.
func (h *handlers) findGroup(w http.ResponseWriter, r *http.Request) (*access.UserGroup, bool) {
	group, err := h.d.Access.UserGroupByGUID(r.Context(), r.PathValue("guid"))
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	if group == nil {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "users.user_group_not_found", nil))
		return nil, false
	}
	return group, true
}

type groupPolicy struct {
	GroupGUID string              `json:"group_guid"`
	Sections  []access.SectionRow `json:"sections"`
	Features  []access.FeatureRow `json:"features"`
	Effective *access.Policy      `json:"effective"`
}

// group answers GET /api/panel/access/groups/{guid}: the rows stored for
// the user group and its policy filled up with defaults.
func (h *handlers) group(w http.ResponseWriter, r *http.Request) {
	group, ok := h.findGroup(w, r)
	if !ok {
		return
	}
	guid := r.PathValue("guid")
	out := groupPolicy{GroupGUID: guid}
	var err error
	if out.Sections, err = h.d.Access.PanelAccessPolicies(r.Context(), access.SubjectGroup, guid); err == nil {
		if out.Features, err = h.d.Access.ClientFeaturePolicies(r.Context(), access.SubjectGroup, guid); err == nil {
			out.Effective, err = h.d.Access.ResolveForGroup(r.Context(), group.GUID)
		}
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, out)
}

// saveGroup answers PUT /api/panel/access/groups/{guid} with the body
// {sections, features}: it replaces the user group's stored rows, when the
// caller may change every member of the group and does not lose write
// access to Users by it.
func (h *handlers) saveGroup(w http.ResponseWriter, r *http.Request) {
	group, ok := h.findGroup(w, r)
	if !ok {
		return
	}
	members, err := h.d.Access.UserGroupMembers(r.Context(), group.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for _, member := range members {
		if !mayChange(r, member) {
			h.fail(w, r, errNotYours)
			return
		}
	}
	guid := r.PathValue("guid")
	sections, features, err := submitted(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.keepsOwnWrite(w, r, access.SubjectGroup, guid, sections) {
		return
	}
	saved, err := h.d.Access.SavePolicy(r.Context(), access.SubjectGroup, guid, sections, features)
	if err == nil {
		h.audit(r, "user_group_access_policy_updated", "Updated access policy for group "+guid)
	}
	out := groupPolicy{GroupGUID: guid, Sections: saved.Sections, Features: saved.Features}
	if err == nil {
		out.Effective, err = h.d.Access.ResolveForGroup(r.Context(), group.GUID)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, out)
}
