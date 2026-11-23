package users

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// plainMember is body[name] as encoding/json decodes it, the form the
// scope functions of package access read.
func plainMember(body middleware.JSONObject, name string) any {
	var v any
	if raw := body.Raw(name); raw != nil {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

// checkScope checks the device scope of a body before anything is
// written: folder and device ids that can be read as text, and a strategy
// that exists. It returns the strategy's guid.
func (h *handlers) checkScope(ctx context.Context, body middleware.JSONObject) (string, error) {
	// Only a list of folders is read, item by item as text
	if ids := member(body, "folderIds"); body.Has("folderIds") && ids.IsArray() && hidesToString(ids) {
		return "", errNotPrimitive
	}
	if body.Has("peerIds") && hidesToString(member(body, "peerIds")) {
		return "", errNotPrimitive
	}
	if err := h.checkKnown(ctx, body); err != nil {
		return "", err
	}
	if !body.Has("strategyGuid") {
		return "", nil
	}
	strategy, err := trimmedOr(member(body, "strategyGuid"), "")
	if err != nil || strategy == "" {
		return "", err
	}
	known, err := strategyExists(ctx, h.d.Store, strategy)
	if err != nil {
		return "", err
	}
	if !known {
		return "", errStrategyNotFound
	}
	return strategy, nil
}

// devicePattern is the form of a device ID a direct grant may name.
var devicePattern = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,64}$`)

// checkKnown refuses what used to be stored or dropped without a word:
// a user group or a folder that doesn't exist, and a direct grant whose
// device ID is no ID. A well-formed ID of a device that hasn't joined yet
// is taken, as before.
func (h *handlers) checkKnown(ctx context.Context, body middleware.JSONObject) error {
	guids, _, err := groupGUIDsOf(body)
	if err != nil {
		return err
	}
	for _, guid := range guids {
		known, err := userGroupExists(ctx, h.d.Store, guid)
		if err != nil {
			return err
		}
		if !known {
			return badRequestKey("users.unknown_user_group")
		}
	}
	if ids, isList := plainMember(body, "folderIds").([]any); body.Has("folderIds") && isList {
		folders, err := h.d.Access.AllFolders(ctx)
		if err != nil {
			return err
		}
		existing := make(map[int64]bool, len(folders))
		for _, folder := range folders {
			existing[folder.ID] = true
		}
		normalized := access.NormalizeFolderIDs(ids)
		if len(normalized) != len(ids) {
			return badRequestKey("users.unknown_folder")
		}
		for _, id := range normalized {
			if !existing[id] {
				return badRequestKey("users.unknown_folder")
			}
		}
	}
	if body.Has("peerIds") {
		for _, id := range access.NormalizePeerIDs(plainMember(body, "peerIds")) {
			if !devicePattern.MatchString(id) {
				return badRequestKey("users.invalid_device_id")
			}
		}
	}
	return nil
}

// applyScope is applyUserScopeFromBody after checkScope: the folders of
// body.folderIds, the device grants of body.peerIds and the strategy,
// each only when the body has it, in this order. The assignment of the
// strategy is told to the API server too; whether that works does not
// matter.
func (h *handlers) applyScope(ctx context.Context, userID int64, username string, body middleware.JSONObject, strategy string) error {
	if body.Has("folderIds") {
		if _, err := h.d.Access.SyncUserFolderAccess(ctx, username, plainMember(body, "folderIds")); err != nil {
			return err
		}
	}
	if body.Has("peerIds") {
		if _, err := h.d.Access.SyncUserPeerGrants(ctx, userID, plainMember(body, "peerIds")); err != nil {
			return err
		}
	}
	if body.Has("strategyGuid") {
		assigned, err := assignStrategy(ctx, h.d.Store, userID, strategy)
		if err != nil {
			return err
		}
		if h.d.Backend.IsNeoDesk() {
			h.tellStrategy(ctx, username, assigned)
		}
	}
	return nil
}

// strategyAssignment is the body of POST /api/strategies/assign: no
// strategy takes the assignment away.
type strategyAssignment struct {
	Strategy string   `json:"strategy,omitempty"`
	Users    []string `json:"users"`
}

// tellStrategy hands an account's strategy to the API server. A failure
// is logged and changes nothing: the assignment is stored already.
func (h *handlers) tellStrategy(ctx context.Context, username, strategy string) {
	key, err := assignmentKey(ctx, h.d.Store, jsbind.Trim(username))
	if err == nil {
		_, err = h.d.GoAPI.Post(ctx, "/strategies/assign", goapi.Body(strategyAssignment{Strategy: strategy, Users: []string{key}}))
	}
	if err != nil {
		h.log.Warn("[users] Strategy assign Go sync failed:", err.Error())
	}
}

// effectiveScope answers GET /api/users/{id}/effective-scope: how many
// devices the account may see, and whether that is a limit.
func (h *handlers) effectiveScope(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Effective scope error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	devices := devicegroups.ScopeDevices(h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
	count, err := h.d.Access.CountEffectiveScope(ctx, &access.ScopeUser{ID: user.ID.V, Username: user.username(), Role: user.role()}, devices)
	if err != nil {
		h.serverError(w, r, "Effective scope error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool              `json:"success"`
		Data    access.ScopeCount `json:"data"`
	}{true, count})
}

// apiUserID answers for an id the API server does not know as it was
// answered: 400 for an id that is no positive number, 404 for no account.
// It returns the id of the API server's account, which is the console's.
func (h *handlers) apiUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return "", false
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Resolve user organizations error:", err)
		return "", false
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return "", false
	}
	// The account is looked up once more, as the API server's, which is
	// the same row; one that went away in between was not synchronized
	again, err := accountByID(ctx, h.d.Store, userID)
	if err != nil || again == nil || again.ID.V == 0 {
		fail(w, r, http.StatusBadGateway, "User is not synchronized with the Go server")
		return "", false
	}
	return accountIDText(again.ID), true
}

// organizations answers GET /api/users/{id}/organizations with what the
// API server answers for the account.
func (h *handlers) organizations(w http.ResponseWriter, r *http.Request) {
	id, ok := h.apiUserID(w, r)
	if !ok {
		return
	}
	goapipath.ProxyToGo(h.d.GoAPI, w, r, http.MethodGet, goapipath.Fixed("/users/"+id+"/organizations"))
}

// assignOrganization answers POST /api/users/{id}/organizations by handing
// the body to the API server.
func (h *handlers) assignOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := h.apiUserID(w, r)
	if !ok {
		return
	}
	goapipath.ProxyToGo(h.d.GoAPI, w, r, http.MethodPost, goapipath.Fixed("/users/"+id+"/organizations"),
		goapipath.Body(goapipath.RequestBody(r)))
}
