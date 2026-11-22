package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

// TestANewDeviceTokenEndsTheEarlierOne: when the server issues a device
// a new token (a signed status poll without a working token, for example
// after the console's "Issue a new token" ended the old one), the token
// the device had ends; an operator's enrollment token bound to the device
// stays. A poll with the working token gets no new one and no complaint
// about a missing proof (L46, L45).
func TestANewDeviceTokenEndsTheEarlierOne(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "BD-REISSUE", UUID: "machine-reissue"}); err != nil {
		t.Fatal(err)
	}
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	if err := srv.storeBdMgmtPublicKey("BD-REISSUE", base64.StdEncoding.EncodeToString(publicKey)); err != nil {
		t.Fatal(err)
	}
	operator := &db.DeviceToken{Token: "operator-made-token", TokenHash: hashToken("operator-made-token"), PeerID: "BD-REISSUE",
		Status: db.TokenStatusActive, CreatedBy: "admin"}
	if err := database.CreateDeviceToken(operator); err != nil {
		t.Fatal(err)
	}
	poll := func(bearer string) EnrollmentResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/devices/register/status?device_id=BD-REISSUE", nil)
		applyHeaders(req, signedEnrollmentHeaders(t, privateKey, http.MethodGet, "/api/devices/register/status", "BD-REISSUE", publicKey))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		srv.handleDeviceRegisterStatus(rec, req)
		var resp EnrollmentResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
			t.Fatalf("status poll: %d %s", rec.Code, rec.Body.String())
		}
		return resp
	}
	live := func(token string) bool {
		row, err := database.ValidateToken(hashToken(token))
		return err == nil && row != nil && row.Status == db.TokenStatusActive
	}

	first := poll("").DeviceToken
	if first == "" {
		t.Fatal("the first signed poll got no device token")
	}
	if kept := poll(first); kept.DeviceToken != "" || kept.Message != "" {
		t.Errorf("poll with the working token: %+v, want no new token and no message", kept)
	}
	second := poll("").DeviceToken
	if second == "" || second == first {
		t.Fatalf("signed poll without the token: %q, want a new device token", second)
	}
	if live(first) || !live(second) {
		t.Errorf("after the new token: first live %v, second live %v; want false, true", live(first), live(second))
	}
	if !live("operator-made-token") {
		t.Error("the operator's enrollment token bound to the device ended with the new device token")
	}
}
