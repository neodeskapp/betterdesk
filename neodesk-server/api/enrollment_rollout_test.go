package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// rolloutToken makes a rollout token through POST /api/tokens with this
// limit, 30 days, the folder and the organization, and returns its id and
// plain value.
func (f *placementFixture) rolloutToken(t *testing.T, limit int, folder int64, org string) (int64, string) {
	t.Helper()
	f.createdTokens++
	body := fmt.Sprintf(`{"name":"rollout %d","kind":"rollout","max_uses":%d,"expires_in":2592000,"folder_id":%d,"org_id":%q}`,
		f.createdTokens, limit, folder, org)
	status, raw := asConsole(t, f.srv, http.MethodPost, "/api/tokens", body)
	if status != http.StatusCreated {
		t.Fatalf("create rollout token: %d %s", status, raw)
	}
	var out DeviceTokenResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != db.TokenKindRollout || out.MaxUses != limit || out.ExpiresAt == nil {
		t.Fatalf("created token: %s", raw)
	}
	return out.ID, out.Token
}

// registerCode registers a new computer and returns the status and answer.
func (f *placementFixture) registerCode(t *testing.T, deviceID, token string) (int, EnrollmentResponse) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"device_id": deviceID, "uuid": "machine-" + deviceID, "hostname": "rollout-host", "platform": "windows", "token": token,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.srv.handleDeviceRegister(rec, req)
	var resp EnrollmentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func (f *placementFixture) tokenRow(t *testing.T, id int64) *db.DeviceToken {
	t.Helper()
	tok, err := f.srv.db.GetDeviceToken(id)
	if err != nil || tok == nil {
		t.Fatalf("token %d: %v %v", id, tok, err)
	}
	return tok
}

// A rollout token admits computers until its limit, in managed and locked
// mode: each lands in the token's folder and organization and gets a
// device token of its own, bound to it; the rollout token stays unbound
// and counts the computers. The next computer is refused with 403 and
// rollout_token_used_up.
func TestRolloutTokenAdmitsComputersUpToItsLimit(t *testing.T) {
	for _, mode := range []string{config.EnrollmentModeManaged, config.EnrollmentModeLocked} {
		t.Run(mode, func(t *testing.T) {
			placementStores(t, mode, func(t *testing.T, f *placementFixture) {
				id, token := f.rolloutToken(t, 3, f.lab, f.org)
				seen := map[string]bool{}
				for i := 1; i <= 3; i++ {
					device := fmt.Sprintf("ROLL%04d", i)
					code, resp := f.registerCode(t, device, token)
					if code != http.StatusOK || resp.Status != "approved" {
						t.Fatalf("computer %d: %d %+v", i, code, resp)
					}
					if resp.DeviceToken == "" || resp.DeviceToken == token || seen[resp.DeviceToken] {
						t.Fatalf("computer %d got device token %q, want its own", i, resp.DeviceToken)
					}
					seen[resp.DeviceToken] = true
					own, err := f.srv.db.ValidateToken(hashToken(resp.DeviceToken))
					if err != nil || own == nil || own.PeerID != device || own.IsRollout() {
						t.Errorf("computer %d: its device token is %+v, want one bound to %s", i, own, device)
					}
					if got := f.folderOf(t, device); got != f.lab {
						t.Errorf("computer %d: folder %d, want the token's %d", i, got, f.lab)
					}
					if got := f.orgOf(t, device); got != f.org {
						t.Errorf("computer %d: organization %q, want the token's %q", i, got, f.org)
					}
				}
				row := f.tokenRow(t, id)
				if row.UseCount != 3 || row.PeerID != "" || !row.IsRollout() {
					t.Errorf("rollout token after three computers: %+v", row)
				}
				code, resp := f.registerCode(t, "ROLL0004", token)
				if code != http.StatusForbidden || resp.Status != "rejected" || resp.Error != "rollout_token_used_up" || resp.DeviceToken != "" {
					t.Errorf("fourth computer: %d %+v, want 403 rollout_token_used_up", code, resp)
				}
				if p, _ := f.srv.db.GetPeer("ROLL0004"); p != nil {
					t.Error("the refused computer was enrolled")
				}
				if row := f.tokenRow(t, id); row.UseCount != 3 {
					t.Errorf("use count after the refusal: %d, want 3", row.UseCount)
				}
				// A computer that joined registers again: no place is taken
				if code, resp := f.registerCode(t, "ROLL0001", token); code != http.StatusOK || resp.Status != "approved" {
					t.Errorf("joined computer again: %d %+v", code, resp)
				}
			})
		})
	}
}

// Two registrations at once never take more places than the limit, on
// SQLite and PostgreSQL: of twelve computers racing for five places,
// exactly five join.
func TestRolloutTokenConcurrentJoinsStayWithinTheLimit(t *testing.T) {
	placementStores(t, config.EnrollmentModeLocked, func(t *testing.T, f *placementFixture) {
		id, token := f.rolloutToken(t, 5, f.office, "")
		const racers = 12
		codes := make([]int, racers)
		errs := make([]string, racers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				data, _ := json.Marshal(map[string]any{
					"device_id": fmt.Sprintf("RACE%04d", i), "uuid": fmt.Sprintf("machine-race-%d", i),
					"hostname": "race", "platform": "windows", "token": token,
				})
				req := httptest.NewRequest(http.MethodPost, "/api/devices/register", bytes.NewReader(data))
				rec := httptest.NewRecorder()
				f.srv.handleDeviceRegister(rec, req)
				var resp EnrollmentResponse
				_ = json.Unmarshal(rec.Body.Bytes(), &resp)
				codes[i], errs[i] = rec.Code, resp.Error
			}(i)
		}
		close(start)
		wg.Wait()
		joined := 0
		for i, code := range codes {
			switch {
			case code == http.StatusOK:
				joined++
			case code == http.StatusForbidden && errs[i] == "rollout_token_used_up":
			default:
				t.Errorf("racer %d: %d %q", i, code, errs[i])
			}
		}
		if joined != 5 {
			t.Errorf("%d computers joined, want exactly the limit of 5", joined)
		}
		if row := f.tokenRow(t, id); row.UseCount != 5 {
			t.Errorf("use count %d, want 5", row.UseCount)
		}
		folders, err := f.folderStore.ListPanelDeviceFolderAssignments()
		if err != nil {
			t.Fatal(err)
		}
		placed := 0
		for device, folder := range folders {
			if strings.HasPrefix(device, "RACE") && folder == f.office {
				placed++
			}
		}
		if placed != 5 {
			t.Errorf("%d racers in the token's folder, want 5", placed)
		}
	})
}

// An expired rollout token is refused with rollout_token_expired, and a
// revoked one with rollout_token_revoked; revoking leaves the computers
// that joined with their device tokens.
func TestRolloutTokenExpiryAndRevoke(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		expiring, expiringToken := f.rolloutToken(t, 10, 0, "")
		row := f.tokenRow(t, expiring)
		past := time.Now().Add(-time.Minute)
		row.ExpiresAt = &past
		if err := f.srv.db.UpdateDeviceToken(row); err != nil {
			t.Fatal(err)
		}
		if code, resp := f.registerCode(t, "EXPI0001", expiringToken); code != http.StatusForbidden || resp.Error != "rollout_token_expired" {
			t.Errorf("expired: %d %+v, want 403 rollout_token_expired", code, resp)
		}

		id, token := f.rolloutToken(t, 10, 0, "")
		code, first := f.registerCode(t, "REVO0001", token)
		if code != http.StatusOK || first.DeviceToken == "" {
			t.Fatalf("first computer: %d %+v", code, first)
		}
		if status, raw := asConsole(t, f.srv, http.MethodDelete, fmt.Sprintf("/api/tokens/%d", id), ""); status != http.StatusOK {
			t.Fatalf("revoke: %d %s", status, raw)
		}
		if code, resp := f.registerCode(t, "REVO0002", token); code != http.StatusForbidden || resp.Error != "rollout_token_revoked" {
			t.Errorf("after revoking: %d %+v, want 403 rollout_token_revoked", code, resp)
		}
		if !f.srv.hasBoundActiveDeviceToken("REVO0001", first.DeviceToken) {
			t.Error("revoking the rollout token ended the device token of a computer that joined")
		}
	})
}

// The rollout token is never a device's credential: not for a device
// self-service call, not to get a device token issued, not bindable, and
// a token of one computer keeps working as before.
func TestRolloutTokenIsNoDeviceCredential(t *testing.T) {
	placementStores(t, config.EnrollmentModeOpen, func(t *testing.T, f *placementFixture) {
		id, token := f.rolloutToken(t, 10, 0, "")
		if code, resp := f.registerCode(t, "CRED0001", token); code != http.StatusOK || resp.DeviceToken == token {
			t.Fatalf("join: %d %+v", code, resp)
		}
		if f.srv.hasBoundActiveDeviceToken("CRED0001", token) {
			t.Error("the rollout token passes as the device token of a computer that joined with it")
		}
		for _, requireBound := range []bool{false, true} {
			if f.srv.hasEnrollmentCredential("CRED0002", []string{token}, requireBound) {
				t.Errorf("the rollout token counts as an enrollment credential (bound=%v)", requireBound)
			}
		}
		if status, raw := asConsole(t, f.srv, http.MethodPost, fmt.Sprintf("/api/tokens/%d/bind", id), `{"peer_id":"CRED0003"}`); status != http.StatusConflict {
			t.Errorf("bind: %d %s, want 409", status, raw)
		}
		if row := f.tokenRow(t, id); row.PeerID != "" {
			t.Errorf("rollout token bound to %q", row.PeerID)
		}
	})
}

// What a rollout token needs: a limit from 1 to 100000 and an expiry, also
// when the limit is edited. A kind that doesn't exist is refused.
func TestRolloutTokenNeedsALimitAndAnExpiry(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		for name, body := range map[string]string{
			"no limit":         `{"name":"x","kind":"rollout","expires_in":3600}`,
			"limit 0":          `{"name":"x","kind":"rollout","max_uses":0,"expires_in":3600}`,
			"limit too high":   `{"name":"x","kind":"rollout","max_uses":100001,"expires_in":3600}`,
			"no expiry":        `{"name":"x","kind":"rollout","max_uses":10}`,
			"expiry too long":  `{"name":"x","kind":"rollout","max_uses":10,"expires_in":31536001}`,
			"unknown kind":     `{"name":"x","kind":"many","max_uses":10,"expires_in":3600}`,
			"negative expires": `{"name":"x","kind":"rollout","max_uses":10,"expires_in":-5}`,
		} {
			if status, raw := asConsole(t, f.srv, http.MethodPost, "/api/tokens", body); status != http.StatusBadRequest {
				t.Errorf("%s: %d %s, want 400", name, status, raw)
			}
		}
		id, _ := f.rolloutToken(t, 100000, 0, "")
		path := fmt.Sprintf("/api/tokens/%d", id)
		if status, raw := asConsole(t, f.srv, http.MethodPut, path, `{"max_uses":0}`); status != http.StatusBadRequest {
			t.Errorf("edit to no limit: %d %s, want 400", status, raw)
		}
		if status, raw := asConsole(t, f.srv, http.MethodPut, path, `{"max_uses":250}`); status != http.StatusOK || !strings.Contains(raw, `"kind":"rollout"`) {
			t.Errorf("edit the limit: %d %s", status, raw)
		}
		if row := f.tokenRow(t, id); row.MaxUses != 250 || !row.IsRollout() {
			t.Errorf("after the edit: %+v", row)
		}
		status, raw := asConsole(t, f.srv, http.MethodGet, "/api/tokens", "")
		if status != http.StatusOK || strings.Count(raw, `"kind":"rollout"`) != 1 {
			t.Errorf("list: %d %s", status, raw)
		}
	})
}

// A token of one computer is unchanged: no kind in its answer, it binds to
// the first computer, and a second computer is refused as before with
// enrollment_token_rejected.
func TestOneComputerTokenStaysAsItWas(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		status, raw := asConsole(t, f.srv, http.MethodPost, "/api/tokens", `{"name":"one","max_uses":5}`)
		if status != http.StatusCreated || strings.Contains(raw, `"kind"`) {
			t.Fatalf("create: %d %s", status, raw)
		}
		var made DeviceTokenResponse
		_ = json.Unmarshal([]byte(raw), &made)
		if code, resp := f.registerCode(t, "ONE00001", made.Token); code != http.StatusOK || resp.Status != "approved" {
			t.Fatalf("first computer: %d %+v", code, resp)
		}
		row := f.tokenRow(t, made.ID)
		if row.PeerID != "ONE00001" || row.Kind != db.TokenKindDevice || row.Status != db.TokenStatusActive {
			t.Errorf("token after the first computer: %+v", row)
		}
		if code, resp := f.registerCode(t, "ONE00002", made.Token); code != http.StatusForbidden || resp.Error != "enrollment_token_rejected" {
			t.Errorf("second computer: %d %+v, want 403 enrollment_token_rejected", code, resp)
		}
	})
}

// rustdesk --deploy --token takes places of a rollout token too: two
// computers, the token stays unbound and counts both.
func TestRolloutTokenWithDeploy(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		id, token := f.rolloutToken(t, 2, f.office, "")
		for i, device := range []string{"DEPROLL01", "DEPROLL02"} {
			if code, result := deploy(t, f.srv, token, device, newDeployMachine(byte(40+i))); code != http.StatusOK || result != deployOK {
				t.Fatalf("deploy %s = %d %q", device, code, result)
			}
			if got := f.folderOf(t, device); got != f.office {
				t.Errorf("%s: folder %d, want %d", device, got, f.office)
			}
		}
		if row := f.tokenRow(t, id); row.PeerID != "" || row.UseCount != 2 {
			t.Errorf("rollout token after two deploys: %+v", row)
		}
		if code, _ := deploy(t, f.srv, token, "DEPROLL03", newDeployMachine(42)); code != http.StatusUnauthorized {
			t.Errorf("third deploy: %d, want 401", code)
		}
	})
}
