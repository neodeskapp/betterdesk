package pairing_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

const (
	signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	denied    = `{"success":false,"error":"Permission denied: enrollment.approve"}`
	codeID    = "0123456789abcdef"
	// made is what the API server answers for a new code.
	made = `{"code":"K7M2QX","id":"` + codeID + `","host":"desk.example.org","expires_at":"2026-11-10T08:10:00Z","expires_in":600}`
)

// answers makes the fake API server answer every call with one status
// and body.
func answers(status int, body string) func(call) (int, string) {
	return func(call) (int, string) { return status, body }
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	w := newWorld(t, nil)
	var got []string
	for _, route := range w.router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := "GET /api/panel/pairing/host,GET /api/panel/pairing/codes,POST /api/panel/pairing/codes,DELETE /api/panel/pairing/codes/{id},GET /signin/{role}"
	if strings.Join(got, ",") != want {
		t.Errorf("routes = %v, want %s", got, want)
	}
}

func TestEveryRouteNeedsASessionAndThePermission(t *testing.T) {
	w := newWorld(t, nil)
	w.api.answer = answers(200, `{"codes":[]}`)
	requests := []struct{ method, target, body string }{
		{"GET", "/api/panel/pairing/host", ""},
		{"GET", "/api/panel/pairing/codes", ""},
		{"POST", "/api/panel/pairing/codes", `{"label":"x"}`},
		{"DELETE", "/api/panel/pairing/codes/" + codeID, ""},
	}
	for _, req := range requests {
		name := req.method + " " + req.target
		want(t, name+" signed out", w.do("", req.method, req.target, req.body), 401, signedOut)
		for _, role := range []string{"guest_issuer", "viewer", "operator", "server_admin"} {
			want(t, name+" as "+role, w.do(role, req.method, req.target, req.body), 403, denied)
		}
	}
	if calls := w.api.seen(); len(calls) != 0 {
		t.Errorf("refused requests reached the API server: %v", calls)
	}
	if entries := w.audit(); len(entries) != 0 {
		t.Errorf("refused requests wrote audit entries: %v", entries)
	}
	for _, role := range []string{"global_admin", "super_admin", "admin"} {
		for _, req := range requests[:2] {
			if rec := w.do(role, req.method, req.target, req.body); rec.Code != 200 {
				t.Errorf("%s %s as %s: %d %s", req.method, req.target, role, rec.Code, rec.Body.String())
			}
		}
	}
	// The sign-in check comes first: an id that is not one is not looked
	// at for somebody who may not revoke
	want(t, "bad id signed out", w.do("", "DELETE", "/api/panel/pairing/codes/nope", ""), 401, signedOut)
	want(t, "bad id as viewer", w.do("viewer", "DELETE", "/api/panel/pairing/codes/nope", ""), 403, denied)
}

// The suggested address is the host clients are given: the public server
// ID setting, else PANEL_PUBLIC_HOST, else the host of the API address,
// as the spec tree's route answers it (upstream 3.5.264 resolves its
// pairing page's host this way). An API address on loopback gives way to
// the request's host (BUGS SI2).
func TestHostIsTheClientFacingHost(t *testing.T) {
	w := newWorld(t, nil)
	want(t, "configured API address on loopback", w.do("admin", "GET", "/api/panel/pairing/host", ""), 200, `{"success":true,"host":"example.com"}`)
	w.env["PANEL_PUBLIC_HOST"] = "Desk.Example.org:5443"
	want(t, "PANEL_PUBLIC_HOST", w.do("admin", "GET", "/api/panel/pairing/host", ""), 200, `{"success":true,"host":"Desk.Example.org"}`)
	w.env["PUBLIC_SERVER_ID"] = "id.example.org"
	want(t, "public server ID", w.do("admin", "GET", "/api/panel/pairing/host", ""), 200, `{"success":true,"host":"id.example.org"}`)
	if calls := w.api.seen(); len(calls) != 0 {
		t.Errorf("the host route called the API server: %v", calls)
	}
}

func TestList(t *testing.T) {
	w := newWorld(t, nil)
	codes := `[{"id":"` + codeID + `","label":"Front desk","created_at":"2026-11-10T08:00:00Z","expires_at":"2026-11-10T08:10:00Z","created_by":"apikey:console-bridge"}]`
	w.api.answer = answers(200, `{"codes":`+codes+`}`)
	want(t, "codes", w.do("admin", "GET", "/api/panel/pairing/codes", ""), 200, `{"success":true,"codes":`+codes+`}`)
	if calls := w.api.seen(); len(calls) != 1 || calls[0].method != "GET" || calls[0].path != "/api/pairing/codes" {
		t.Errorf("calls = %v", calls)
	}

	for _, body := range []string{`{"codes":[]}`, `{"codes":null}`, `{}`, `{"codes":0}`, `{"codes":""}`, `[]`, `"text"`, `null`} {
		w.api.answer = answers(200, body)
		want(t, "server says "+body, w.do("admin", "GET", "/api/panel/pairing/codes", ""), 200, `{"success":true,"codes":[]}`)
	}
	// What the server sends under "codes" is handed on as it is
	w.api.answer = answers(200, `{"codes":{"a":1}}`)
	want(t, "an object", w.do("admin", "GET", "/api/panel/pairing/codes", ""), 200, `{"success":true,"codes":{"a":1}}`)

	for _, status := range []int{400, 401, 404, 500, 503} {
		w.api.answer = answers(status, `{"error":"no"}`)
		want(t, "server answers "+itoa(status), w.do("admin", "GET", "/api/panel/pairing/codes", ""), 502,
			`{"success":false,"error":"Request failed with status code `+itoa(status)+`"}`)
	}
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestCreate(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		w.api.answer = answers(201, made)
		rec := w.do("global_admin", "POST", "/api/panel/pairing/codes", `{"label":"  Front desk \n"}`)
		want(t, "create", rec, 201,
			`{"success":true,"code":"K7M2QX","id":"`+codeID+`","host":"desk.example.org","label":"Front desk","expires_at":"2026-11-10T08:10:00Z","expires_in":600}`)
		calls := w.api.seen()
		if len(calls) != 1 || calls[0].method != "POST" || calls[0].path != "/api/pairing/codes" || calls[0].body != `{"label":"Front desk"}` {
			t.Errorf("calls = %v", calls)
		}
		entries := w.audit()
		wantEntry := entry{userID: roleIDs["global_admin"], action: "pairing_code_created", details: "Created pairing code " + codeID + " (Front desk)", ip: "203.0.113.7"}
		if len(entries) != 1 || entries[0] != wantEntry {
			t.Errorf("audit = %+v\nwant %+v", entries, wantEntry)
		}

		// No label: the entry names the code only
		want(t, "create without label", w.do("admin", "POST", "/api/panel/pairing/codes", `{}`), 201,
			`{"success":true,"code":"K7M2QX","id":"`+codeID+`","host":"desk.example.org","label":"","expires_at":"2026-11-10T08:10:00Z","expires_in":600}`)
		if calls := w.api.seen(); len(calls) != 1 || calls[0].body != `{"label":""}` {
			t.Errorf("calls = %v", calls)
		}
		entries = w.audit()
		wantEntry = entry{userID: roleIDs["admin"], action: "pairing_code_created", details: "Created pairing code " + codeID, ip: "203.0.113.7"}
		if len(entries) != 2 || entries[1] != wantEntry {
			t.Errorf("audit = %+v\nwant %+v", entries, wantEntry)
		}
		for _, e := range entries {
			if strings.Contains(e.details, "K7M2QX") {
				t.Errorf("the code is in the audit log: %q", e.details)
			}
		}
	})
}

func TestCreateLabels(t *testing.T) {
	w := newWorld(t, nil)
	w.api.answer = answers(201, `{"id":"`+codeID+`"}`)
	long := strings.Repeat("0123456789", 7)
	screens := strings.Repeat("\U0001F5A5", 40)
	cases := []struct {
		name, body string
		// sent is the label the API server gets, answered the label in the
		// route's answer as JSON.
		sent, answered string
	}{
		{"text", `{"label":"Front desk"}`, "Front desk", `"Front desk"`},
		{"white space around", `{"label":"\u00a0\ufeff\t Front desk \u2028\u2029\u3000\r\n"}`, "Front desk", `"Front desk"`},
		{"white space inside stays", `{"label":"a \u2028 b"}`, "a \u2028 b", "\"a \u2028 b\""},
		{"only white space", `{"label":" \n "}`, "", `""`},
		{"missing", `{}`, "", `""`},
		{"null", `{"label":null}`, "", `""`},
		{"empty", `{"label":""}`, "", `""`},
		{"false", `{"label":false}`, "", `""`},
		{"zero", `{"label":0}`, "", `""`},
		{"minus zero", `{"label":-0.0}`, "", `""`},
		{"true", `{"label":true}`, "true", `"true"`},
		{"number", `{"label":12.50}`, "12.5", `"12.5"`},
		{"large number", `{"label":1e21}`, "1e+21", `"1e+21"`},
		{"number beyond the range", `{"label":-1e999}`, "-Infinity", `"-Infinity"`},
		{"text zero", `{"label":"0"}`, "0", `"0"`},
		{"list", `{"label":["a",1,null,["b",[]],true,{"x":1}]}`, "a,1,,b,,true,[object Object]", `"a,1,,b,,true,[object Object]"`},
		{"empty list", `{"label":[]}`, "", `""`},
		{"object", `{"label":{"a":1}}`, "[object Object]", `"[object Object]"`},
		// The Node console stopped on this one (BUGS SI13)
		{"object with its own toString", `{"label":{"toString":1,"valueOf":2}}`, "[object Object]", `"[object Object]"`},
		{"quotes and controls", `{"label":"a\"b\\c\u0001d\te"}`, "a\"b\\c\u0001d\te", `"a\"b\\c\u0001d\te"`},
		{"64 characters stay", `{"label":"` + long[:64] + `"}`, long[:64], `"` + long[:64] + `"`},
		{"65 characters are cut", `{"label":"` + long[:65] + `"}`, long[:64], `"` + long[:64] + `"`},
		{"cut after trimming", `{"label":"   ` + long + `"}`, long[:64], `"` + long[:64] + `"`},
		{"32 signs of two units", `{"label":"` + screens + `"}`, screens[:32*4], `"` + screens[:32*4] + `"`},
		// 64 units end inside the 32nd sign: the answer has the half sign
		// as an escape, the server and the audit log get U+FFFD
		{"cut inside a sign", `{"label":"a` + screens + `"}`, "a" + screens[:31*4] + "\ufffd", `"a` + screens[:31*4] + `\ud83d"`},
		{"array body", `["label"]`, "", `""`},
	}
	for _, c := range cases {
		rec := w.do("admin", "POST", "/api/panel/pairing/codes", c.body)
		want(t, c.name, rec, 201, `{"success":true,"id":"`+codeID+`","host":"","label":`+c.answered+`}`)
		calls := w.api.seen()
		var got struct {
			Label string `json:"label"`
		}
		if len(calls) != 1 || json.Unmarshal([]byte(calls[0].body), &got) != nil || got.Label != c.sent {
			t.Errorf("%s: the server got %v, want the label %q", c.name, calls, c.sent)
		}
	}
	entries := w.audit()
	if len(entries) != len(cases) {
		t.Fatalf("%d audit entries for %d codes", len(entries), len(cases))
	}
	for i, c := range cases {
		details := "Created pairing code " + codeID
		if c.sent != "" {
			// The audit log writes line ends in a text as the two
			// characters \n (auditlog.RedactDetails)
			details += " (" + strings.ReplaceAll(c.sent, "\u2028", `\n`) + ")"
		}
		if entries[i].details != details {
			t.Errorf("%s: audit details %q, want %q", c.name, entries[i].details, details)
		}
	}
}

func TestCreateAnswerFollowsTheServer(t *testing.T) {
	w := newWorld(t, nil)
	for _, c := range []struct{ server, want string }{
		{`{}`, `{"success":true,"host":"","label":"x"}`},
		{`{"code":null,"id":null,"host":null,"expires_at":null,"expires_in":null}`,
			`{"success":true,"code":null,"id":null,"host":"","label":"x","expires_at":null,"expires_in":null}`},
		{`{"expires_in":600,"id":"` + codeID + `","code":"K7M2QX","host":"","extra":1}`,
			`{"success":true,"code":"K7M2QX","id":"` + codeID + `","host":"","label":"x","expires_in":600}`},
		{`[1,2]`, `{"success":true,"host":"","label":"x"}`},
		{`"text"`, `{"success":true,"host":"","label":"x"}`},
	} {
		w.api.answer = answers(200, c.server)
		want(t, "server says "+c.server, w.do("admin", "POST", "/api/panel/pairing/codes", `{"label":"x"}`), 201, c.want)
	}
	entries := w.audit()
	if len(entries) != 5 || entries[0].details != "Created pairing code  (x)" || entries[2].details != "Created pairing code "+codeID+" (x)" {
		t.Errorf("audit = %+v", entries)
	}
}

func TestCreateWhenTheServerRefuses(t *testing.T) {
	w := newWorld(t, nil)
	for _, status := range []int{400, 403, 404, 429, 500} {
		w.api.answer = answers(status, `{"error":"no"}`)
		rec := w.do("admin", "POST", "/api/panel/pairing/codes", `{"label":"x"}`)
		want(t, "server answers "+itoa(status), rec, 502, `{"success":false,"error":"Request failed with status code `+itoa(status)+`"}`)
	}
	if entries := w.audit(); len(entries) != 0 {
		t.Errorf("codes that were not made are in the audit log: %+v", entries)
	}
}

func TestACodeIsAnsweredWhenTheAuditLogFails(t *testing.T) {
	w := newWorld(t, nil)
	w.api.answer = answers(201, made)
	if _, err := w.db.Exec(context.Background(), "ALTER TABLE audit_log RENAME TO audit_log_gone"); err != nil {
		t.Fatal(err)
	}
	rec := w.do("admin", "POST", "/api/panel/pairing/codes", `{"label":"x"}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"code":"K7M2QX"`) {
		t.Errorf("create without an audit log: %d %s", rec.Code, rec.Body.String())
	}
	w.api.answer = answers(200, `{"success":true}`)
	want(t, "revoke without an audit log", w.do("admin", "DELETE", "/api/panel/pairing/codes/"+codeID, ""), 200, `{"success":true,"id":"`+codeID+`"}`)
}

func TestRevoke(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		w.api.answer = answers(200, `{"success":true}`)
		want(t, "revoke", w.do("global_admin", "DELETE", "/api/panel/pairing/codes/"+codeID, ""), 200, `{"success":true,"id":"`+codeID+`"}`)
		if calls := w.api.seen(); len(calls) != 1 || calls[0].method != "DELETE" || calls[0].path != "/api/pairing/codes/"+codeID {
			t.Errorf("calls = %v", calls)
		}
		entries := w.audit()
		wantEntry := entry{userID: roleIDs["global_admin"], action: "pairing_code_revoked", details: "Revoked pairing code " + codeID, ip: "203.0.113.7"}
		if len(entries) != 1 || entries[0] != wantEntry {
			t.Errorf("audit = %+v\nwant %+v", entries, wantEntry)
		}
	})
}

func TestRevokeIDs(t *testing.T) {
	w := newWorld(t, nil)
	w.api.answer = answers(200, `{"success":true}`)
	// id is the path segment as sent; clean is the id the server is asked
	// for, "" when the route refuses the id itself
	for _, c := range []struct{ id, clean string }{
		{"abcdef01", "abcdef01"},
		{"ABCDEF0123456789", "ABCDEF0123456789"},
		{strings.Repeat("a", 32), strings.Repeat("a", 32)},
		{"12345678", "12345678"},
		{"%20abcdef01%09", "abcdef01"},
		{"%C2%A0abcdef01%E2%80%A8", "abcdef01"},
		{"%61bcdef01", "abcdef01"},
		{"abcdef0", ""},
		{strings.Repeat("a", 33), ""},
		{"ghijklmn", ""},
		{"abcdef0g", ""},
		{"abcd%20ef01", ""},
		{"abcdef01%0Aabcdef01", ""},
		{"abcdef01.json", ""},
		{"%20", ""},
		{"abcdef01%2F..", ""},
	} {
		rec := w.do("admin", "DELETE", "/api/panel/pairing/codes/"+c.id, "")
		calls := w.api.seen()
		if c.clean == "" {
			want(t, c.id, rec, 400, `{"success":false,"error":"invalid_id"}`)
			if len(calls) != 0 {
				t.Errorf("%s: a refused id reached the server: %v", c.id, calls)
			}
			continue
		}
		want(t, c.id, rec, 200, `{"success":true,"id":"`+c.clean+`"}`)
		if len(calls) != 1 || calls[0].path != "/api/pairing/codes/"+c.clean {
			t.Errorf("%s: calls = %v", c.id, calls)
		}
	}
}

func TestRevokeWhenTheServerRefuses(t *testing.T) {
	w := newWorld(t, nil)
	for status, answered := range map[int]int{404: 404, 400: 502, 403: 502, 410: 502, 500: 502} {
		w.api.answer = answers(status, `{"error":"no"}`)
		rec := w.do("admin", "DELETE", "/api/panel/pairing/codes/"+codeID, "")
		want(t, "server answers "+itoa(status), rec, answered, `{"success":false,"error":"Request failed with status code `+itoa(status)+`"}`)
	}
	if entries := w.audit(); len(entries) != 0 {
		t.Errorf("codes that were not revoked are in the audit log: %+v", entries)
	}
}
