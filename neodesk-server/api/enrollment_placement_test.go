package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// placementFixture is a server in the mode with two folders and an
// organization, on SQLite and on PostgreSQL.
type placementFixture struct {
	srv           *Server
	lab, office   int64
	org           string
	folderStore   db.PanelGroupStore
	createdTokens int
}

func placementStores(t *testing.T, mode string, test func(t *testing.T, f *placementFixture)) {
	storetest.Each(t, func(t *testing.T, sdb *store.DB) {
		database := storetest.GoServer(t, sdb)
		cfg := config.DefaultConfig()
		cfg.EnrollmentMode = mode
		srv := New(cfg, database, peer.NewMap(), nil, "test")
		folders, ok := database.(db.PanelGroupStore)
		if !ok {
			t.Fatal("the database keeps no folders")
		}
		lab, office := &db.Folder{Name: "Lab"}, &db.Folder{Name: "Front office"}
		for _, f := range []*db.Folder{lab, office} {
			if err := folders.CreatePanelFolder(f); err != nil {
				t.Fatal(err)
			}
		}
		org := &db.Organization{ID: "org-receiver", Name: "Receivers", Slug: "receivers", Settings: "{}"}
		if err := database.CreateOrganization(org); err != nil {
			t.Fatal(err)
		}
		test(t, &placementFixture{srv: srv, lab: lab.ID, office: office.ID, org: org.ID, folderStore: folders})
	})
}

// token makes an enrollment token through POST /api/tokens with the body's
// folder_id and org_id and returns its plain value.
func (f *placementFixture) token(t *testing.T, folder int64, org string) string {
	t.Helper()
	f.createdTokens++
	body := fmt.Sprintf(`{"name":"placement %d","max_uses":5,"folder_id":%d,"org_id":%q}`, f.createdTokens, folder, org)
	status, raw := asConsole(t, f.srv, http.MethodPost, "/api/tokens", body)
	if status != http.StatusCreated {
		t.Fatalf("create token: %d %s", status, raw)
	}
	var out DeviceTokenResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.FolderID != folder || out.OrgID != org {
		t.Fatalf("created token names folder %d and organization %q, want %d and %q", out.FolderID, out.OrgID, folder, org)
	}
	return out.Token
}

func (f *placementFixture) register(t *testing.T, payload map[string]any) EnrollmentResponse {
	t.Helper()
	payload["uuid"] = "machine-" + payload["device_id"].(string)
	payload["hostname"] = "placement-host"
	payload["platform"] = "windows"
	data, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.srv.handleDeviceRegister(rec, req)
	var resp EnrollmentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp
}

func (f *placementFixture) folderOf(t *testing.T, deviceID string) int64 {
	t.Helper()
	all, err := f.folderStore.ListPanelDeviceFolderAssignments()
	if err != nil {
		t.Fatal(err)
	}
	return all[deviceID]
}

func (f *placementFixture) orgOf(t *testing.T, deviceID string) string {
	t.Helper()
	org, err := f.srv.db.GetDeviceOrgID(deviceID)
	if err != nil {
		t.Fatal(err)
	}
	return org
}

// A device that enrolls with a token lands in the token's folder and
// organization, in every mode, and the organization's policies are the
// device's policies.
func TestEnrollmentTokenPutsTheDeviceIntoItsFolderAndOrganization(t *testing.T) {
	for _, mode := range []string{config.EnrollmentModeOpen, config.EnrollmentModeManaged, config.EnrollmentModeLocked} {
		t.Run(mode, func(t *testing.T) {
			placementStores(t, mode, func(t *testing.T, f *placementFixture) {
				if err := f.srv.db.SetOrgSetting(f.org, "policy_security", `{"allow_uninstall":false}`); err != nil {
					t.Fatal(err)
				}
				token := f.token(t, f.lab, f.org)
				resp := f.register(t, map[string]any{"device_id": "PLACE0001", "token": token})
				if resp.Status != "approved" {
					t.Fatalf("register: %+v", resp)
				}
				if got := f.folderOf(t, "PLACE0001"); got != f.lab {
					t.Errorf("folder = %d, want the token's %d", got, f.lab)
				}
				if got := f.orgOf(t, "PLACE0001"); got != f.org {
					t.Errorf("organization = %q, want the token's %q", got, f.org)
				}
				status, raw := asConsole(t, f.srv, http.MethodGet, "/api/peers/PLACE0001/policy", "")
				if status != http.StatusOK || !strings.Contains(raw, `"allow_uninstall":false`) {
					t.Errorf("policy of the device = %d %s, want the organization's", status, raw)
				}
			})
		})
	}
}

// A group in the registration counts only next to a valid token; the
// token's own folder wins over it.
func TestEnrollmentGroupNeedsATokenAndLosesToTheTokensFolder(t *testing.T) {
	placementStores(t, config.EnrollmentModeOpen, func(t *testing.T, f *placementFixture) {
		// No token: open mode takes the device, but not into the group
		resp := f.register(t, map[string]any{"device_id": "PLACE0002", "group": "Lab"})
		if resp.Status != "approved" {
			t.Fatalf("register without a token: %+v", resp)
		}
		if got := f.folderOf(t, "PLACE0002"); got != 0 {
			t.Errorf("group without a token put the device into folder %d", got)
		}
		// A token that does not work: no group either
		f.register(t, map[string]any{"device_id": "PLACE0003", "group": "Lab", "token": "no-such-token-0123456789abcdef"})
		if got := f.folderOf(t, "PLACE0003"); got != 0 {
			t.Errorf("group with a wrong token put the device into folder %d", got)
		}

		// A token without a folder: the group counts, by name in any case or
		// by id. A token admits one computer, so each gets its own.
		f.register(t, map[string]any{"device_id": "PLACE0004", "group": "front OFFICE", "token": f.token(t, 0, "")})
		if got := f.folderOf(t, "PLACE0004"); got != f.office {
			t.Errorf("group by name: folder = %d, want %d", got, f.office)
		}
		f.register(t, map[string]any{"device_id": "PLACE0005", "group": fmt.Sprint(f.lab), "token": f.token(t, 0, "")})
		if got := f.folderOf(t, "PLACE0005"); got != f.lab {
			t.Errorf("group by id: folder = %d, want %d", got, f.lab)
		}

		// A token with a folder: its folder wins
		withFolder := f.token(t, f.lab, "")
		f.register(t, map[string]any{"device_id": "PLACE0006", "group": "Front office", "token": withFolder})
		if got := f.folderOf(t, "PLACE0006"); got != f.lab {
			t.Errorf("token folder against group: folder = %d, want the token's %d", got, f.lab)
		}
	})
}

// A group without a token in managed mode leaves the device waiting for
// approval, in no folder.
func TestEnrollmentGroupWithoutTokenInManagedModeWaits(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		resp := f.register(t, map[string]any{"device_id": "PLACE0007", "group": "Lab"})
		if resp.Status != "pending" {
			t.Fatalf("register: %+v, want pending", resp)
		}
		if got := f.folderOf(t, "PLACE0007"); got != 0 {
			t.Errorf("folder = %d, want none", got)
		}
	})
}

// A token can't name a folder or an organization that does not exist,
// and an edit can change and clear both.
func TestEnrollmentTokenPlacementIsChecked(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		for name, body := range map[string]string{
			"unknown folder":       `{"name":"x","max_uses":1,"folder_id":99999}`,
			"negative folder":      `{"name":"x","max_uses":1,"folder_id":-1}`,
			"unknown organization": `{"name":"x","max_uses":1,"org_id":"no-such-org"}`,
		} {
			if status, raw := asConsole(t, f.srv, http.MethodPost, "/api/tokens", body); status != http.StatusBadRequest {
				t.Errorf("%s: %d %s, want 400", name, status, raw)
			}
		}
		f.token(t, f.lab, f.org)
		tokens, err := f.srv.db.ListDeviceTokens(false)
		if err != nil || len(tokens) != 1 {
			t.Fatalf("tokens: %v %v", tokens, err)
		}
		path := fmt.Sprintf("/api/tokens/%d", tokens[0].ID)
		if status, raw := asConsole(t, f.srv, http.MethodPut, path, fmt.Sprintf(`{"folder_id":%d}`, f.office)); status != http.StatusOK {
			t.Fatalf("move to the office: %d %s", status, raw)
		}
		got, _ := f.srv.db.GetDeviceToken(tokens[0].ID)
		if got.FolderID != f.office || got.OrgID != f.org {
			t.Errorf("after the edit: folder %d organization %q, want %d and %q", got.FolderID, got.OrgID, f.office, f.org)
		}
		if status, _ := asConsole(t, f.srv, http.MethodPut, path, `{"folder_id":99999}`); status != http.StatusBadRequest {
			t.Errorf("edit to an unknown folder: %d, want 400", status)
		}
		if status, raw := asConsole(t, f.srv, http.MethodPut, path, `{"folder_id":0,"org_id":""}`); status != http.StatusOK {
			t.Fatalf("clear: %d %s", status, raw)
		}
		got, _ = f.srv.db.GetDeviceToken(tokens[0].ID)
		if got.FolderID != 0 || got.OrgID != "" {
			t.Errorf("after clearing: folder %d organization %q, want none", got.FolderID, got.OrgID)
		}
	})
}

// rustdesk --deploy --token puts the device where the token says too.
func TestDeployTokenPutsTheDeviceIntoItsFolderAndOrganization(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		token := f.token(t, f.office, f.org)
		if code, result := deploy(t, f.srv, token, "DEPLOY0001", newDeployMachine(21)); code != http.StatusOK || result != deployOK {
			t.Fatalf("deploy = %d %q, want 200 OK", code, result)
		}
		if got := f.folderOf(t, "DEPLOY0001"); got != f.office {
			t.Errorf("folder = %d, want the token's %d", got, f.office)
		}
		if got := f.orgOf(t, "DEPLOY0001"); got != f.org {
			t.Errorf("organization = %q, want the token's %q", got, f.org)
		}
	})
}
