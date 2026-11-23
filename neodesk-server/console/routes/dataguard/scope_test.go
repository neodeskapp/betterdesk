package dataguard_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// devicePolicy is a policy as a device gets it.
type devicePolicy struct {
	ID         int             `json:"id"`
	Name       string          `json:"name"`
	PolicyType string          `json:"policy_type"`
	Action     string          `json:"action"`
	Scope      string          `json:"scope"`
	Rules      json.RawMessage `json:"rules"`
}

// TestDevicesGetWhatThePolicySays: a device gets the policies that apply
// to it, each with its transfer type, action and scope, which the page
// says devices block or log by; a policy for one device does not reach
// another, and one without a scope reaches every device (L413).
func TestDevicesGetWhatThePolicySays(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for _, tok := range []struct{ token, hash, device string }{
			{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155", "fake-alpha"},
			{"tok-device-b", "", "fake-beta"},
		} {
			hash := tok.hash
			if hash == "" {
				hash = sha256Hex(tok.token)
			}
			w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at, token_kind) VALUES (?, ?, 5, ?, '2999-01-01 00:00:00', 'device')",
				[]any{tok.token, hash, tok.device})
		}
		w.Exec("INSERT INTO dlp_policies (id, name, policy_type, action, scope, rules) VALUES (1, 'Block USB drives', 'usb', 'block', 'fake-beta', '[]')", nil)
		w.Exec("INSERT INTO dlp_policies (id, name, policy_type, action, scope, rules) VALUES (2, 'Log the clipboard', 'clipboard', 'log', '', '[]')", nil)
		w.Exec("INSERT INTO dlp_policies (id, name, policy_type, action, scope, rules) VALUES (3, 'Two devices', 'file_transfer', 'block', ' fake-gamma , fake-beta ', '[]')", nil)

		get := func(token string) map[int]devicePolicy {
			t.Helper()
			got := w.Do(replay.Request{Path: "/api/nd/dlp-policies", Headers: map[string]string{"Authorization": "Bearer " + token}})
			var list []devicePolicy
			if got.Status != 200 || json.Unmarshal([]byte(got.Body), &list) != nil {
				t.Fatalf("the policies of %s: %d %s", token, got.Status, got.Body)
			}
			out := map[int]devicePolicy{}
			for _, p := range list {
				out[p.ID] = p
			}
			return out
		}
		alpha, beta := get("tok-device-a"), get("tok-device-b")
		if _, ok := alpha[1]; ok {
			t.Errorf("the USB policy for fake-beta reached fake-alpha: %+v", alpha[1])
		}
		if _, ok := alpha[3]; ok {
			t.Errorf("the policy for fake-gamma and fake-beta reached fake-alpha: %+v", alpha[3])
		}
		if p, ok := beta[1]; !ok || p.PolicyType != "usb" || p.Action != "block" || p.Scope != "fake-beta" {
			t.Errorf("fake-beta's USB policy: %+v, want usb, block, fake-beta", p)
		}
		if p, ok := beta[3]; !ok || p.PolicyType != "file_transfer" || p.Action != "block" {
			t.Errorf("fake-beta's file transfer policy: %+v", p)
		}
		for name, got := range map[string]map[int]devicePolicy{"fake-alpha": alpha, "fake-beta": beta} {
			if p, ok := got[2]; !ok || p.PolicyType != "clipboard" || p.Action != "log" {
				t.Errorf("%s's clipboard policy for every device: %+v", name, p)
			}
		}
	})
}
