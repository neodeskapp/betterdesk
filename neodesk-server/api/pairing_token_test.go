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

	"github.com/unitronix/betterdesk-server/db"
)

// redeemAs posts a redeem request for a new code as a device, with the
// key proof of privateKey when it is set.
func redeemAs(t *testing.T, srv *Server, deviceID string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) EnrollmentResponse {
	t.Helper()
	body := map[string]any{"code": mintCode(t, srv, deviceID), "device_id": deviceID, "uuid": "uuid-" + deviceID}
	if publicKey != nil {
		body["public_key"] = base64.StdEncoding.EncodeToString(publicKey)
	}
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/redeem", bytes.NewReader(payload))
	req.RemoteAddr = "203.0.113.7:12345"
	if privateKey != nil {
		applyHeaders(req, signedEnrollmentHeaders(t, privateKey, http.MethodPost, "/api/pairing/redeem", deviceID, publicKey))
	}
	rec := httptest.NewRecorder()
	srv.handlePairingRedeem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem of %s: %d %s", deviceID, rec.Code, rec.Body.String())
	}
	var out struct {
		Enrollment EnrollmentResponse `json:"enrollment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Enrollment
}

// TestPairingEndsWithADeviceToken: a device that redeems a code and
// proves its key gets its device token in the answer, bound to it; one
// without a proof is enrolled without a token and told why. A known
// device that proves its key again gets a new token (L102, L46).
func TestPairingEndsWithADeviceToken(t *testing.T) {
	srv := newPairingTestServer(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	proved := redeemAs(t, srv, "100200400", publicKey, privateKey)
	if proved.Status != "approved" || proved.DeviceToken == "" {
		t.Fatalf("redeem with a key proof: %+v, want approved with a device token", proved)
	}
	row, err := srv.db.ValidateToken(hashToken(proved.DeviceToken))
	if err != nil || row == nil || row.PeerID != "100200400" || row.Status != db.TokenStatusActive {
		t.Fatalf("the paired device's token: %+v, %v; want it active and bound to 100200400", row, err)
	}

	unproved := redeemAs(t, srv, "100200401", nil, nil)
	if unproved.Status != "approved" || unproved.DeviceToken != "" || !strings.Contains(unproved.Message, "proof") {
		t.Fatalf("redeem without a key proof: %+v, want approved without a token and the reason", unproved)
	}

	again := redeemAs(t, srv, "100200400", publicKey, privateKey)
	if again.DeviceToken == "" || again.DeviceToken == proved.DeviceToken {
		t.Fatalf("known device redeeming with a proof and no token: %+v, want a new device token", again)
	}
}
