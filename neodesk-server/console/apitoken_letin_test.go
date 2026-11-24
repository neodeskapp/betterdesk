package console

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The token list names the computers a token let in (joined through one of
// its invites, or approved with it) so the revoke dialog can list them for
// the admin to check; the revoke lets none of them out. Computers let in
// without the token are not named.
func TestTheTokenListNamesTheComputersItLetIn(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) { testTheTokenListNamesTheComputersItLetIn(t, madeWorld(t, db)) })
}

func testTheTokenListNamesTheComputersItLetIn(t *testing.T, w *world) {
	ctx := context.Background()
	value, id := w.makeToken("super_admin", `{"name":"script","permissions":`+allAdmin+`,"read_only":false,"expires_in_days":1}`)
	tok, browser := w.bearer(value), w.agent().signIn("super_admin")
	invite := func(a *agent, name string) string {
		t.Helper()
		var res answer
		if a == tok {
			res = a.postJSON("/api/onboard/invites", `{"name":"`+name+`"}`)
		} else {
			res = a.post("/api/onboard/invites", `{"name":"`+name+`"}`)
		}
		code, _ := res.json(t)["code"].(string)
		if res.status != http.StatusCreated || code == "" {
			t.Fatalf("invite %s: %d %s", name, res.status, res.body)
		}
		return code
	}
	join := func(code, device, host string) {
		t.Helper()
		body := `{"code":"` + code + `","device_id":"` + device + `","uuid":"u-` + device + `","hostname":"` + host + `","platform":"Windows"}`
		if res := w.agent().postJSON("/api/onboard/join", body); res.status != http.StatusOK {
			t.Fatalf("join %s: %d %s", device, res.status, res.body)
		}
	}
	join(invite(tok, "reception"), "123456781", "reception")
	join(invite(browser, "office"), "123456782", "office")
	for _, device := range []string{"123456783", "123456784"} {
		pending := `{"device_id":"` + device + `","hostname":"pending-` + device + `"}`
		if _, err := w.db.Exec(ctx, `INSERT INTO server_config (key, value) VALUES (?, ?)`, "pending_device_"+device, pending); err != nil {
			t.Fatal(err)
		}
	}
	if res := tok.postJSON("/api/enrollment/approve/123456783", `{}`); res.status != http.StatusOK {
		t.Fatalf("approve with the token: %d %s", res.status, res.body)
	}
	if res := browser.post("/api/enrollment/approve/123456784", `{}`); res.status != http.StatusOK {
		t.Fatalf("approve in the browser: %d %s", res.status, res.body)
	}

	var list struct {
		Data struct {
			Tokens []struct {
				ID    string `json:"id"`
				LetIn []struct {
					DeviceID string `json:"device_id"`
					How      string `json:"how"`
				} `json:"let_in"`
			} `json:"tokens"`
		} `json:"data"`
	}
	res := browser.get("/api/panel/api-tokens")
	if err := json.Unmarshal([]byte(res.body), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tk := range list.Data.Tokens {
		if tk.ID == id {
			for _, in := range tk.LetIn {
				got[in.DeviceID] = in.How
			}
		}
	}
	if len(got) != 2 || got["123456781"] != "invite" || got["123456783"] != "approval" {
		t.Errorf("the computers the token let in: %v, want 123456781 by invite and 123456783 by approval\n%s", got, res.body)
	}
	if res := browser.do("DELETE", "/api/panel/api-tokens/"+id, "", "X-CSRF-Token", browser.csrf()); res.status != http.StatusOK {
		t.Fatalf("revoke: %d %s", res.status, res.body)
	}
	for _, device := range []string{"123456781", "123456783"} {
		var n int
		if err := w.db.QueryRow(ctx, `SELECT COUNT(*) FROM peers WHERE id = ?`, device).Scan(&n); err != nil || n != 1 {
			t.Errorf("computer %s left with the token: %d %v", device, n, err)
		}
	}
}
