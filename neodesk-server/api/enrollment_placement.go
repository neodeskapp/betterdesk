package api

// Where a device that enrolls with an enrollment token goes. A token can
// name a folder (device group) and an organization, whose policies then
// apply to the device (GET /api/peers/{id}/policy finds them through the
// organization). The registration may name a group of its own (the
// receiver MSI's GROUP property); it counts only next to a token that
// admitted the device, and the token's own folder wins over it.

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// enrollmentGroupMax is the longest group a registration may name, in bytes.
const enrollmentGroupMax = 128

// folderNamed is the id of the folder group names: a folder id, or a
// folder's name in any case; 0 when no folder fits or the database keeps
// no folders.
func (s *Server) folderNamed(group string) int64 {
	group = strings.TrimSpace(group)
	if group == "" {
		return 0
	}
	store, err := s.panelGroupStore()
	if err != nil {
		return 0
	}
	folders, err := store.ListPanelFolders()
	if err != nil {
		log.Printf("[API] ListPanelFolders for an enrollment group: %v", err)
		return 0
	}
	if id, err := strconv.ParseInt(group, 10, 64); err == nil && id > 0 {
		for _, f := range folders {
			if f.ID == id {
				return id
			}
		}
	}
	for _, f := range folders {
		if strings.EqualFold(strings.TrimSpace(f.Name), group) {
			return f.ID
		}
	}
	return 0
}

// placeEnrolledDevice puts a device the token admitted into the token's
// folder, else into the folder the registration's group names, and into
// the token's organization. What fails is logged; the device stays
// enrolled.
func (s *Server) placeEnrolledDevice(tok *db.DeviceToken, deviceID, group string) {
	if tok == nil {
		return
	}
	folder := tok.FolderID
	if folder <= 0 {
		folder = s.folderNamed(group)
	}
	if folder > 0 {
		if store, err := s.panelGroupStore(); err == nil {
			if err := store.SetDevicePanelFolder(deviceID, folder); err != nil {
				log.Printf("[API] Put %s into folder %d of its enrollment token: %v", deviceID, folder, err)
			}
		}
	}
	if tok.OrgID != "" {
		if err := s.db.AssignDeviceToOrg(&db.OrgDevice{OrgID: tok.OrgID, DeviceID: deviceID}); err != nil {
			log.Printf("[API] Put %s into organization %s of its enrollment token: %v", deviceID, tok.OrgID, err)
		}
	}
}

// tokenPlacementOK checks the folder and the organization a token is
// to name, before it is stored: a folder must exist and be open to the
// caller, an organization must exist. It answers the refusal itself and
// reports false; 0 and "" pass.
func (s *Server) tokenPlacementOK(w http.ResponseWriter, r *http.Request, folderID int64, orgID string) bool {
	if folderID < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder_id must be a folder's id, or 0 for none"})
		return false
	}
	if folderID > 0 {
		store, err := s.panelGroupStore()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return false
		}
		folders, err := store.ListPanelFolders()
		if err != nil {
			writeInternalError(w, err, "ListPanelFolders")
			return false
		}
		found := false
		for _, f := range folders {
			if f.ID == folderID {
				found = true
				break
			}
		}
		if !found {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder not found"})
			return false
		}
		if !s.panelFolderOpenTo(w, r, folderID) {
			return false
		}
	}
	if orgID != "" {
		org, err := s.db.GetOrganization(orgID)
		if err != nil {
			writeInternalError(w, err, "GetOrganization")
			return false
		}
		if org == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "organization not found"})
			return false
		}
	}
	return true
}
