//go:build rdflow

package rustdeskapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The flow test speaks the RustDesk client API the way a desktop client
// does, with access tokens: sign-in for each role, the account behind a
// token, address books written and read back, tags, groups, users, peers
// and devices under each role's scope, strategies, a second sign-in of the
// same client, sign-out, the two-factor steps and the lockout after ten
// wrong passwords, and the audit entries all of that leaves.
//
// The contract suite cannot do this: a case that signs in would write the
// password into its golden, and a token into the header notes. So this
// test keeps its own recording per database, testdata/flow_sqlite.json and
// testdata/flow_postgres.json, made from the Node console before the
// routes moved. A recording holds the method and path of each request and
// the answer with every token, secret and random id masked; no request
// header and no request body goes into it. It runs against a fresh test
// instance:
//
//	BD_URL=http://bdgo-rd2:5000 RDFLOW_DB=sqlite go test -tags rdflow -count=1 -run TestClientAPIFlow ./console/routes/rustdeskapi/
//
// RDFLOW_RECORD=1 writes the recording instead of comparing, and
// CONTRACT_REQUIRE_GO=1 fails when the Node console answered a route of
// the client API.

const (
	flowUser     = "rd_flow_tmp"
	proUser      = "rd_flow_pro_tmp"
	flowPassword = "Rd-Flow-1!throwaway"
)

// flowStep is one request of the flow in the recording.
type flowStep struct {
	Name    string                `json:"name"`
	Request string                `json:"request"`
	Answer  contract.GoldenAnswer `json:"answer"`
}

type flow struct {
	t        *testing.T
	client   *contract.Client
	device   *contract.Session
	steps    []flowStep
	got      []contract.Answer
	asked    map[string]bool
	password string
}

// request is one call of a client.
type request struct {
	method, path, token string
	body                any
	// unordered compares the list "data" of the answer as a set: the
	// device layer orders devices by their last contact, which the
	// heartbeats of the test instance keep changing.
	unordered bool
	volatile  []string
}

// call sends a request as a desktop client: no cookies, no CSRF token, the
// token as Authorization: Bearer.
func (f *flow) call(name string, req request) contract.Answer {
	f.t.Helper()
	r := contract.Request{Method: req.method, Path: req.path, NoCookies: true, NoCSRF: true}
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	if req.token != "" {
		r.Header = http.Header{"Authorization": {"Bearer " + req.token}}
	}
	if req.body != nil {
		raw, err := json.Marshal(req.body)
		if err != nil {
			f.t.Fatalf("%s: encode the body: %v", name, err)
		}
		r.Body, r.ContentType = raw, "application/json"
	}
	answer := f.device.Do(r)
	if answer.Failed != "" {
		f.t.Fatalf("%s: no answer (%s)", name, answer.Failed)
	}
	path := strings.SplitN(req.path, "?", 2)[0]
	f.asked[r.Method+" "+path] = true
	if req.unordered {
		answer = sortData(f.t, answer)
	}
	f.keep(name, r.Method+" "+req.path, answer, req.volatile...)
	return answer
}

// keep adds an answer to the recording. volatile lists paths of the body
// whose values change from run to run; a path may carry what is still
// checked there after a space: "integer", or a pattern for the class of
// the string.
func (f *flow) keep(name, request string, answer contract.Answer, volatile ...string) {
	f.t.Helper()
	marks := make([]contract.VolatileMark, 0, len(volatile))
	paths := make([]string, 0, len(volatile))
	for _, text := range volatile {
		p, check, _ := strings.Cut(text, " ")
		mark := contract.VolatileMark{Path: p}
		switch {
		case check == "integer":
			mark.Integer = true
		case check != "":
			mark.Pattern = check
		}
		marks = append(marks, mark)
		paths = append(paths, p)
	}
	golden := contract.GoldenAnswer{
		Status: answer.Status, Redirects: answer.Redirects, Headers: answer.Headers, Body: answer.Body, Volatile: marks,
	}
	if answer.Body.Kind == contract.BodyJSON && len(paths) > 0 {
		masked, err := maskPaths(answer.Body.JSON, paths)
		if err != nil {
			f.t.Fatalf("%s: %v", name, err)
		}
		golden.Body.JSON = masked
	}
	f.steps = append(f.steps, flowStep{Name: name, Request: request, Answer: golden})
	f.got = append(f.got, answer)
}

// signIn signs a client in and returns its token.
func (f *flow) signIn(name, username, password, id, uuid string) string {
	f.t.Helper()
	answer := f.call(name, request{
		method: "POST", path: "/api/login",
		body:     map[string]any{"username": username, "password": password, "id": id, "uuid": uuid, "autoLogin": true, "type": "account"},
		volatile: []string{tokenMark},
	})
	token, _ := field(f.t, answer, "access_token").(string)
	if token == "" {
		f.t.Fatalf("%s: no access token (status %d)", name, answer.Status)
	}
	return token
}

const (
	tokenMark  = `access_token ^[9a]{64}$`
	secretMark = `secret ^[9a]{32}$`
	guidMark   = `^[9a]{8}-[9a]{4}-[9a]{4}-[9a]{4}-[9a]{12}$`
	timeMark   = `^9999-99-99[ A]99:99:99(\.999A)?$`
)

func TestClientAPIFlow(t *testing.T) {
	settings, err := contract.LoadSettings(os.Getenv)
	if err != nil {
		t.Skipf("no console to test: %v", err)
	}
	dbName := os.Getenv("RDFLOW_DB")
	if dbName != "sqlite" && dbName != "postgres" {
		t.Fatalf("RDFLOW_DB must say which database the instance runs on: sqlite or postgres")
	}
	file := "testdata/flow_" + dbName + ".json"
	client, err := contract.NewClient(settings)
	if err != nil {
		t.Fatalf("%v", err)
	}
	raw, err := os.ReadFile(settings.PasswordFile)
	if err != nil {
		t.Fatalf("read the test accounts' password: %v", err)
	}
	device := client.NewSession(contract.SignedOut)
	f := &flow{t: t, client: client, device: device, asked: map[string]bool{}, password: strings.TrimSpace(string(raw))}

	admin := client.NewSession("admin")
	if _, err := admin.SignIn(); err != nil || !admin.SignedIn() {
		t.Fatalf("sign in as admin: %v", err)
	}
	baseline := lastAuditID(t, admin)

	// An account of the flow's own, with two-factor sign-in, for the
	// second step and the lockout.
	created := admin.Do(jsonRequest(t, http.MethodPost, "/api/users", map[string]any{"username": flowUser, "password": flowPassword, "role": "operator"}))
	if created.Status != http.StatusOK {
		t.Fatalf("create the account %s: status %d (a used instance still has it; start a fresh one)", flowUser, created.Status)
	}
	accountID := fmt.Sprint(field(t, created, "data", "id"))
	defer func() {
		if gone := admin.Do(contract.Request{Method: http.MethodDelete, Path: "/api/users/" + accountID}); gone.Status != http.StatusOK {
			t.Errorf("delete the account %s: status %d", flowUser, gone.Status)
		}
	}()
	secret := enableTOTP(t, client)
	// The account that becomes pro after it signed in.
	createdPro := admin.Do(jsonRequest(t, http.MethodPost, "/api/users", map[string]any{"username": proUser, "password": flowPassword, "role": "operator"}))
	if createdPro.Status != http.StatusOK {
		t.Fatalf("create the account %s: status %d", proUser, createdPro.Status)
	}
	proID := fmt.Sprint(field(t, createdPro, "data", "id"))
	defer func() {
		if gone := admin.Do(contract.Request{Method: http.MethodDelete, Path: "/api/users/" + proID}); gone.Status != http.StatusOK {
			t.Errorf("delete the account %s: status %d", proUser, gone.Status)
		}
	}()

	// A device group open to the admin only: the vip device leaves the
	// scope of everybody else.
	private := admin.Do(jsonRequest(t, http.MethodPost, "/api/device-groups", map[string]any{
		"name": "rd-flow-private", "source_type": "tag", "tag_filter": "vip", "allowed_users": []string{"e2e_admin"},
	}))
	if private.Status != http.StatusOK {
		t.Fatalf("create the private device group: status %d", private.Status)
	}
	privateGUID := fmt.Sprint(field(t, private, "data", "guid"))
	defer func() {
		if gone := admin.Do(contract.Request{Method: http.MethodDelete, Path: "/api/device-groups/" + privateGUID}); gone.Status != http.StatusOK {
			t.Errorf("delete the private device group: status %d", gone.Status)
		}
	}()

	pw := f.password
	groupMarks := []string{"data[*].guid ^([9a]{8}-[9a]{4}-[9a]{4}-[9a]{4}-[9a]{12}|aaaaaa_9+)$"}

	// A viewer: may see devices, may not change them.
	viewer := f.signIn("viewer-sign-in", "e2e_viewer", pw, "rdflow-viewer", "rdflow-uuid-viewer")
	f.call("viewer-current-user", request{path: "/api/currentUser", token: viewer})
	f.call("viewer-current-user-post", request{method: "POST", path: "/api/currentUser", token: viewer, body: map[string]any{"id": "rdflow-viewer"}})
	f.call("viewer-ab-empty", request{path: "/api/ab", token: viewer})
	book := map[string]any{
		"tags": []any{"office", " x-flow ", "x-flow", 7, nil},
		"peers": []any{
			map[string]any{"id": "900100001", "alias": "Reception", "tags": []any{"office", "x-flow"}, "hash": "h1"},
			map[string]any{"id": "900100005", "tags": "vip,boss"},
			map[string]any{"id": "RDFLOWTYPED1", "username": "typed", "tags": []any{}},
			map[string]any{"id": "900100028"},
			map[string]any{"id": " 900100002 ", "platform": "Windows"},
			"not a peer",
			map[string]any{"alias": "no id"},
		},
		"tag_colors": `{"office":1}`,
	}
	bookText := mustJSON(t, book)
	f.call("viewer-ab-save", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"data": bookText}})
	f.call("viewer-ab-read", request{path: "/api/ab", token: viewer})
	f.call("viewer-ab-save-malformed", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"data": `{"peers":[{"id":"900100001"`}})
	f.call("viewer-ab-read-malformed", request{path: "/api/ab", token: viewer})
	f.call("viewer-ab-save-object", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"data": book}})
	f.call("viewer-ab-read-object", request{path: "/api/ab", token: viewer})
	f.call("viewer-ab-save-list", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"data": `[{"id":"900100001"}]`}})
	f.call("viewer-ab-read-list", request{path: "/api/ab", token: viewer})
	f.call("viewer-ab-save-without-data", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"other": 1}})
	f.call("viewer-ab-read-unchanged", request{path: "/api/ab", token: viewer})
	f.call("viewer-ab-save-null", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"data": nil}})
	f.call("viewer-ab-read-null", request{path: "/api/ab", token: viewer})
	f.call("viewer-ab-save-again", request{method: "POST", path: "/api/ab", token: viewer, body: map[string]any{"data": bookText}})
	f.call("viewer-ab-personal-probe", request{method: "POST", path: "/api/ab/personal", token: viewer})
	f.call("viewer-ab-personal-probe-empty", request{method: "POST", path: "/api/ab/personal", token: viewer, body: map[string]any{"data": ""}})
	f.call("viewer-ab-personal-empty", request{path: "/api/ab/personal", token: viewer})
	f.call("viewer-ab-personal-save", request{method: "POST", path: "/api/ab/personal", token: viewer, body: map[string]any{"data": `{"tags":["home"],"peers":[{"id":"900100012","tags":["home"]}]}`}})
	f.call("viewer-ab-personal-read", request{path: "/api/ab/personal", token: viewer})
	f.call("viewer-ab-tags", request{path: "/api/ab/tags", token: viewer})
	f.call("viewer-users", request{path: "/api/users", token: viewer})
	f.call("viewer-peers", request{path: "/api/peers", token: viewer, unordered: true})
	f.call("viewer-peers-folder-name", request{path: "/api/peers?folder=office", token: viewer, unordered: true})
	f.call("viewer-peers-folder-guid", request{path: "/api/peers?group=folder_2", token: viewer, unordered: true})
	f.call("viewer-peers-folder-number", request{path: "/api/peers?folder_id=3", token: viewer, unordered: true})
	f.call("viewer-peers-group-name", request{path: "/api/peers?device_group_name=" + url.QueryEscape("Critical servers"), token: viewer, unordered: true})
	f.call("viewer-peers-private-group", request{path: "/api/peers?group=rd-flow-private", token: viewer, unordered: true})
	f.call("viewer-peers-tag-fallback", request{path: "/api/peers?group=scanner", token: viewer, unordered: true})
	f.call("viewer-peers-tags", request{path: "/api/peers?tag=server,linux", token: viewer, unordered: true})
	f.call("viewer-peers-tags-repeated", request{path: "/api/peers?tags=server&tags=critical", token: viewer, unordered: true})
	f.call("viewer-peers-page", request{path: "/api/peers?tag=scanner&page=2&page_size=1", token: viewer})
	f.call("viewer-peers-search", request{path: "/api/peers?search=DEMO-LAB", token: viewer, unordered: true})
	f.call("viewer-peers-search-list", request{path: "/api/peers?search=a&search=b", token: viewer})
	f.call("viewer-peers-list-post", request{method: "POST", path: "/api/peers/list", token: viewer, body: map[string]any{"folder_id": 4}, unordered: true})
	f.call("viewer-peers-list-get", request{path: "/api/peers/list?tags=lab", token: viewer, unordered: true})
	f.call("viewer-peers-list-put", request{method: "PUT", path: "/api/peers/list?group_name=Lab", token: viewer, unordered: true})
	f.call("viewer-device-groups", request{path: "/api/device-group", token: viewer, volatile: groupMarks})
	f.call("viewer-device-groups-accessible", request{path: "/api/device-group/accessible", token: viewer, volatile: groupMarks})
	f.call("viewer-group", request{path: "/api/group", token: viewer, volatile: groupMarks})
	f.call("viewer-group-get", request{path: "/api/group/get", token: viewer, volatile: groupMarks})
	f.call("viewer-group-get-post", request{method: "POST", path: "/api/group/get", token: viewer, volatile: groupMarks})
	f.call("viewer-device-group-save", request{method: "POST", path: "/api/device-group", token: viewer, body: map[string]any{"name": "x"}})
	f.call("viewer-user-group", request{path: "/api/user/group", token: viewer})
	f.call("viewer-user-groups", request{path: "/api/user-groups", token: viewer, volatile: groupMarks})
	f.call("viewer-user-groups-save", request{method: "POST", path: "/api/user-groups", token: viewer, body: map[string]any{"name": "x"}})
	f.call("viewer-strategies", request{path: "/api/strategies", token: viewer})
	f.call("viewer-strategy-missing", request{path: "/api/strategies/rdflow-missing", token: viewer})
	f.call("viewer-strategy-save", request{method: "POST", path: "/api/strategies", token: viewer, body: map[string]any{"name": "x"}})
	f.call("viewer-devices", request{path: "/api/devices?pageSize=5", token: viewer})
	f.call("viewer-peer-key", request{path: "/api/peer-key/900100001", token: viewer})
	f.call("viewer-peer-key-unknown", request{path: "/api/peer-key/RDFLOWNONE", token: viewer})
	f.call("viewer-peer-key-long", request{path: "/api/peer-key/" + strings.Repeat("k", 40), token: viewer})
	f.call("viewer-audit", request{path: "/api/audit", token: viewer})
	f.call("viewer-audit-conn", request{path: "/api/audit/conn?host_id=rdflow-none", token: viewer})
	f.call("viewer-audit-file", request{path: "/api/audit/file?limit=5", token: viewer})
	f.call("viewer-audit-alarm", request{path: "/api/audit/alarm?alarm_type=2", token: viewer})

	// An operator may change devices: the tags of the address book go to
	// the devices.
	operator := f.signIn("operator-sign-in", "e2e_operator", pw, "rdflow-operator", "rdflow-uuid-operator")
	f.call("operator-current-user", request{path: "/api/currentUser", token: operator})
	f.call("operator-ab-save", request{method: "POST", path: "/api/ab", token: operator, body: map[string]any{
		"data": `{"tags":["x-flow-op"],"peers":[{"id":"900100024","tags":["lab","iot","x-flow-op"]},{"id":"bad id!","tags":["x"]},{"id":"900100005","tags":["vip"]}]}`,
	}})
	f.call("operator-ab-read", request{path: "/api/ab", token: operator})
	f.call("operator-ab-tags", request{path: "/api/ab/tags", token: operator})
	f.call("operator-peers-after-tags", request{path: "/api/peers?folder=Lab", token: operator, unordered: true})
	f.call("operator-ab-personal-save", request{method: "POST", path: "/api/ab/personal", token: operator, body: map[string]any{"data": `{"peers":[{"id":"900100024","tags":["lab","iot"]}]}`}})
	f.call("operator-peers", request{path: "/api/peers", token: operator, unordered: true})
	f.call("operator-users", request{path: "/api/users", token: operator})
	f.call("operator-device-group-save", request{method: "POST", path: "/api/device-group", token: operator, body: map[string]any{"name": "x"}})

	// An admin sees every device and may write groups and strategies.
	admin1 := f.signIn("admin-sign-in", "e2e_admin", pw, "rdflow-admin", "rdflow-uuid-admin")
	f.call("admin-current-user", request{path: "/api/currentUser", token: admin1})
	f.call("admin-users", request{path: "/api/users", token: admin1})
	f.call("admin-ab-save", request{method: "POST", path: "/api/ab", token: admin1, body: map[string]any{"data": bookText}})
	f.call("admin-ab-read", request{path: "/api/ab", token: admin1})
	f.call("admin-peers", request{path: "/api/peers", token: admin1, unordered: true})
	f.call("admin-peers-private-group", request{path: "/api/peers?group=rd-flow-private", token: admin1, unordered: true})
	created1 := f.call("admin-device-group-create", request{method: "POST", path: "/api/device-group", token: admin1,
		body:     map[string]any{"name": "  rd-flow-group  ", "note": "flow", "team_id": "team-1", "allowed_users": []string{"nobody"}},
		volatile: []string{"guid " + guidMark, "id", "created_at " + timeMark}})
	groupGUID, _ := field(t, created1, "guid").(string)
	f.call("admin-device-group-update", request{method: "POST", path: "/api/device-group", token: admin1,
		body:     map[string]any{"guid": groupGUID, "name": "rd-flow-group-2", "source_type": "tag", "tag": "kiosk"},
		volatile: []string{"guid " + guidMark, "id", "created_at " + timeMark}})
	f.call("admin-device-group-update-keeps", request{method: "POST", path: "/api/device-group", token: admin1,
		body:     map[string]any{"guid": groupGUID, "name": "rd-flow-group-2", "dynamic": true, "tag_filter": "kiosk", "note": nil},
		volatile: []string{"guid " + guidMark, "id", "created_at " + timeMark}})
	f.call("admin-device-group-unknown", request{method: "POST", path: "/api/device-group", token: admin1, body: map[string]any{"guid": "rdflow-missing", "name": "x"}})
	f.call("admin-device-group-no-name", request{method: "POST", path: "/api/device-group", token: admin1, body: map[string]any{"name": "   "}})
	f.call("admin-device-group-tag-without-filter", request{method: "POST", path: "/api/device-group", token: admin1, body: map[string]any{"name": "x", "source_type": "tag"}})
	f.call("admin-device-groups", request{path: "/api/device-group", token: admin1, volatile: groupMarks})
	f.call("admin-device-groups-accessible", request{path: "/api/device-group/accessible", token: admin1, volatile: groupMarks})
	f.call("admin-peers-manual-group", request{path: "/api/peers?group=" + url.QueryEscape("Support team devices"), token: admin1, unordered: true})
	users1 := f.call("admin-user-groups-create", request{method: "POST", path: "/api/user-groups", token: admin1,
		body:     map[string]any{"name": "rd-flow-users", "note": "flow", "team_id": 5},
		volatile: []string{"guid " + guidMark, "id", "created_at " + timeMark}})
	userGroupGUID, _ := field(t, users1, "guid").(string)
	f.call("admin-user-groups-update", request{method: "POST", path: "/api/user-groups", token: admin1,
		body:     map[string]any{"guid": userGroupGUID, "name": "rd-flow-users-2"},
		volatile: []string{"guid " + guidMark, "id", "created_at " + timeMark}})
	f.call("admin-user-groups-unknown", request{method: "POST", path: "/api/user-groups", token: admin1, body: map[string]any{"guid": "rdflow-missing", "name": "x"}})
	f.call("admin-user-groups-no-name", request{method: "POST", path: "/api/user-groups", token: admin1, body: map[string]any{"name": 5}})
	f.call("admin-user-groups", request{path: "/api/user-groups", token: admin1, volatile: groupMarks})
	f.call("admin-user-group", request{path: "/api/user/group", token: admin1})
	strategyMarks := []string{"guid " + guidMark, "id", "created_at " + timeMark, "updated_at " + timeMark}
	createdMarks := append(append([]string{}, strategyMarks...), "user_group_guid "+guidMark, "device_group_guid "+guidMark)
	s1 := f.call("admin-strategy-create", request{method: "POST", path: "/api/strategies", token: admin1,
		body:     map[string]any{"name": "rd-flow-strategy", "user_group_guid": userGroupGUID, "device_group_guid": groupGUID, "enabled": true, "permissions": map[string]any{"b": 1, "a": []int{1}}},
		volatile: createdMarks})
	strategyGUID, _ := field(t, s1, "guid").(string)
	f.call("admin-strategy-create-disabled", request{method: "POST", path: "/api/strategies", token: admin1,
		body:     map[string]any{"name": "rd-flow-strategy-off", "enabled": 0, "permissions": "not an object"},
		volatile: []string{"guid " + guidMark, "id", "created_at " + timeMark, "updated_at " + timeMark}})
	f.call("admin-strategy-no-name", request{method: "POST", path: "/api/strategies", token: admin1, body: map[string]any{"name": ""}})
	f.call("admin-strategy-update-unknown", request{method: "POST", path: "/api/strategies", token: admin1, body: map[string]any{"guid": "rdflow-missing", "name": "x"}})
	f.call("admin-strategies", request{path: "/api/strategies", token: admin1, volatile: []string{"data[*].guid " + guidMark, "data[0].user_group_guid " + guidMark, "data[0].device_group_guid " + guidMark}})
	f.call("admin-strategy", request{path: "/api/strategies/" + strategyGUID, token: admin1, volatile: createdMarks})
	f.call("admin-strategy-update", request{method: "POST", path: "/api/strategies", token: admin1,
		body:     map[string]any{"guid": strategyGUID, "name": "rd-flow-strategy-2", "enabled": false, "permissions": []int{1}},
		volatile: strategyMarks})
	f.call("admin-strategy-assign", request{method: "POST", path: "/api/strategies/assign", token: admin1,
		body: map[string]any{"strategy": strategyGUID, "peers": []string{"900100001"}, "users": []string{"e2e_viewer"}, "groups": []string{"rd-flow-group-2"}}})
	f.call("admin-strategy-assigned", request{path: "/api/strategies/" + strategyGUID, token: admin1,
		volatile: append(append([]string{}, strategyMarks...), "peers[*] "+guidMark, "users[*] "+guidMark, "groups[*] "+guidMark)})
	f.call("admin-strategy-assign-nothing", request{method: "POST", path: "/api/strategies/assign", token: admin1, body: map[string]any{"strategy": strategyGUID}})
	f.call("admin-strategy-assign-unknown-peer", request{method: "POST", path: "/api/strategies/assign", token: admin1, body: map[string]any{"strategy": strategyGUID, "peers": []string{"RDFLOWNONE"}}})
	f.call("admin-strategy-assign-unknown-strategy", request{method: "POST", path: "/api/strategies/assign", token: admin1, body: map[string]any{"strategy": "rdflow-missing", "users": []string{"e2e_admin"}}})
	f.call("admin-strategy-assign-clear", request{method: "POST", path: "/api/strategies/assign", token: admin1, body: map[string]any{"strategy": "", "peers": []string{"900100001"}}})
	f.call("admin-strategy-after-clear", request{path: "/api/strategies/" + strategyGUID, token: admin1,
		volatile: append(append([]string{}, strategyMarks...), "users[*] "+guidMark, "groups[*] "+guidMark)})
	f.call("admin-strategy-status", request{method: "PUT", path: "/api/strategies/" + strategyGUID + "/status", token: admin1, body: map[string]any{}})
	f.call("admin-strategy-status-unknown", request{method: "PUT", path: "/api/strategies/rdflow-missing/status", token: admin1, body: []any{true}})
	f.call("admin-strategy-delete", request{method: "DELETE", path: "/api/strategies/" + strategyGUID, token: admin1, volatile: []string{"guid " + guidMark}})
	f.call("admin-strategy-delete-again", request{method: "DELETE", path: "/api/strategies/" + strategyGUID, token: admin1})
	f.call("admin-strategy-gone", request{path: "/api/strategies/" + strategyGUID, token: admin1})
	f.call("admin-devices", request{path: "/api/devices", token: admin1})
	f.call("admin-devices-one", request{path: "/api/devices?id=900100005&pageSize=1000", token: admin1})
	f.call("admin-devices-page", request{path: "/api/devices?pageSize=3", token: admin1})
	f.call("admin-peer-key", request{path: "/api/peer-key/900100005", token: admin1})
	f.call("admin-audit", request{path: "/api/audit", token: admin1})

	// A pro account cannot sign in (BUGS S11). A token issued before the
	// account became pro still works, and gets nothing of the inventory.
	f.call("pro-sign-in", request{method: "POST", path: "/api/login",
		body: map[string]any{"username": "e2e_pro", "password": pw, "id": "rdflow-pro", "uuid": "rdflow-uuid-pro"}})
	pro := f.signIn("pro-to-be-sign-in", proUser, flowPassword, "rdflow-pro", "rdflow-uuid-pro")
	if a := admin.Do(jsonRequest(t, http.MethodPatch, "/api/users/"+proID, map[string]any{"role": "pro"})); a.Status != http.StatusOK {
		t.Fatalf("make %s a pro account: status %d", proUser, a.Status)
	}
	f.call("pro-current-user", request{path: "/api/currentUser", token: pro})
	f.call("pro-ab-empty", request{path: "/api/ab", token: pro})
	f.call("pro-ab-save", request{method: "POST", path: "/api/ab", token: pro, body: map[string]any{"data": `{"tags":["b","a"," "],"peers":[{"id":"900100001"}]}`}})
	f.call("pro-ab-read", request{path: "/api/ab", token: pro})
	f.call("pro-ab-tags", request{path: "/api/ab/tags", token: pro})
	f.call("pro-ab-personal", request{path: "/api/ab/personal", token: pro})
	f.call("pro-users", request{path: "/api/users", token: pro})
	f.call("pro-peers", request{path: "/api/peers", token: pro})
	f.call("pro-peers-list", request{method: "POST", path: "/api/peers/list", token: pro})
	f.call("pro-device-groups", request{path: "/api/device-group", token: pro})
	f.call("pro-user-groups", request{path: "/api/user-groups", token: pro})
	f.call("pro-user-group", request{path: "/api/user/group", token: pro})
	f.call("pro-peer-key", request{path: "/api/peer-key/900100001", token: pro})
	f.call("pro-strategies", request{path: "/api/strategies", token: pro, volatile: []string{"data[*].guid " + guidMark}})

	// The same client signs in again: its first token is gone.
	viewer2 := f.signIn("viewer-sign-in-again", "e2e_viewer", pw, "rdflow-viewer", "rdflow-uuid-viewer")
	f.call("viewer-first-token-revoked", request{path: "/api/currentUser", token: viewer})
	f.call("viewer-second-token", request{path: "/api/currentUser", token: viewer2})
	viewer3 := f.signIn("viewer-other-client", "e2e_viewer", pw, "rdflow-viewer-2", "rdflow-uuid-viewer-2")
	f.call("viewer-second-token-kept", request{path: "/api/currentUser", token: viewer2})
	f.call("viewer-logout-client", request{method: "POST", path: "/api/logout", token: viewer2, body: map[string]any{"id": "rdflow-viewer", "uuid": "rdflow-uuid-viewer"}})
	f.call("viewer-logged-out", request{path: "/api/currentUser", token: viewer2})
	f.call("viewer-other-client-kept", request{path: "/api/currentUser", token: viewer3})
	viewer4 := f.signIn("viewer-sign-in-without-uuid", "e2e_viewer", pw, "rdflow-viewer-3", "")
	f.call("viewer-logout-all", request{method: "POST", path: "/api/logout", token: viewer3, body: map[string]any{}})
	f.call("viewer-other-client-gone", request{path: "/api/currentUser", token: viewer3})
	f.call("viewer-without-uuid-gone", request{path: "/api/currentUser", token: viewer4})
	f.call("viewer-logout-again", request{method: "POST", path: "/api/logout", token: viewer3})
	f.call("operator-logout", request{method: "POST", path: "/api/logout", token: operator, body: map[string]any{"id": "rdflow-operator"}})
	f.call("operator-logged-out", request{path: "/api/currentUser", token: operator})

	// The second step. Any code passes (BUGS S5); a secret works once.
	step1 := f.call("tfa-password", request{method: "POST", path: "/api/login",
		body: map[string]any{"username": flowUser, "password": flowPassword, "id": "rdflow-tfa", "uuid": "rdflow-uuid-tfa"}, volatile: []string{secretMark}})
	pending, _ := field(t, step1, "secret").(string)
	tfa := f.call("tfa-wrong-code", request{method: "POST", path: "/api/login",
		body: map[string]any{"username": flowUser, "tfaCode": wrongCode(t, secret), "secret": pending, "id": "rdflow-tfa", "uuid": "rdflow-uuid-tfa"}, volatile: []string{tokenMark}})
	tfaToken, _ := field(t, tfa, "access_token").(string)
	f.call("tfa-current-user", request{path: "/api/currentUser", token: tfaToken})
	f.call("tfa-secret-used", request{method: "POST", path: "/api/login",
		body: map[string]any{"tfaCode": currentCode(t, secret), "secret": pending}})
	step2 := f.call("tfa-password-again", request{method: "POST", path: "/api/login",
		body: map[string]any{"username": flowUser, "password": flowPassword}, volatile: []string{secretMark}})
	pending2, _ := field(t, step2, "secret").(string)
	f.call("tfa-verification-code", request{method: "POST", path: "/api/login",
		body: map[string]any{"verificationCode": currentCode(t, secret), "secret": pending2, "uuid": "rdflow-uuid-tfa-2"}, volatile: []string{tokenMark}})

	// Ten wrong passwords lock the account.
	for i := 1; i <= 10; i++ {
		f.call(fmt.Sprintf("wrong-password-%d", i), request{method: "POST", path: "/api/login",
			body: map[string]any{"username": flowUser, "password": "not-the-password", "id": "rdflow-tfa"}})
	}
	time.Sleep(500 * time.Millisecond)
	f.call("locked", request{method: "POST", path: "/api/login",
		body: map[string]any{"username": flowUser, "password": "not-the-password"}, volatile: []string{"retry_after integer"}})
	f.call("locked-right-password", request{method: "POST", path: "/api/login",
		body: map[string]any{"username": flowUser, "password": flowPassword}, volatile: []string{"retry_after integer"}})
	f.call("tfa-logout", request{method: "POST", path: "/api/logout", token: tfaToken})

	f.keep("audit-entries", "GET /api/settings/audit", auditSince(t, admin, baseline))

	if settings.RequireGo {
		for id := range f.asked {
			method, path, _ := strings.Cut(id, " ")
			if handler := client.Handler(method, path); handler != contract.HandlerGo {
				t.Errorf("%s was answered by %q, not by the Go console", id, handler)
			}
		}
	}

	if os.Getenv("RDFLOW_RECORD") == "1" {
		// A mark that does not fit the value it masks would make the
		// recording fail every later run
		for i, step := range f.steps {
			for _, diff := range contract.CompareAnswers(step.Answer, f.got[i]) {
				t.Errorf("%s (%s) does not fit its own marks: %s", step.Name, step.Request, diff)
			}
		}
		if t.Failed() {
			t.FailNow()
		}
		out, err := json.MarshalIndent(f.steps, "", " ")
		if err != nil {
			t.Fatalf("encode the recording: %v", err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("%v", err)
		}
		if err := os.WriteFile(file, append(out, '\n'), 0o644); err != nil {
			t.Fatalf("write the recording: %v", err)
		}
		t.Logf("wrote %d steps to %s", len(f.steps), file)
		return
	}

	recorded, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	var want []flowStep
	if err := json.Unmarshal(recorded, &want); err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	if len(want) != len(f.steps) {
		t.Fatalf("the recording has %d steps, the flow %d", len(want), len(f.steps))
	}
	for i, step := range f.steps {
		if want[i].Name != step.Name {
			t.Fatalf("step %d is %q in the recording and %q in the flow", i+1, want[i].Name, step.Name)
		}
		for _, diff := range contract.CompareAnswers(want[i].Answer, f.got[i]) {
			t.Errorf("%s (%s): %s", step.Name, step.Request, diff)
		}
	}
}

// enableTOTP signs the flow's account in to the console and switches its
// second factor on. It returns the secret.
func enableTOTP(t *testing.T, client *contract.Client) string {
	t.Helper()
	browser := client.NewSession(contract.SignedOut)
	if _, err := browser.SignIn(); err != nil {
		t.Fatalf("open a browser: %v", err)
	}
	if a := browser.Do(jsonRequest(t, http.MethodPost, "/api/auth/login", map[string]any{"username": flowUser, "password": flowPassword})); a.Status != http.StatusOK {
		t.Fatalf("sign in as %s: status %d", flowUser, a.Status)
	}
	setup := browser.Do(jsonRequest(t, http.MethodPost, "/api/auth/totp/setup", map[string]any{}))
	secret, _ := field(t, setup, "secret").(string)
	if secret == "" {
		t.Fatalf("two-factor setup: status %d", setup.Status)
	}
	if a := browser.Do(jsonRequest(t, http.MethodPost, "/api/auth/totp/enable", map[string]any{"code": currentCode(t, secret)})); a.Status != http.StatusOK {
		t.Fatalf("two-factor enable: status %d", a.Status)
	}
	browser.Do(jsonRequest(t, http.MethodPost, "/api/auth/logout", map[string]any{}))
	return secret
}

func jsonRequest(t *testing.T, method, path string, body any) contract.Request {
	t.Helper()
	return contract.Request{Method: method, Path: path, Body: []byte(mustJSON(t, body)), ContentType: "application/json"}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(raw)
}

// field reads a value out of a JSON answer by keys.
func field(t *testing.T, answer contract.Answer, keys ...string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(answer.Body.JSON, &value); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("the answer has no %s", strings.Join(keys, "."))
		}
		value = object[key]
	}
	return value
}

// sortData orders the list "data" of a JSON answer by the ids of its
// items.
func sortData(t *testing.T, answer contract.Answer) contract.Answer {
	t.Helper()
	if answer.Body.Kind != contract.BodyJSON {
		return answer
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(answer.Body.JSON, &body); err != nil {
		return answer
	}
	var items []json.RawMessage
	if err := json.Unmarshal(body["data"], &items); err != nil {
		return answer
	}
	id := func(raw json.RawMessage) string {
		var item struct {
			ID any `json:"id"`
		}
		_ = json.Unmarshal(raw, &item)
		return fmt.Sprint(item.ID)
	}
	sort.SliceStable(items, func(i, j int) bool { return id(items[i]) < id(items[j]) })
	sorted, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("sort the answer: %v", err)
	}
	// Put the sorted list back without touching the rest of the text
	var out bytes.Buffer
	dec := json.NewDecoder(bytes.NewReader(answer.Body.JSON))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return answer
	}
	out.WriteByte('{')
	first := true
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return answer
		}
		key, _ := keyTok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return answer
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		if key == "data" {
			out.Write(sorted)
		} else {
			out.Write(value)
		}
	}
	out.WriteByte('}')
	answer.Body.JSON = out.Bytes()
	return answer
}

func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("the secret is not base32: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

// currentCode is the code of the step that is running; near the end of a
// step this waits for the next.
func currentCode(t *testing.T, secret string) string {
	t.Helper()
	if left := 30 - time.Now().Unix()%30; left <= 4 {
		time.Sleep(time.Duration(left)*time.Second + 200*time.Millisecond)
	}
	return totpAt(t, secret, time.Now())
}

// wrongCode is six digits that are not the code of this step or the next.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	for _, candidate := range []string{"000000", "111111", "222222"} {
		if candidate != totpAt(t, secret, now) && candidate != totpAt(t, secret, now.Add(30*time.Second)) {
			return candidate
		}
	}
	return "333333"
}

// auditRows reads the newest audit entries as the settings page gets them.
func auditRows(t *testing.T, admin *contract.Session) []map[string]any {
	t.Helper()
	answer := admin.Do(contract.Request{Method: http.MethodGet, Path: "/api/settings/audit?limit=500"})
	if answer.Status != http.StatusOK {
		t.Fatalf("read the audit log: status %d", answer.Status)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(answer.Body.JSON))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("read the audit log: %v", err)
	}
	return body.Data
}

func auditID(row map[string]any) int64 {
	var id int64
	_, _ = fmt.Sscan(fmt.Sprint(row["id"]), &id)
	return id
}

func lastAuditID(t *testing.T, admin *contract.Session) int64 {
	t.Helper()
	var last int64
	for _, row := range auditRows(t, admin) {
		last = max(last, auditID(row))
	}
	return last
}

// auditSince returns the entries of the client API (actions that start
// with "api_") written after the entry with the id baseline, oldest first:
// action, details, address and account name of each.
func auditSince(t *testing.T, admin *contract.Session, baseline int64) contract.Answer {
	t.Helper()
	rows := auditRows(t, admin)
	sort.Slice(rows, func(i, j int) bool { return auditID(rows[i]) < auditID(rows[j]) })
	type entry struct {
		Action   any `json:"action"`
		Details  any `json:"details"`
		Address  any `json:"ip_address"`
		Username any `json:"username"`
	}
	entries := []entry{}
	for _, row := range rows {
		if auditID(row) <= baseline || !strings.HasPrefix(fmt.Sprint(row["action"]), "api_") {
			continue
		}
		entries = append(entries, entry{row["action"], row["details"], row["ip_address"], row["username"]})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("encode the audit entries: %v", err)
	}
	return contract.Answer{Status: http.StatusOK, Body: contract.Body{Kind: contract.BodyJSON, JSON: raw}}
}

// maskPaths replaces the values at the paths of a JSON text: strings by
// "<volatile>", numbers by 0. A path is keys joined by dots; "[*]" after a
// key stands for every element of the list there, "[n]" for one.
func maskPaths(raw json.RawMessage, paths []string) (json.RawMessage, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}
	for _, p := range paths {
		value = maskAt(value, strings.Split(p, "."))
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func maskAt(value any, keys []string) any {
	if len(keys) == 0 {
		switch value.(type) {
		case string:
			return "<volatile>"
		case json.Number:
			return json.Number("0")
		}
		return value
	}
	key, index := keys[0], -1
	every := false
	if open := strings.Index(key, "["); open >= 0 && strings.HasSuffix(key, "]") {
		inner := key[open+1 : len(key)-1]
		key = key[:open]
		if inner == "*" {
			every = true
		} else {
			_, _ = fmt.Sscan(inner, &index)
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	child, has := object[key]
	if !has {
		return value
	}
	list, isList := child.([]any)
	switch {
	case every && isList:
		for i := range list {
			list[i] = maskAt(list[i], keys[1:])
		}
	case index >= 0 && isList:
		if index < len(list) {
			list[index] = maskAt(list[index], keys[1:])
		}
	default:
		object[key] = maskAt(child, keys[1:])
	}
	return value
}
