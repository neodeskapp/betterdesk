package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// supportAgentSKU is the product of the quick support agent, which takes
// no command but collect.metrics.
const supportAgentSKU = "neodesk-support"

// isSupportAgent reports whether the device last identified itself as the
// support agent.
func (s *Server) isSupportAgent(deviceID string) bool {
	snapshot, err := s.db.GetTelemetrySnapshot(deviceID, "identity")
	if err != nil || snapshot == nil {
		return false
	}
	var identity struct {
		ProductSKU string `json:"product_sku"`
	}
	return json.Unmarshal([]byte(snapshot.Payload), &identity) == nil && identity.ProductSKU == supportAgentSKU
}

// fleetTargetError is a target text that names something unknown or a
// device that takes no fleet work.
type fleetTargetError struct {
	status  int
	message string
	code    string
	names   []string
}

func (e *fleetTargetError) write(w http.ResponseWriter) {
	body := map[string]any{"error": e.message}
	if e.code != "" {
		body["code"] = e.code
	}
	if len(e.names) > 0 {
		body["names"] = e.names
	}
	writeJSON(w, e.status, body)
}

// fleetTargets is what a target text resolved to.
type fleetTargets struct {
	// Devices are the device ids, each once, in the order they came.
	Devices []string
	// Skipped are support agents a group, tag or organization named:
	// they take no fleet work and are left out.
	Skipped []string
	// Orgs are the organizations of the devices ("" for a device without one).
	Orgs map[string]bool
}

// splitTargets splits a target text: at commas, semicolons and line
// breaks when it has any (so a group name may have spaces), else at
// spaces.
func splitTargets(text string) []string {
	sep := func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }
	if !strings.ContainsAny(text, ",;\n\r") {
		sep = func(r rune) bool { return r == ' ' || r == '\t' }
	}
	var out []string
	for _, part := range strings.FieldsFunc(text, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// fleetGroups finds device groups by name or guid and their members.
type fleetGroups struct {
	s      *Server
	loaded bool
	groups []*db.DeviceGroup
}

func (g *fleetGroups) find(ref string) *db.DeviceGroup {
	if !g.loaded {
		g.loaded = true
		g.groups, _ = g.s.db.ListDeviceGroups()
	}
	for _, group := range g.groups {
		if group.GUID == ref {
			return group
		}
	}
	for _, group := range g.groups {
		if strings.EqualFold(group.Name, ref) {
			return group
		}
	}
	return nil
}

// members are the device ids of a group: its members, or the devices
// with its tag for a tag group.
func (g *fleetGroups) members(group *db.DeviceGroup) []string {
	var ids []string
	if group.SourceType == "tag" && group.TagFilter != "" {
		peers, _ := g.s.db.ListPeersByTag(group.TagFilter)
		for _, p := range peers {
			ids = append(ids, p.ID)
		}
		return ids
	}
	if store, ok := g.s.db.(db.PanelGroupStore); ok {
		ids, _ = store.ListDeviceGroupPeerIDs(group.GUID)
	}
	return ids
}

// resolveFleetTargets reads a task's target text: device ids, device
// group names, org:<id>, group:<name or guid> and tag:<tag>. With a
// request, only what the caller may see counts. A name that is nothing
// the caller may see is refused (400), and so is a support agent named by
// its id (403); one a group, tag or organization brings in is skipped.
func (s *Server) resolveFleetTargets(r *http.Request, text string) (*fleetTargets, *fleetTargetError) {
	tokens := splitTargets(text)
	if len(tokens) == 0 {
		return nil, &fleetTargetError{status: http.StatusBadRequest, message: "Enter at least one device ID or group"}
	}
	if len(tokens) > 500 {
		return nil, &fleetTargetError{status: http.StatusBadRequest, message: "Name at most 500 devices or groups"}
	}
	groups := &fleetGroups{s: s}
	scopeOrg := ""
	if r != nil {
		scopeOrg = getOrgIDFromCtx(r)
	}
	type pick struct {
		id       string
		explicit bool
	}
	var picks []pick
	var unknown []string
	for _, token := range tokens {
		kind, value, prefixed := strings.Cut(token, ":")
		if !prefixed {
			kind, value = "", token
		}
		kind = strings.ToLower(kind)
		value = strings.TrimSpace(value)
		switch {
		case kind == "org" && value != "":
			if scopeOrg != "" && value != scopeOrg {
				unknown = append(unknown, token)
				continue
			}
			if org, err := s.db.GetOrganization(value); err != nil || org == nil {
				unknown = append(unknown, token)
				continue
			}
			devices, _ := s.db.ListOrgDevices(value)
			for _, d := range devices {
				picks = append(picks, pick{id: d.DeviceID})
			}
		case kind == "group" && value != "":
			group := groups.find(value)
			if group == nil {
				unknown = append(unknown, token)
				continue
			}
			for _, id := range groups.members(group) {
				picks = append(picks, pick{id: id})
			}
		case kind == "tag" && value != "":
			peers, _ := s.db.ListPeersByTag(value)
			for _, p := range peers {
				picks = append(picks, pick{id: p.ID})
			}
		default:
			if p, err := s.db.GetPeer(token); err == nil && p != nil && !p.SoftDeleted {
				picks = append(picks, pick{id: p.ID, explicit: true})
				continue
			}
			if group := groups.find(token); group != nil {
				for _, id := range groups.members(group) {
					picks = append(picks, pick{id: id})
				}
				continue
			}
			unknown = append(unknown, token)
		}
	}

	// What the caller may see, and only devices the server knows
	ids := make([]string, 0, len(picks))
	for _, p := range picks {
		ids = append(ids, p.id)
	}
	known, err := s.db.GetPeersByIDs(ids)
	if err != nil {
		return nil, &fleetTargetError{status: http.StatusInternalServerError, message: "Could not read the devices"}
	}
	var visible map[string]bool
	if r != nil {
		peers := make([]*db.Peer, 0, len(known))
		for _, p := range known {
			peers = append(peers, p)
		}
		if visible, err = s.visiblePeerSet(r, peers); err != nil {
			return nil, &fleetTargetError{status: http.StatusInternalServerError, message: "Could not read the devices"}
		}
	}
	out := &fleetTargets{Orgs: map[string]bool{}}
	seen := map[string]bool{}
	var restricted []string
	for _, p := range picks {
		if seen[p.id] {
			continue
		}
		seen[p.id] = true
		if known[p.id] == nil || (visible != nil && !visible[p.id]) {
			if p.explicit {
				unknown = append(unknown, p.id)
			}
			continue
		}
		if s.isSupportAgent(p.id) {
			if p.explicit {
				restricted = append(restricted, p.id)
			} else {
				out.Skipped = append(out.Skipped, p.id)
			}
			continue
		}
		out.Devices = append(out.Devices, p.id)
		org, _ := s.db.GetDeviceOrgID(p.id)
		out.Orgs[org] = true
	}
	if len(unknown) > 0 {
		return nil, &fleetTargetError{status: http.StatusBadRequest,
			message: "Unknown devices or groups: " + strings.Join(unknown, ", "), code: "unknown_targets", names: unknown}
	}
	if len(restricted) > 0 {
		return nil, &fleetTargetError{status: http.StatusForbidden,
			message: "Support agents take no fleet tasks: " + strings.Join(restricted, ", "), code: "support_agent_restricted", names: restricted}
	}
	if len(out.Devices) == 0 {
		return nil, &fleetTargetError{status: http.StatusBadRequest, message: "The devices and groups named have no devices", code: "no_devices"}
	}
	return out, nil
}

// singleOrg is the one organization all devices are in, else "".
func (t *fleetTargets) singleOrg() string {
	if len(t.Orgs) != 1 {
		return ""
	}
	for org := range t.Orgs {
		return org
	}
	return ""
}

// fleetOrgAllowed reports whether the caller may see orgID's fleet data,
// as requireOrgMembership decides.
func (s *Server) fleetOrgAllowed(r *http.Request, orgID string) bool {
	role := getRoleFromCtx(r)
	if auth.IsSuperAdminRole(role) || role == auth.RoleGlobalAdmin {
		return true
	}
	if own := getOrgIDFromCtx(r); own != "" {
		return own == orgID
	}
	if username := getUsernameFromCtx(r); username != "" {
		if u, err := s.db.GetOrgUserByUsername(orgID, username); err == nil && u != nil {
			return true
		}
	}
	return false
}

// fleetOrgDevices are the device ids of the organizations: one, or every
// organization when orgID is "".
func (s *Server) fleetOrgDevices(orgID string) ([]string, map[string]string, error) {
	var orgs []string
	if orgID != "" {
		orgs = []string{orgID}
	} else {
		all, err := s.db.ListOrganizations()
		if err != nil {
			return nil, nil, err
		}
		for _, o := range all {
			orgs = append(orgs, o.ID)
		}
	}
	var ids []string
	orgOf := map[string]string{}
	for _, org := range orgs {
		devices, err := s.db.ListOrgDevices(org)
		if err != nil {
			return nil, nil, err
		}
		for _, d := range devices {
			if _, dup := orgOf[d.DeviceID]; !dup {
				ids = append(ids, d.DeviceID)
				orgOf[d.DeviceID] = org
			}
		}
	}
	sort.Strings(ids)
	return ids, orgOf, nil
}
