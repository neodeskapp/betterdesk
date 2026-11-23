package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deviceKey(t *testing.T) (string, []byte) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), pub
}

// rejoinWithKey redeems a join code for the reset lab computer with key.
func rejoinWithKey(t *testing.T, srv *Server, code, key string) (int, onboardAnswer) {
	t.Helper()
	return onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
		"code": code, "kind": "join",
		"device": map[string]any{"device_id": "289485454", "uuid": "machine-1", "hostname": "ndlab-win11", "platform": "windows", "public_key": key},
	})
}

// TestAResetComputerRejoinsOnceTheNewKeyIsApproved: a computer whose ID is
// enrolled with another key is refused with what to do, the invite is not
// spent, its new key waits for an administrator, and once approved the
// same invite lets it in (L334).
func TestAResetComputerRejoinsOnceTheNewKeyIsApproved(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		oldKey, _ := deviceKey(t)
		newKey, newPK := deviceKey(t)
		if status, out := rejoinWithKey(t, srv, makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code, oldKey); status != http.StatusOK {
			t.Fatalf("first join: %d %+v", status, out)
		}
		token, err := srv.issueEnrollmentDeviceToken("289485454")
		if err != nil {
			t.Fatal(err)
		}

		// Reset: the same machine and ID, a new key, a fresh invite
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code
		status, out := rejoinWithKey(t, srv, code, newKey)
		if status != http.StatusForbidden || out.Error != "device_refused" ||
			out.Message != "This computer's ID is already enrolled with another key. Ask your administrator to approve the new key or to delete the device in the console." {
			t.Fatalf("rejoin with a new key: %d %+v", status, out)
		}
		if status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/lookup", map[string]any{"code": code}); status != http.StatusOK || out.Record.UsesLeft != 1 {
			t.Fatalf("the refusal spent the invite: %d %+v", status, out)
		}
		status, raw := asConsole(t, srv, http.MethodGet, "/api/onboard/devices/289485454/key-request", "")
		if status != http.StatusOK || !strings.Contains(raw, `"hostname":"ndlab-win11"`) || !strings.Contains(raw, `"fingerprint":"`+keyFingerprint(newPK)+`"`) {
			t.Fatalf("key request: %d %s", status, raw)
		}

		if status, raw := asConsole(t, srv, http.MethodPost, "/api/onboard/devices/289485454/key-request/approve", `{"actor":"admin"}`); status != http.StatusOK {
			t.Fatalf("approve: %d %s", status, raw)
		}
		if bound, err := srv.loadBdMgmtPublicKey("289485454"); err != nil || !bytes.Equal(bound, newPK) {
			t.Errorf("the bound key is not the new one: %v", err)
		}
		if p, _ := srv.db.GetPeer("289485454"); p == nil || !bytes.Equal(p.PK, newPK) {
			t.Errorf("the signal server's key is not the new one: %+v", p)
		}
		if valid, _ := srv.db.ValidateToken(hashToken(token)); valid != nil {
			t.Error("the old key's device token still works")
		}
		if req := srv.loadDeviceKeyRequest("289485454"); req != nil {
			t.Error("the approved request is still there")
		}

		if status, out := rejoinWithKey(t, srv, code, newKey); status != http.StatusOK || !out.Success {
			t.Fatalf("rejoin after the approval: %d %+v", status, out)
		}
	})
}

// TestANewKeyNeedsAnInviteAndAnApproval: without a usable invite no key
// request is made, and a key nobody approved does not get in by
// registering (L334).
func TestANewKeyNeedsAnInviteAndAnApproval(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		oldKey, _ := deviceKey(t)
		attackerKey, _ := deviceKey(t)
		if status, out := rejoinWithKey(t, srv, makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code, oldKey); status != http.StatusOK {
			t.Fatalf("first join: %d %+v", status, out)
		}
		if status, _ := rejoinWithKey(t, srv, "AAAA-AAAA", attackerKey); status != http.StatusNotFound {
			t.Errorf("a wrong code: %d", status)
		}
		if req := srv.loadDeviceKeyRequest("289485454"); req != nil {
			t.Errorf("a wrong code left a key request: %+v", req)
		}
		if status, _ := asConsole(t, srv, http.MethodPost, "/api/onboard/devices/289485454/key-request/approve", `{}`); status != http.StatusNotFound {
			t.Errorf("approve without a request: %d", status)
		}
		body, _ := json.Marshal(map[string]any{"device_id": "289485454", "uuid": "machine-1", "public_key": attackerKey})
		rec := httptest.NewRecorder()
		srv.handleDeviceRegister(rec, httptest.NewRequest(http.MethodPost, "/api/devices/register", bytes.NewReader(body)))
		if rec.Code != http.StatusForbidden {
			t.Errorf("register with another key: %d %s", rec.Code, rec.Body.String())
		}
		if bound, _ := srv.loadBdMgmtPublicKey("289485454"); base64.StdEncoding.EncodeToString(bound) != oldKey {
			t.Error("the bound key changed")
		}
	})
}
