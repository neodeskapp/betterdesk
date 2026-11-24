package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A pairing code made with a console API token records the computer it let
// in, under the token, so the token's revoke dialog can name the computer
// for the admin to check. A code made without a token records nothing.
func TestAPairingCodeOfATokenRecordsTheComputerItLetIn(t *testing.T) {
	srv := newPairingTestServer(t)
	as := consoleBridge
	as.Actor, as.Token = "ann", "tokenaaaaaaa"
	for _, c := range []struct {
		who    Identity
		device string
	}{{as, "100200300"}, {consoleBridge, "100200301"}} {
		status, body := callAs(t, srv, c.who, http.MethodPost, "/api/pairing/codes", `{"label":"desk"}`)
		var out struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(body), &out); status != http.StatusCreated || err != nil || out.Code == "" {
			t.Fatalf("make a code: %d %s", status, body)
		}
		res := pairingPost(t, srv, srv.handlePairingRedeem, map[string]any{"code": out.Code, "device_id": c.device, "hostname": "desk"})
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"approved"`) {
			t.Fatalf("redeem: %d %s", res.Code, res.Body.String())
		}
	}
	rows, err := srv.db.ListConfigByPrefix("token_admission_")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != "token_admission_tokenaaaaaaa_100200300" || !strings.Contains(rows[0].Value, `"how":"pairing_code"`) {
		t.Errorf("recorded admissions: %+v", rows)
	}
}
