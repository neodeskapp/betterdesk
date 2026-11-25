package onboard_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// labKey is a computer's key pair.
type labKey struct {
	b64     string
	private ed25519.PrivateKey
}

func newLabKey(t *testing.T) labKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return labKey{b64: base64.StdEncoding.EncodeToString(pub), private: priv}
}

// proofHeaders are the headers a client sends with a join it signs: the
// nd-enrollment-v1 proof over POST /api/onboard/join, the device ID and
// its key.
func proofHeaders(t *testing.T, key labKey, deviceID string) []string {
	t.Helper()
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339)
	payload := strings.Join([]string{"nd-enrollment-v1", "POST", "/api/onboard/join", deviceID, key.b64, timestamp, hex.EncodeToString(nonce)}, "\n")
	return []string{
		"X-ND-Enrollment-Timestamp", timestamp,
		"X-ND-Enrollment-Nonce", hex.EncodeToString(nonce),
		"X-ND-Enrollment-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(key.private, []byte(payload))),
	}
}

// TestAJoinUnderAnEnrolledIDNeedsItsKeyProof: a person with an invite who
// knows an enrolled computer's ID can't join under it without the
// computer's key, and the computer keeps its device token. The computer
// itself joins again with its proof.
func TestAJoinUnderAnEnrolledIDNeedsItsKeyProof(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		admin := w.browser("admin")
		invite := func() string {
			return str(admin.do(http.MethodPost, "/api/onboard/invites", `{"name":"Lab","lifetime_hours":24,"uses":1}`).json(t), "code")
		}
		key := newLabKey(t)
		body := func(code, publicKey string) string {
			return `{"code":"` + code + `","device_id":"289485454","uuid":"machine-1","hostname":"ndlab-win11","platform":"windows","public_key":"` + publicKey + `"}`
		}
		// A new computer joins without a proof, as older clients do
		first := w.app().do(http.MethodPost, "/api/onboard/join", body(invite(), key.b64))
		if first.status != http.StatusOK {
			t.Fatalf("first join: %d %s", first.status, first.body)
		}
		token := str(first.json(t), "device_token")

		code := invite()
		for name, res := range map[string]answer{
			"no key, no uuid":   w.app().do(http.MethodPost, "/api/onboard/join", `{"code":"`+code+`","device_id":"289485454"}`),
			"its key, no proof": w.app().do(http.MethodPost, "/api/onboard/join", body(code, key.b64)),
			"its key, another's proof": w.app().do(http.MethodPost, "/api/onboard/join", body(code, key.b64),
				proofHeaders(t, labKey{b64: key.b64, private: newLabKey(t).private}, "289485454")...),
		} {
			if res.status != http.StatusForbidden || !strings.Contains(res.body, "Update NeoDesk on this computer") {
				t.Errorf("%s: %d %s", name, res.status, res.body)
			}
		}
		if auth, err := w.deps.Authn.RequireDeviceToken(context.Background(), "Bearer "+token); err != nil || auth.DeviceID != "289485454" {
			t.Fatalf("a refused join ended the computer's device token: %v", err)
		}

		again := w.app().do(http.MethodPost, "/api/onboard/join", body(code, key.b64), proofHeaders(t, key, "289485454")...)
		if again.status != http.StatusOK || !token64.MatchString(str(again.json(t), "device_token")) {
			t.Fatalf("the computer's own signed join: %d %s", again.status, again.body)
		}
	})
}
