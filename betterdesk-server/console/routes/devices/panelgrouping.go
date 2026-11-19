package devices

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/api"
	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The device group writes go to the API server, its /api/panel/device-groups
// routes (services/panelGrouping.js), unless BACKEND_PANEL_GROUPING is node.
// The console's own writer stays as the fallback for an API server that
// cannot take them: an answer of 404, 501 or 503, or no answer. Any other
// refusal is answered as it came, with the API server's status (502 when it
// gave none) and the error of the call.

// goRefusal is err.fromGo: a refusal of the API server that the route
// answers with status and message.
type goRefusal struct {
	status  int
	message string
}

func (e *goRefusal) Error() string { return e.message }

// writesToGo is writesGoToGo.
func (h *handlers) writesToGo() bool { return !h.d.Config.PanelGroupingNode }

var unreachable = regexp.MustCompile(`(?i)ECONNREFUSED|ENOTFOUND|timeout|socket hang up`)

// unavailable is isUnavailable: the API server's status was 404, 501 or
// 503, or the error reads like a call without an answer. Reading the error
// fails where String() of it throws.
func unavailable(result *goapi.Object) (bool, error) {
	switch result.Get("status") {
	case float64(http.StatusNotFound), float64(http.StatusNotImplemented), float64(http.StatusServiceUnavailable):
		return true, nil
	}
	message, err := textOr(result.Get("error"), "")
	if err != nil {
		return false, err
	}
	return unreachable.MatchString(message), nil
}

// fromGo is what saveGroupToGo and the other writers return for a failed
// call: nil when the console's own writer takes over, else the refusal.
// fallback is the message for a refusal without an error.
func fromGo(result *goapi.Object, fallback string) error {
	down, err := unavailable(result)
	if err != nil || down {
		return err
	}
	message, err := textOr(result.Get("error"), fallback)
	if err != nil {
		return err
	}
	status := http.StatusBadGateway
	if s, ok := result.Get("status").(float64); ok && s != 0 {
		status = int(s)
	}
	return &goRefusal{status: status, message: message}
}

// saveGroupToGo is saveGroupToGo: the saved group, nil when the console's
// own writer takes over (the API server cannot take the write, or it
// answered success without a group).
func (h *handlers) saveGroupToGo(r *http.Request, payload *goapi.Object) (any, error) {
	result := h.api.SavePanelDeviceGroup(r.Context(), payload, h.panelCall(r)...)
	if goapi.Truthy(result.Get("success")) {
		return result.Get("data"), nil
	}
	return nil, fromGo(result, "Could not save the device group")
}

// deleteGroupFromGo is deleteGroupFromGo: true when the API server deleted
// the group, false when the console's own writer takes over.
func (h *handlers) deleteGroupFromGo(r *http.Request, guid string) (bool, error) {
	result := h.api.DeletePanelDeviceGroup(r.Context(), guid, h.panelCall(r)...)
	if goapi.Truthy(result.Get("success")) {
		return true, nil
	}
	return false, fromGo(result, "Could not delete the device group")
}

// groupPeersFromGo is getGroupPeersFromGo: the ids of the group's members,
// ok false when the API server gave no list.
func (h *handlers) groupPeersFromGo(r *http.Request, guid string) (peers []any, ok bool) {
	result := h.api.GetPanelDeviceGroupPeers(r.Context(), guid, h.panelCall(r)...)
	if !goapi.Truthy(result.Get("success")) || !goapi.Truthy(result.Get("data")) {
		return nil, false
	}
	peers, ok = dig(result.Get("data"), "peers").([]any)
	return peers, ok
}

// setGroupPeersInGo is setGroupPeersInGo: saved is false when the console's
// own writer takes over, also after an answer of success without data.
func (h *handlers) setGroupPeersInGo(r *http.Request, guid string, peers []any) (saved bool, err error) {
	result := h.api.SetPanelDeviceGroupPeers(r.Context(), guid, peers, h.panelCall(r)...)
	if goapi.Truthy(result.Get("success")) {
		return goapi.Truthy(result.Get("data")), nil
	}
	return false, fromGo(result, "Could not save group members")
}

// membershipToGo writes the device's manual groups through the API
// server: it reads the members of every manual group, puts the device into
// the selected one and takes it out of the others, then writes each
// group's members back. saved is false when the console's own writer
// takes over: a group's members could not be read, or a write went
// nowhere, which can leave the groups written before it changed.
func (h *handlers) membershipToGo(r *http.Request, id string, manual, selected []string) (saved bool, err error) {
	type update struct {
		guid  string
		peers []any
	}
	updates := make([]update, 0, len(manual))
	for _, guid := range manual {
		peers, ok := h.groupPeersFromGo(r, guid)
		if !ok {
			return false, nil
		}
		in := len(selected) == 1 && selected[0] == guid
		updates = append(updates, update{guid: guid, peers: withPeer(peers, id, in)})
	}
	for _, u := range updates {
		if saved, err := h.setGroupPeersInGo(r, u.guid, u.peers); err != nil || !saved {
			return false, err
		}
	}
	return true, nil
}

// panelCall is panelConfig: the options of a call into the panel grouping
// of the API server. With the panel trust bridge on, the Node console sent
// a statement of the operator's name and role; the API server then acted
// as that operator, with the role the account has in the users table. In
// process the call carries that identity, for every guid in the path.
// Where the statement did not hold (a line break in the name or role), the
// API server refused it with 401, which an identity without a role gets
// too. Without a name or a role in the session Node sent no statement, and
// the call acts as the API key.
func (h *handlers) panelCall(r *http.Request) []goapi.CallOption {
	user := kit.User(r)
	if !h.d.Config.PanelTrust || user == nil {
		return nil
	}
	subject, role := jsTrim(user.Username), jsTrim(user.Role)
	if subject == "" || role == "" {
		return nil
	}
	id := api.Identity{Username: subject}
	if !strings.ContainsRune(subject+role, '\n') {
		id.Role = panelRole(r.Context(), h.d.Store, subject, role)
	}
	return []goapi.CallOption{goapi.As(id)}
}

// panelRole is the API server's resolvePanelRole: the role of the account
// named subject when it has one (none, "", when the server does not know
// that role), else the claimed role when the server knows it and it is no
// device or pro role.
func panelRole(ctx context.Context, db *store.DB, subject, claimed string) string {
	if role, found := accountRole(ctx, db, subject); found {
		if !auth.ValidRole(role) {
			return ""
		}
		return role
	}
	if !auth.ValidRole(claimed) || auth.IsDeviceRole(claimed) || auth.IsProRole(claimed) {
		return ""
	}
	return claimed
}

// groupPayload is the group saveGroupToGo sends: the payload's fields in
// the order of the route, a note and a team id only when the body had
// them.
func groupPayload(p groupFields) *goapi.Object {
	out := goapi.NewObject("guid", p.guid, "name", p.name)
	if p.note != nil {
		out.Set("note", *p.note)
	}
	if p.teamID != nil {
		out.Set("team_id", *p.teamID)
	}
	return out.Set("source_type", p.sourceType).Set("tag_filter", p.tagFilter).
		Set("allowed_users", listOf(p.allowedUsers)).Set("allowed_groups", listOf(p.allowedGroups))
}

// groupFields are the fields of a normalized group payload that go to the
// API server.
type groupFields struct {
	guid, name, sourceType, tagFilter string
	note, teamID                      *string
	allowedUsers, allowedGroups       []string
}

func listOf(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// savedName is String(saved.name) of what the API server saved: "undefined"
// for a value without a name.
func savedName(saved any) (string, error) {
	if o, ok := saved.(*goapi.Object); ok && o != nil && o.Has("name") {
		return text(o.Get("name"))
	}
	return "undefined", nil
}

// withPeer is the member list of a group after the change: peers as a
// set, with id added or taken out. Equal texts, numbers and booleans count
// once; lists and objects are never equal to anything.
func withPeer(peers []any, id string, in bool) []any {
	out := make([]any, 0, len(peers)+1)
	seen := map[any]bool{}
	for _, p := range peers {
		switch p.(type) {
		case string, float64, bool, nil:
			if seen[p] {
				continue
			}
			seen[p] = true
		}
		if !in && p == id {
			continue
		}
		out = append(out, p)
	}
	if in && !seen[id] {
		out = append(out, id)
	}
	return out
}
