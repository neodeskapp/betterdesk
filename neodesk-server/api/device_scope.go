package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// deviceInScope reports whether the caller may act on one device: a caller
// with an organization scope only on its organization's devices, and every
// caller only on a device that the device groups, folders and grants let it
// see, the scope GET /api/peers filters by. Super admins and global admins
// (the console's in-process identity among them) see every device. A device
// the server has no row for passes the device group check, so the handler
// answers for it as before. When the device is out of scope it writes 403
// and returns false.
func (s *Server) deviceInScope(w http.ResponseWriter, r *http.Request, peerID string) bool {
	role := getRoleFromCtx(r)
	if auth.IsSuperAdminRole(role) || role == auth.RoleGlobalAdmin {
		return true
	}
	if orgID := getOrgIDFromCtx(r); orgID != "" {
		if od, _ := s.db.GetOrgDevice(orgID, peerID); od == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device not in your organization"})
			return false
		}
	}
	visible, err := s.deviceVisible(r, peerID)
	if err != nil {
		writeInternalError(w, err, "device scope")
		return false
	}
	if !visible {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device not in your scope"})
		return false
	}
	return true
}

// deviceVisible reports whether the device groups, folders and grants let
// the caller see the device.
func (s *Server) deviceVisible(r *http.Request, peerID string) (bool, error) {
	if s.panelStore == nil {
		return true, nil
	}
	p, err := s.db.GetPeer(peerID)
	if err != nil {
		return false, fmt.Errorf("load device %s: %w", peerID, err)
	}
	if p == nil {
		return true, nil
	}
	visible, err := s.visiblePeerSet(r, []*db.Peer{p})
	if err != nil {
		return false, err
	}
	return visible == nil || visible[p.ID], nil
}

// visiblePeerSet returns which of peers the caller may see, by its
// organization and by the device groups, folders and grants; nil means all
// of them.
func (s *Server) visiblePeerSet(r *http.Request, peers []*db.Peer) (map[string]bool, error) {
	username, role := getUsernameFromCtx(r), getRoleFromCtx(r)
	if auth.IsSuperAdminRole(role) || role == auth.RoleGlobalAdmin {
		return nil, nil
	}
	peerByID := make(map[string]*db.Peer, len(peers))
	for _, p := range peers {
		if p != nil {
			peerByID[p.ID] = p
		}
	}
	orgID := getOrgIDFromCtx(r)
	if orgID != "" {
		inOrg, err := s.db.ListPeersForOrg(orgID, true)
		if err != nil {
			return nil, fmt.Errorf("list devices of organization %s: %w", orgID, err)
		}
		held := make(map[string]bool, len(inOrg))
		for _, p := range inOrg {
			if p != nil {
				held[p.ID] = true
			}
		}
		for id := range peerByID {
			if !held[id] {
				delete(peerByID, id)
			}
		}
	}
	var visible map[string]bool
	if username != "" && role != "" {
		visible = s.rustDeskVisiblePeerSet(s.rustDeskUserForGroups(r, username, role), role, peerByID)
	}
	if visible == nil && orgID == "" {
		return nil, nil
	}
	// Grants can name devices outside peers or outside the organization.
	kept := make(map[string]bool, len(peerByID))
	for id := range peerByID {
		if visible == nil || visible[id] {
			kept[id] = true
		}
	}
	return kept, nil
}

// visiblePeers keeps the peers the caller may see (see visiblePeerSet).
func (s *Server) visiblePeers(r *http.Request, peers []*db.Peer) ([]*db.Peer, error) {
	visible, err := s.visiblePeerSet(r, peers)
	if err != nil || visible == nil {
		return peers, err
	}
	kept := make([]*db.Peer, 0, len(peers))
	for _, p := range peers {
		if p != nil && visible[p.ID] {
			kept = append(kept, p)
		}
	}
	return kept, nil
}

// visibleSnapshotIDs returns which of the live snapshots the caller may
// see; nil means all of them. A snapshot of a device without a row is seen
// only by a caller that sees every device.
func (s *Server) visibleSnapshotIDs(r *http.Request, snapshots []peer.Snapshot) (map[string]bool, error) {
	role := getRoleFromCtx(r)
	if auth.IsSuperAdminRole(role) || role == auth.RoleGlobalAdmin {
		return nil, nil
	}
	if s.panelStore == nil && getOrgIDFromCtx(r) == "" {
		return nil, nil
	}
	peers := make([]*db.Peer, 0, len(snapshots))
	for _, snap := range snapshots {
		p, err := s.db.GetPeer(snap.ID)
		if err != nil {
			return nil, fmt.Errorf("load device %s: %w", snap.ID, err)
		}
		peers = append(peers, p)
	}
	visible, err := s.visiblePeerSet(r, peers)
	if err != nil || visible != nil {
		return visible, err
	}
	all := make(map[string]bool, len(peers))
	for _, p := range peers {
		if p != nil {
			all[p.ID] = true
		}
	}
	return all, nil
}

// scopedDevice runs handler only for a device in the caller's scope (see
// deviceInScope); the device is the path value {id}.
func (s *Server) scopedDevice(handler http.HandlerFunc) http.HandlerFunc {
	return s.scopedDeviceAt("id", handler)
}

// scopedDeviceAt is scopedDevice for a route whose device is the path
// value name.
func (s *Server) scopedDeviceAt(name string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.deviceInScope(w, r, strings.TrimSpace(r.PathValue(name))) {
			return
		}
		handler(w, r)
	}
}
