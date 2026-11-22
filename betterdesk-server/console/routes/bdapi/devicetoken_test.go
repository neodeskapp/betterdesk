package bdapi_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestRegistrationEndsWithADeviceToken: a device an agent template admits
// gets its device token in the answer of its registration, and so does one
// that proves its key, new or known. The token passes the device routes;
// a registration that brings it gets no new one, also with a proof, and
// one without a proof gets none (L46, L45).
func TestRegistrationEndsWithADeviceToken(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"BD_REGISTER_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		template := strings.Repeat("k", 64)
		if _, err := w.DB.Exec(ctx, "INSERT INTO agent_templates (name, enrollment_token) VALUES ('kiosk', ?)", template); err != nil {
			t.Fatal(err)
		}
		register := func(device, body string, headers ...string) string {
			t.Helper()
			got := w.SignedOut().Do("POST", "/api/bd/register", body, append([]string{"X-Device-Id", device}, headers...)...)
			if got.Status != 200 {
				t.Fatalf("registration of %s: %d %s", device, got.Status, got.Body)
			}
			var answer struct {
				DeviceToken string `json:"device_token"`
			}
			if err := json.Unmarshal([]byte(got.Body), &answer); err != nil {
				t.Fatal(err)
			}
			return answer.DeviceToken
		}
		proves := func(token, device string) bool {
			got, err := w.Server.Deps().Authn.RequireDeviceToken(ctx, "Bearer "+token)
			return err == nil && got.DeviceID == device
		}

		byTemplate := register("TPLDEV1", `{"enrollment_token":"`+template+`"}`)
		if byTemplate == "" || !proves(byTemplate, "TPLDEV1") {
			t.Fatalf("device admitted by a template: token %q, want one that proves TPLDEV1", byTemplate)
		}
		if again := register("TPLDEV1", `{}`, "Authorization", "Bearer "+byTemplate); again != "" {
			t.Error("a registration that brings its device token got a new one")
		}
		if none := register("TPLDEV1", `{}`); none != "" || !proves(byTemplate, "TPLDEV1") {
			t.Errorf("a registration without a proof: token %q, earlier token proves %v; want none and true", none, proves(byTemplate, "TPLDEV1"))
		}

		public, private, _ := ed25519.GenerateKey(nil)
		key := base64.StdEncoding.EncodeToString(public)
		signed := func(device, nonce string) []string {
			timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
			payload := strings.Join([]string{"bd-enrollment-v1", "POST", "/api/bd/register", device, key, timestamp, nonce}, "\n")
			return []string{"X-BD-Enrollment-Timestamp", timestamp, "X-BD-Enrollment-Nonce", nonce,
				"X-BD-Enrollment-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(payload)))}
		}
		body := `{"public_key":"` + key + `","uuid":"uuid-proof"}`
		byProof := register("PRFDEV1", body, signed("PRFDEV1", "n-1")...)
		if byProof == "" || !proves(byProof, "PRFDEV1") {
			t.Fatalf("device admitted by the proof of its key: token %q, want one that proves PRFDEV1", byProof)
		}
		renewed := register("PRFDEV1", body, signed("PRFDEV1", "n-2")...)
		if renewed == "" || renewed == byProof || proves(byProof, "PRFDEV1") || !proves(renewed, "PRFDEV1") {
			t.Errorf("known device with a proof and no token: new %q; want a new token that ends the old one", renewed)
		}
		if kept := register("PRFDEV1", body, append(signed("PRFDEV1", "n-3"), "Authorization", "Bearer "+renewed)...); kept != "" || !proves(renewed, "PRFDEV1") {
			t.Errorf("known device with a proof and its device token: new %q; want none, and its token kept", kept)
		}
	})
}
