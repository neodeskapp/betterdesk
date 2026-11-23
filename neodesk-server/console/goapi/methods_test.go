package goapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeAPI stands in for the API server: it records every request and
// answers all of them the same way.
type fakeAPI struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
	answer   func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.RequestURI)
	f.bodies = append(f.bodies, string(body))
	f.mu.Unlock()
	f.answer(w, r)
}

func answerJSON(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

// noAnswer ends the request the way a handler that dies does.
func noAnswer(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }

// toJSON encodes a result the way a route would send it.
func toJSON(t *testing.T, v any) string {
	t.Helper()
	if th, ok := v.(thrown); ok {
		v = NewObject("thrown", th.err.Message, "status", float64(th.err.Status()), "data", th.err.Data())
	}
	var buf bytes.Buffer
	if err := encodeValue(&buf, v); err != nil {
		t.Fatalf("encode result: %v", err)
	}
	return buf.String()
}

func parseArgs(t *testing.T, args string) []any {
	t.Helper()
	if args == "" {
		return nil
	}
	v, err := Parse([]byte(args))
	if err != nil {
		t.Fatalf("arguments %s: %v", args, err)
	}
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("arguments %s are not a list", args)
	}
	return list
}

const (
	okBody   = `{"ok":true}`
	okW      = `{"success":true,"data":{"ok":true}}`
	notFound = "Request failed with status code 404"
	hangUp   = "socket hang up"
)

// methodCase is one call of a function of the Node client: the request it
// has to make and what it returns when the API server answers 200 with
// {"ok":true}, when it answers 404 with {"error":"nope"}, and when it gives
// no answer. An empty expectation follows from kind.
type methodCase struct {
	fn   string
	args string
	// request is the method and the path with its query; "" when the
	// function must not make a call.
	request string
	body    string
	// kind is "f1", "f2" or "f3" for a function that returns W on success
	// and that failure shape otherwise.
	kind   string
	ok     string
	failed string
	down   string
	// skipOK leaves the 200 answer unchecked, for functions that reshape
	// a peer or a target.
	skipOK bool
	// failedRequests are the requests when the first one fails, when they
	// differ from request.
	failedRequests []string
}

func (c methodCase) want(mode string) string {
	explicit := map[string]string{"ok": c.ok, "failed": c.failed, "down": c.down}[mode]
	if explicit != "" {
		return explicit
	}
	switch mode + " " + c.kind {
	case "ok f1", "ok f2", "ok f3", "ok f4":
		return okW
	case "failed f1":
		return `{"success":false,"error":"` + notFound + `"}`
	case "failed f2":
		return `{"success":false,"error":"nope"}`
	case "failed f3":
		return `{"success":false,"error":"nope","status":404}`
	case "failed f4":
		return `{"success":false,"error":"` + notFound + `","status":404}`
	case "down f1", "down f2", "down f4":
		return `{"success":false,"error":"` + hangUp + `"}`
	case "down f3":
		return `{"success":false,"error":"` + hangUp + `","status":502}`
	}
	return ""
}

func fellBack(msg string, extra string) string {
	return `{"success":false,"error":"` + msg + `"` + extra + `}`
}

var methodCases = []methodCase{
	{fn: "getHealth", request: "GET /api/health/detail", failedRequests: []string{"GET /api/health/detail", "GET /api/health"},
		ok:     `{"ok":true,"status":"running","backend":"neodesk"}`,
		failed: `{"status":"unreachable","backend":"neodesk","error":"` + notFound + `"}`,
		down:   `{"status":"unreachable","backend":"neodesk","error":"` + hangUp + `"}`},
	{fn: "getServerStats", request: "GET /api/server/stats", kind: "f1"},
	{fn: "getAllPeers", request: "GET /api/peers", ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "getAllPeers", args: `[{"includeDeleted":true}]`, request: "GET /api/peers?include_deleted=true", ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "getAllPeers", args: `[{"includeDeleted":false}]`, request: "GET /api/peers", ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "getPeer", args: `["a/b c"]`, request: "GET /api/peers/a%2Fb%20c", skipOK: true, failed: `null`, down: `null`},
	{fn: "getPeerIncludingDeleted", args: `["P1"]`, request: "GET /api/peers/P1", skipOK: true, failed: `null`, down: `null`,
		failedRequests: []string{"GET /api/peers/P1", "GET /api/peers?include_deleted=true"}},
	{fn: "deletePeer", args: `["P1"]`, request: "DELETE /api/peers/P1", kind: "f2"},
	{fn: "deletePeer", args: `["P1",{"revoke":true,"hard":true}]`, request: "DELETE /api/peers/P1?revoke=true&hard=true", kind: "f2"},
	{fn: "deletePeer", args: `["P1",{"cascade":1}]`, request: "DELETE /api/peers/P1?cascade=true", kind: "f2"},
	{fn: "banPeer", args: `["P1","why"]`, request: "POST /api/peers/P1/ban", body: `{"reason":"why"}`, kind: "f2"},
	{fn: "banPeer", args: `["P1"]`, request: "POST /api/peers/P1/ban", body: `{"reason":""}`, kind: "f2"},
	{fn: "unbanPeer", args: `["P1"]`, request: "POST /api/peers/P1/unban", kind: "f2"},
	{fn: "getConnectionMode", args: `["P1"]`, request: "GET /api/peers/P1/connection-mode", kind: "f3"},
	{fn: "setConnectionMode", args: `["P1",{"mode":"incoming-only","operator_id":"7"}]`, request: "POST /api/peers/P1/connection-mode",
		body: `{"mode":"incoming-only","operator_id":"7"}`, kind: "f3"},
	{fn: "restorePeer", args: `["P1"]`, request: "POST /api/peers/P1/restore", kind: "f2"},
	{fn: "changePeerId", args: `["P1","P2"]`, request: "POST /api/peers/P1/change-id", body: `{"new_id":"P2"}`, kind: "f2"},
	{fn: "getStatusSummary", request: "GET /api/peers/status/summary", kind: "f1"},
	{fn: "getOnlinePeers", request: "GET /api/peers/online", ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "getPeerStatus", args: `["P1"]`, request: "GET /api/peers/P1/status", kind: "f1"},
	{fn: "getPeerTelemetry", args: `["P1"]`, request: "GET /api/peers/P1/telemetry", kind: "f1"},
	{fn: "refreshPeerHardware", args: `["P1"]`, request: "POST /api/peers/P1/telemetry/refresh", kind: "f2"},
	{fn: "queuePeerTelemetryCommand", args: `["P1","collect",{"a":1}]`, request: "POST /api/peers/P1/telemetry/command",
		body: `{"command":"collect","args":{"a":1}}`, kind: "f2"},
	{fn: "queuePeerTelemetryCommand", args: `["P1","collect"]`, request: "POST /api/peers/P1/telemetry/command",
		body: `{"command":"collect","args":{}}`, kind: "f2"},
	{fn: "getBlocklist", request: "GET /api/blocklist", kind: "f1"},
	{fn: "removeBlocklistEntry", args: `["10.0.0.0/8"]`, request: "DELETE /api/blocklist/10.0.0.0%2F8", kind: "f2"},
	{fn: "setPeerTags", args: `["P1","x, y,,"]`, request: "PUT /api/peers/P1/tags", body: `{"tags":["x","y"]}`, kind: "f2"},
	{fn: "setPeerTags", args: `["P1",["p","q"]]`, request: "PUT /api/peers/P1/tags", body: `{"tags":["p","q"]}`, kind: "f2"},
	{fn: "setPeerTags", args: `["P1",5]`, request: "PUT /api/peers/P1/tags", body: `{"tags":[]}`, kind: "f2"},
	{fn: "getPeersByTag", args: `["t g"]`, request: "GET /api/tags/t%20g/peers", ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "updatePeer", args: `["P1",{"note":"n"}]`, request: "PATCH /api/peers/P1", body: `{"note":"n"}`, kind: "f2"},
	{fn: "getAllRemoteTargets", args: `[{}]`, request: "GET /api/remote-targets?org_id=&include_disabled=false", ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "getAllRemoteTargets", args: `[{"orgId":"o1","includeDisabled":true}]`, request: "GET /api/remote-targets?org_id=o1&include_disabled=true",
		ok: `[]`, failed: `[]`, down: `[]`},
	{fn: "testRemoteTarget", args: `[{"name":"x"}]`, request: "POST /api/remote-targets/test", body: `{"name":"x"}`, kind: "f2"},
	{fn: "getRemoteTarget", args: `["rt_1"]`, request: "GET /api/remote-targets/rt_1", skipOK: true, failed: `null`, down: `null`},
	{fn: "createRemoteTarget", args: `[{"name":"x"}]`, request: "POST /api/remote-targets", body: `{"name":"x"}`, kind: "f2"},
	{fn: "updateRemoteTarget", args: `["rt_1",{"name":"y"}]`, request: "PATCH /api/remote-targets/rt_1", body: `{"name":"y"}`, kind: "f2"},
	{fn: "deleteRemoteTarget", args: `["rt_1"]`, request: "DELETE /api/remote-targets/rt_1", kind: "f2"},
	{fn: "getRemoteTargetCredentialStatus", args: `["rt_1"]`, request: "GET /api/remote-targets/rt_1/credentials", kind: "f2"},
	{fn: "setRemoteTargetCredentials", args: `["rt_1",{"username":"u"}]`, request: "PUT /api/remote-targets/rt_1/credentials",
		body: `{"username":"u"}`, kind: "f2"},
	{fn: "clearRemoteTargetCredentials", args: `["rt_1"]`, request: "DELETE /api/remote-targets/rt_1/credentials", kind: "f2"},
	{fn: "acceptRemoteTargetCertificate", args: `["rt_1","ab"]`, request: "POST /api/remote-targets/rt_1/certificate/accept",
		body: `{"fingerprint":"ab"}`, kind: "f2"},
	{fn: "getAuditEvents", args: `[2]`, request: "GET /api/audit/events?limit=2", kind: "f1"},
	{fn: "getAuditEvents", request: "GET /api/audit/events?limit=100", kind: "f1"},
	{fn: "getClientAuditConnections", args: `[{"limit":5,"host_id":"H 1"}]`, request: "GET /api/audit/conn?limit=5&host_id=H+1",
		ok: `{"data":[],"total":0}`, failed: `null`, down: `null`},
	{fn: "getClientAuditConnections", request: "GET /api/audit/conn", ok: `{"data":[],"total":0}`, failed: `null`, down: `null`},
	{fn: "getClientAuditFiles", args: `[{"limit":5}]`, request: "GET /api/audit/file?limit=5", ok: `{"data":[],"total":0}`, failed: `null`, down: `null`},
	{fn: "getClientAuditAlarms", args: `[{"offset":10}]`, request: "GET /api/audit/alarm?offset=10", ok: `{"data":[],"total":0}`, failed: `null`, down: `null`},
	{fn: "getConfig", args: `["k1"]`, request: "GET /api/config/k1", kind: "f1"},
	{fn: "setConfig", args: `["k1","v"]`, request: "PUT /api/config/k1", body: `{"value":"v"}`, kind: "f2"},
	{fn: "getPeerAvailability", args: `["P1",{"day":"2026-01-01","tzOffset":-60}]`,
		request: "GET /api/peers/P1/availability?day=2026-01-01&tz_offset=-60", kind: "f2"},
	{fn: "getPeerAvailability", args: `["P1",{"tzOffset":0}]`, request: "GET /api/peers/P1/availability?tz_offset=0", kind: "f2"},
	{fn: "getPeerAvailability", args: `["P1",{"day":"","tzOffset":""}]`, request: "GET /api/peers/P1/availability", kind: "f2"},
	{fn: "getPeerAvailability", args: `["P1"]`, request: "GET /api/peers/P1/availability", kind: "f2"},
	{fn: "getPeerConnectionSessions", args: `["P1",{"limit":5}]`, request: "GET /api/peers/P1/connection-sessions?limit=5", kind: "f2"},
	{fn: "getPeerConnectionSessions", args: `["P1",{"limit":0}]`, request: "GET /api/peers/P1/connection-sessions", kind: "f2"},
	{fn: "getMeshStatus", request: "GET /api/mesh/status", ok: okW,
		failed: fellBack(notFound, `,"data":{"enabled":false}`), down: fellBack(hangUp, `,"data":{"enabled":false}`)},
	{fn: "getCDAPStatus", request: "GET /api/cdap/status", kind: "f1"},
	{fn: "getCDAPDevices", request: "GET /api/cdap/devices", kind: "f1"},
	{fn: "getCDAPDeviceInfo", args: `["D1"]`, request: "GET /api/cdap/devices/D1", kind: "f1"},
	{fn: "getCDAPDeviceManifest", args: `["D1"]`, request: "GET /api/cdap/devices/D1/manifest", kind: "f1"},
	{fn: "getCDAPDeviceState", args: `["D1"]`, request: "GET /api/cdap/devices/D1/state", kind: "f1"},
	{fn: "sendCDAPCommand", args: `["D1","w","set",1]`, request: "POST /api/cdap/devices/D1/command",
		body: `{"widget_id":"w","action":"set","value":1,"reason":""}`, kind: "f1"},
	{fn: "sendCDAPCommand", args: `["D1","w","set",{"on":true},"because"]`, request: "POST /api/cdap/devices/D1/command",
		body: `{"widget_id":"w","action":"set","value":{"on":true},"reason":"because"}`, kind: "f1"},
	{fn: "getCDAPAlerts", request: "GET /api/cdap/alerts", ok: okW,
		failed: fellBack(notFound, `,"alerts":[],"total":0`), down: fellBack(hangUp, `,"alerts":[],"total":0`)},
	{fn: "getCDAPAlerts", args: `["D1"]`, request: "GET /api/cdap/alerts?device_id=D1", ok: okW,
		failed: fellBack(notFound, `,"alerts":[],"total":0`), down: fellBack(hangUp, `,"alerts":[],"total":0`)},
	{fn: "getLinkedPeers", args: `["P1"]`, request: "GET /api/peers/P1/linked", ok: okW,
		failed: fellBack(notFound, `,"linked":[],"total":0`), down: fellBack(hangUp, `,"linked":[],"total":0`)},
	{fn: "listDeviceTokens", request: "GET /api/tokens", kind: "f3"},
	{fn: "listDeviceTokens", args: `[true]`, request: "GET /api/tokens?include_revoked=true", kind: "f3"},
	{fn: "createDeviceToken", args: `[{"name":"t","max_uses":1}]`, request: "POST /api/tokens", body: `{"name":"t","max_uses":1}`, kind: "f3"},
	{fn: "getDeviceToken", args: `[7]`, request: "GET /api/tokens/7", kind: "f3"},
	{fn: "getDeviceToken", args: `["a b"]`, ok: fellBack("Invalid tokenId", ""), failed: fellBack("Invalid tokenId", ""), down: fellBack("Invalid tokenId", "")},
	{fn: "getDeviceToken", args: `[0]`, ok: fellBack("Invalid tokenId", ""), failed: fellBack("Invalid tokenId", ""), down: fellBack("Invalid tokenId", "")},
	{fn: "updateDeviceToken", args: `[7,{"name":"n"}]`, request: "PUT /api/tokens/7", body: `{"name":"n"}`, kind: "f3"},
	{fn: "revokeDeviceToken", args: `[7]`, request: "DELETE /api/tokens/7", kind: "f3"},
	{fn: "bulkGenerateTokens", args: `[{"count":2}]`, request: "POST /api/tokens/generate-bulk", body: `{"count":2}`, kind: "f3"},
	{fn: "bindTokenToPeer", args: `[7," P1 "]`, request: "POST /api/tokens/7/bind", body: `{"peer_id":"P1"}`, kind: "f3"},
	{fn: "bindTokenToPeer", args: `[7,"bad id"]`, ok: fellBack("Invalid peerId", ""), failed: fellBack("Invalid peerId", ""), down: fellBack("Invalid peerId", "")},
	{fn: "bindTokenToPeer", args: `["..","P1"]`, ok: fellBack("Invalid tokenId", ""), failed: fellBack("Invalid tokenId", ""), down: fellBack("Invalid tokenId", "")},
	{fn: "getEnrollmentMode", request: "GET /api/enrollment/mode", kind: "f3"},
	{fn: "setEnrollmentMode", args: `["open"]`, request: "PUT /api/enrollment/mode", body: `{"mode":"open"}`, kind: "f3"},
	{fn: "getEnrollmentPending", request: "GET /api/enrollment/pending", ok: `{"success":true,"data":[],"count":0}`,
		failed: fellBack(notFound, `,"data":[],"count":0`), down: fellBack(hangUp, `,"data":[],"count":0`)},
	{fn: "getEnrollmentHistory", request: "GET /api/enrollment/history", ok: `{"success":true,"data":[],"count":0}`,
		failed: fellBack(notFound, `,"data":[],"count":0`), down: fellBack(hangUp, `,"data":[],"count":0`)},
	{fn: "getEnrollmentHistory", args: `["approved"]`, request: "GET /api/enrollment/history?status=approved", ok: `{"success":true,"data":[],"count":0}`,
		failed: fellBack(notFound, `,"data":[],"count":0`), down: fellBack(hangUp, `,"data":[],"count":0`)},
	{fn: "approveEnrollment", args: `["D1","Desk","turbo","a,b"]`, request: "POST /api/enrollment/approve/D1",
		body: `{"display_name":"Desk","sync_mode":"turbo","tags":"a,b"}`, kind: "f1"},
	{fn: "approveEnrollment", args: `["D1"]`, request: "POST /api/enrollment/approve/D1",
		body: `{"display_name":"","sync_mode":"standard","tags":""}`, kind: "f1"},
	{fn: "rejectEnrollment", args: `["D1",1]`, request: "POST /api/enrollment/reject/D1", body: `{"ban":true}`, kind: "f1"},
	{fn: "rejectEnrollment", args: `["D1"]`, request: "POST /api/enrollment/reject/D1", body: `{"ban":false}`, kind: "f1"},
	{fn: "clearEnrollmentRejection", args: `["D1"]`, request: "POST /api/enrollment/clear-rejection/D1", kind: "f1"},
	{fn: "createPairingCode", args: `["desk"]`, request: "POST /api/pairing/codes", body: `{"label":"desk"}`, ok: okW,
		failed: fellBack(notFound, `,"status":404`), down: fellBack(hangUp, "")},
	{fn: "createPairingCode", request: "POST /api/pairing/codes", body: `{"label":""}`, ok: okW,
		failed: fellBack(notFound, `,"status":404`), down: fellBack(hangUp, "")},
	{fn: "listPairingCodes", request: "GET /api/pairing/codes", ok: okW, failed: fellBack(notFound, `,"status":404`), down: fellBack(hangUp, "")},
	{fn: "revokePairingCode", args: `["c1"]`, request: "DELETE /api/pairing/codes/c1", ok: okW,
		failed: fellBack(notFound, `,"status":404`), down: fellBack(hangUp, "")},
	{fn: "getBranding", request: "GET /api/branding", ok: okW, failed: fellBack(notFound, ""), down: fellBack(hangUp, "")},
	{fn: "saveBranding", args: `[{"company_name":"Acme"}]`, request: "POST /api/branding", body: `{"company_name":"Acme"}`, kind: "f1"},
	{fn: "saveBranding", args: `["not an object"]`, request: "POST /api/branding", body: `"not an object"`, kind: "f1"},
	{fn: "getAccessPolicy", args: `["P1"]`, request: "GET /api/peers/P1/access-policy", kind: "f1"},
	{fn: "getAccessPolicy", args: `["bad/id"]`, ok: fellBack("Invalid peerId", ""), failed: fellBack("Invalid peerId", ""), down: fellBack("Invalid peerId", "")},
	{fn: "saveAccessPolicy", args: `["P1",{"unattended_enabled":true}]`, request: "PUT /api/peers/P1/access-policy",
		body: `{"unattended_enabled":true}`, kind: "f1"},
	{fn: "deleteAccessPolicy", args: `["P1"]`, request: "DELETE /api/peers/P1/access-policy", kind: "f1"},
	{fn: "getPeerConnectPassword", args: `["P1"]`, request: "GET /api/peers/P1/connect-password", ok: okBody,
		failed: `{"thrown":"` + notFound + `","status":404,"data":{"error":"nope"}}`,
		down:   `{"thrown":"` + hangUp + `","status":0,"data":null}`},
	{fn: "getPeerConnectPassword", args: `["a b"]`, ok: `{"thrown":"Invalid peerId","status":0,"data":null}`,
		failed: `{"thrown":"Invalid peerId","status":0,"data":null}`, down: `{"thrown":"Invalid peerId","status":0,"data":null}`},
	{fn: "listRoles", request: "GET /api/roles", kind: "f3"},
	{fn: "getRolePermissions", args: `["viewer"]`, request: "GET /api/roles/viewer/permissions", kind: "f3"},
	{fn: "getRolePermissions", args: `["no such role"]`, ok: fellBack("Invalid role", ""), failed: fellBack("Invalid role", ""), down: fellBack("Invalid role", "")},
	{fn: "listRolePermissionOverrides", request: "GET /api/role-permissions", kind: "f3"},
	{fn: "listRolePermissionOverrides", args: `["viewer"]`, request: "GET /api/role-permissions?role=viewer", kind: "f3"},
	{fn: "setRolePermission", args: `["viewer","device.edit",true]`, request: "POST /api/role-permissions",
		body: `{"role":"viewer","permission":"device.edit","granted":true}`, kind: "f3"},
	{fn: "setRolePermission", args: `["viewer","device edit",true]`, ok: fellBack("Invalid permission", ""),
		failed: fellBack("Invalid permission", ""), down: fellBack("Invalid permission", "")},
	{fn: "deleteRolePermission", args: `["viewer","device.edit"]`, request: "DELETE /api/role-permissions/viewer/device.edit", kind: "f3"},
	{fn: "listHelpRequests", args: `[{"status":"pending","limit":5}]`, request: "GET /api/help/requests?status=pending&limit=5",
		ok: `{"success":true,"data":[]}`, kind: "f2"},
	{fn: "listHelpRequests", args: `[{"device_id":"P1"}]`, request: "GET /api/help/requests?device_id=P1", ok: `{"success":true,"data":[]}`, kind: "f2"},
	{fn: "listHelpRequests", request: "GET /api/help/requests", ok: `{"success":true,"data":[]}`, kind: "f2"},
	{fn: "acknowledgeHelpRequest", args: `[9]`, request: "POST /api/help/requests/9/acknowledge", kind: "f2"},
	{fn: "acknowledgeHelpRequest", args: `["x y"]`, ok: fellBack("Invalid requestId", ""), failed: fellBack("Invalid requestId", ""), down: fellBack("Invalid requestId", "")},
	{fn: "resolveHelpRequest", args: `[9]`, request: "POST /api/help/requests/9/resolve", kind: "f2"},
	{fn: "getLDAPConfig", request: "GET /api/auth/ldap/config", kind: "f1"},
	{fn: "saveLDAPConfig", args: `[{"enabled":true}]`, request: "PUT /api/auth/ldap/config", body: `{"enabled":true}`, kind: "f1"},
	{fn: "testLDAPConnection", args: `[{}]`, request: "POST /api/auth/ldap/test", body: `{}`, kind: "f1"},
	{fn: "getOIDCConfig", request: "GET /api/auth/oidc/config", kind: "f1"},
	{fn: "saveOIDCConfig", args: `[{"enabled":true}]`, request: "PUT /api/auth/oidc/config", body: `{"enabled":true}`, kind: "f1"},
	{fn: "testOIDCDiscovery", args: `[{}]`, request: "POST /api/auth/oidc/test", body: `{}`, kind: "f1"},
	{fn: "getOIDCStatus", request: "GET /api/auth/oidc/status", kind: "f1"},
	{fn: "exchangeOIDCCode", args: `["x"]`, request: "POST /api/auth/oidc/exchange", body: `{"code":"x"}`, kind: "f1"},
	{fn: "startOIDCAuthorize", args: `["/devices"]`, request: "GET /api/auth/oidc/authorize?return_url=%2Fdevices",
		ok:     fellBack("OIDC authorize did not return an IdP redirect URL", ""),
		failed: fellBack("nope", ""), down: fellBack(hangUp, "")},
	{fn: "startOIDCAuthorize", request: "GET /api/auth/oidc/authorize",
		ok:     fellBack("OIDC authorize did not return an IdP redirect URL", ""),
		failed: fellBack("nope", ""), down: fellBack(hangUp, "")},
	{fn: "assignStrategy", args: `[{"strategy":"g1"}]`, request: "POST /api/strategies/assign", body: `{"strategy":"g1"}`, kind: "f2"},
	{fn: "getStrategy", args: `["g1"]`, request: "GET /api/strategies/g1", kind: "f1"},
	{fn: "deleteStrategy", args: `["g1"]`, request: "DELETE /api/strategies/g1", kind: "f2"},
	{fn: "setStrategyStatus", args: `["g1",true]`, request: "PUT /api/strategies/g1/status", body: `true`, kind: "f2"},
	{fn: "setStrategyStatus", args: `["g1",false]`, request: "PUT /api/strategies/g1/status", body: `false`, kind: "f2"},
	{fn: "listProDevices", args: `[{"id":"P1","pageSize":2}]`, request: "GET /api/devices?id=P1&pageSize=2", ok: okW,
		failed: fellBack(notFound, `,"data":{"total":0,"data":[]}`), down: fellBack(hangUp, `,"data":{"total":0,"data":[]}`)},
	{fn: "listProDevices", request: "GET /api/devices", ok: okW,
		failed: fellBack(notFound, `,"data":{"total":0,"data":[]}`), down: fellBack(hangUp, `,"data":{"total":0,"data":[]}`)},
	{fn: "getPanelCatalog", request: "GET /api/panel/catalog", kind: "f1"},
	{fn: "listPanelDeviceGroups", request: "GET /api/panel/device-groups", kind: "f1"},
	{fn: "savePanelDeviceGroup", args: `[{"name":"G","allowed_users":["u"]}]`, request: "POST /api/panel/device-groups",
		body: `{"name":"G","allowed_users":["u"]}`, kind: "f4"},
	{fn: "deletePanelDeviceGroup", args: `["g1"]`, request: "DELETE /api/panel/device-groups/g1", kind: "f4"},
	{fn: "deletePanelDeviceGroup", args: `[" a:b "]`, request: "DELETE /api/panel/device-groups/a%3Ab", kind: "f4"},
	{fn: "deletePanelDeviceGroup", args: `["g 1"]`, ok: fellBack("Invalid guid", ""), failed: fellBack("Invalid guid", ""), down: fellBack("Invalid guid", "")},
	{fn: "getPanelDeviceGroupPeers", args: `["g1"]`, request: "GET /api/panel/device-groups/g1/peers", kind: "f1"},
	{fn: "getPanelDeviceGroupPeers", args: `[".."]`, ok: fellBack("Invalid guid", ""), failed: fellBack("Invalid guid", ""), down: fellBack("Invalid guid", "")},
	{fn: "setPanelDeviceGroupPeers", args: `["g1",["A","B"]]`, request: "PUT /api/panel/device-groups/g1/peers", body: `{"peers":["A","B"]}`, kind: "f4"},
	{fn: "setPanelDeviceGroupPeers", args: `["g1","A"]`, request: "PUT /api/panel/device-groups/g1/peers", body: `{"peers":[]}`, kind: "f4"},
	{fn: "setPanelDeviceGroupPeers", args: `["g/1",["A"]]`, ok: fellBack("Invalid guid", ""), failed: fellBack("Invalid guid", ""), down: fellBack("Invalid guid", "")},
	{fn: "getPanelDeviceGroupAcl", args: `["g1"]`, request: "GET /api/panel/device-groups/g1/acl", kind: "f1"},
	{fn: "setPanelDeviceGroupAcl", args: `["g1",{"allowed_user_groups":["ug"],"allowed_users":"u","x":1}]`, request: "PUT /api/panel/device-groups/g1/acl",
		body: `{"allowed_users":[],"allowed_user_groups":["ug"]}`, kind: "f4"},
	{fn: "setPanelDeviceGroupAcl", args: `["g1",null]`, request: "PUT /api/panel/device-groups/g1/acl", body: `{"allowed_users":[],"allowed_user_groups":[]}`, kind: "f4"},
	{fn: "listPanelFolders", request: "GET /api/panel/folders", kind: "f1"},
	{fn: "createPanelFolder", args: `[{"name":"F","color":"#fff"}]`, request: "POST /api/panel/folders", body: `{"name":"F","color":"#fff"}`, kind: "f4"},
	{fn: "updatePanelFolder", args: `[" 7 ",{"name":"F"}]`, request: "PUT /api/panel/folders/7", body: `{"name":"F"}`, kind: "f4"},
	{fn: "updatePanelFolder", args: `["abc",{}]`, request: "PUT /api/panel/folders/NaN", body: `{}`, kind: "f4"},
	{fn: "deletePanelFolder", args: `[7]`, request: "DELETE /api/panel/folders/7", kind: "f4"},
	{fn: "deletePanelFolder", args: `["1e3"]`, request: "DELETE /api/panel/folders/1000", kind: "f4"},
	{fn: "setPanelDeviceFolder", args: `["P1","3"]`, request: "PUT /api/panel/devices/P1/folder", body: `{"folder_id":3}`, kind: "f4"},
	{fn: "setPanelDeviceFolder", args: `["P1","x"]`, request: "PUT /api/panel/devices/P1/folder", body: `{"folder_id":0}`, kind: "f4"},
	{fn: "setPanelDeviceFolder", args: `["P1",null]`, request: "PUT /api/panel/devices/P1/folder", body: `{"folder_id":0}`, kind: "f4"},
	{fn: "setPanelDeviceFolder", args: `["bad id",1]`, ok: fellBack("Invalid device id", ""), failed: fellBack("Invalid device id", ""), down: fellBack("Invalid device id", "")},
	{fn: "listPanelUserGrants", args: `[5]`, request: "GET /api/panel/users/5/grants", kind: "f1"},
	{fn: "setPanelUserGrants", args: `["5",["A"]]`, request: "PUT /api/panel/users/5/grants", body: `{"peers":["A"]}`, kind: "f4"},
	{fn: "setPanelUserGrants", args: `[5,{"0":"A"}]`, request: "PUT /api/panel/users/5/grants", body: `{"peers":[]}`, kind: "f4"},
}

// TestMethodsCallTheirRoutes pins, for every function of the Node client,
// the request it makes and the three results it can give.
func TestMethodsCallTheirRoutes(t *testing.T) {
	covered := map[string]bool{"getServerInfo": true, "syncOnlineStatus": true}
	for _, c := range methodCases {
		covered[c.fn] = true
		call := dispatch[c.fn]
		if call == nil {
			t.Errorf("%s: no method", c.fn)
			continue
		}
		args := parseArgs(t, c.args)
		modes := []struct {
			name   string
			answer func(http.ResponseWriter, *http.Request)
		}{
			{"ok", answerJSON(200, okBody)},
			{"failed", answerJSON(404, `{"error":"nope"}`)},
			{"down", noAnswer},
		}
		for _, mode := range modes {
			t.Run(c.fn+c.args+" "+mode.name, func(t *testing.T) {
				api := &fakeAPI{answer: mode.answer}
				got := toJSON(t, call(New(api), context.Background(), args))

				wantRequests := []string{c.request}
				if c.request == "" {
					wantRequests = nil
				}
				if mode.name != "ok" && c.failedRequests != nil {
					wantRequests = c.failedRequests
				}
				if strings.Join(api.requests, "\n") != strings.Join(wantRequests, "\n") {
					t.Errorf("requests = %q, want %q", api.requests, wantRequests)
				}
				if len(api.bodies) > 0 && api.bodies[0] != c.body {
					t.Errorf("request body = %s, want %s", api.bodies[0], c.body)
				}
				if mode.name == "ok" && c.skipOK {
					return
				}
				if want := c.want(mode.name); got != want {
					t.Errorf("result = %s\n  want   %s", got, want)
				}
			})
		}
	}
	for _, name := range exportedFunctions {
		if !covered[name] && dropped[name] == "" {
			t.Errorf("no case for %s", name)
		}
	}
}

func TestGetServerInfoCallsBothRoutes(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(http.ResponseWriter, *http.Request)
		want   string
	}{
		{"ok", answerJSON(200, okBody), `{"health":{"ok":true},"stats":{"ok":true},"backend":"neodesk"}`},
		{"failed", answerJSON(500, `{"error":"x"}`), `{"health":{},"stats":{},"backend":"neodesk"}`},
		{"down", noAnswer, `{"health":{},"stats":{},"backend":"neodesk"}`},
	} {
		api := &fakeAPI{answer: c.answer}
		got := toJSON(t, New(api).GetServerInfo(context.Background()))
		sort.Strings(api.requests)
		if strings.Join(api.requests, ", ") != "GET /api/health, GET /api/server/stats" {
			t.Errorf("%s: requests = %q", c.name, api.requests)
		}
		if got != c.want {
			t.Errorf("%s: result = %s, want %s", c.name, got, c.want)
		}
	}

	// One of the two failing leaves the other in place.
	api := &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			answerJSON(200, `{"status":"ok"}`)(w, r)
			return
		}
		answerJSON(401, `{"error":"x"}`)(w, r)
	}}
	if got, want := toJSON(t, New(api).GetServerInfo(context.Background())), `{"health":{"status":"ok"},"stats":{},"backend":"neodesk"}`; got != want {
		t.Errorf("stats failing: result = %s, want %s", got, want)
	}
}

// GetHealth reads the full payload of /api/health/detail and falls back to
// the short /api/health when the detail call fails for any reason (an API
// server before 3.5.264 answers 404 there, one that refuses the caller
// 401), as Node's getHealth does.
func TestGetHealthFallsBackToTheShortPayload(t *testing.T) {
	for _, c := range []struct {
		name   string
		detail func(http.ResponseWriter, *http.Request)
		want   string
	}{
		{"detail answers", answerJSON(200, `{"status":"ok","peers_total":3}`), `{"status":"running","peers_total":3,"backend":"neodesk"}`},
		{"no detail route", answerJSON(404, `{"error":"x"}`), `{"status":"running","version":"v","backend":"neodesk"}`},
		{"detail refused", answerJSON(401, `{"error":"x"}`), `{"status":"running","version":"v","backend":"neodesk"}`},
		{"detail fails", answerJSON(500, `{"error":"x"}`), `{"status":"running","version":"v","backend":"neodesk"}`},
		{"detail gives no answer", noAnswer, `{"status":"running","version":"v","backend":"neodesk"}`},
	} {
		api := &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/health/detail" {
				c.detail(w, r)
				return
			}
			answerJSON(200, `{"status":"ok","version":"v"}`)(w, r)
		}}
		if got := toJSON(t, New(api).GetHealth(context.Background())); got != c.want {
			t.Errorf("%s: GetHealth = %s, want %s", c.name, got, c.want)
		}
	}
	// Both failing: the error is the one of the short call
	api := &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health/detail" {
			noAnswer(w, r)
			return
		}
		answerJSON(503, `{"error":"x"}`)(w, r)
	}}
	if got, want := toJSON(t, New(api).GetHealth(context.Background())), `{"status":"unreachable","backend":"neodesk","error":"Request failed with status code 503"}`; got != want {
		t.Errorf("both failing: GetHealth = %s, want %s", got, want)
	}
}

func TestSyncOnlineStatusMakesNoCall(t *testing.T) {
	api := &fakeAPI{answer: answerJSON(200, okBody)}
	got := toJSON(t, New(api).SyncOnlineStatus())
	if want := `{"synced":0,"skipped":true,"reason":"neodesk_manages_state"}`; got != want {
		t.Errorf("result = %s, want %s", got, want)
	}
	if len(api.requests) != 0 {
		t.Errorf("made requests: %q", api.requests)
	}
}

// TestSetPanelDeviceGroup checks the call that sets a device's manual
// group in one write (BUGS UM33), which the Node client did not have: its
// request and body, the answer of a success and of a refusal (F4), and an
// id the path cannot carry, which makes no call.
func TestSetPanelDeviceGroup(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		id, guid string
		groups   []string
		answer   func(http.ResponseWriter, *http.Request)
		request  string
		body     string
		want     string
	}{
		{"P1", "g-1", []string{"g-1", "g:2"}, answerJSON(200, `{"device_id":"P1","guid":"g-1"}`),
			"PUT /api/panel/devices/P1/device-group", `{"guid":"g-1","groups":["g-1","g:2"]}`, `{"success":true,"data":{"device_id":"P1","guid":"g-1"}}`},
		{"P 2", "", nil, answerJSON(200, `{}`), "", "", `{"success":false,"error":"Invalid device id"}`},
		{"P1", "", []string{}, answerJSON(400, `{"error":"invalid JSON"}`),
			"PUT /api/panel/devices/P1/device-group", `{"guid":"","groups":[]}`, `{"success":false,"error":"Request failed with status code 400","status":400}`},
		{"P1", "g-1", nil, noAnswer, "PUT /api/panel/devices/P1/device-group", `{"guid":"g-1","groups":[]}`, `{"success":false,"error":"socket hang up"}`},
	} {
		api := &fakeAPI{answer: c.answer}
		got := toJSON(t, New(api).SetPanelDeviceGroup(ctx, c.id, c.guid, c.groups))
		if got != c.want {
			t.Errorf("SetPanelDeviceGroup(%q, %q, %q) = %s, want %s", c.id, c.guid, c.groups, got, c.want)
		}
		if want := strings.Fields(c.request); strings.Join(api.requests, " ") != strings.Join(want, " ") || len(api.bodies) > 0 && api.bodies[0] != c.body {
			t.Errorf("SetPanelDeviceGroup(%q, %q, %q) sent %q %q, want %q %s", c.id, c.guid, c.groups, api.requests, api.bodies, c.request, c.body)
		}
	}
}
