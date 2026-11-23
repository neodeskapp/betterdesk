package cdap_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/cdap"
)

// knownDevices are the devices of the device layer in the scope tests:
// the API server knows them as peers, every other id is unknown.
var knownDevices = map[string]bool{"dev-1": true, "dev-2": true}

// peers is the API server's peer lookup of the device layer, kept apart
// from the scripted API server of the world, so the calls a vector
// expects stay the gateway's own.
func peers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(r.URL.Path, "/api/peers/")
	if r.Method == http.MethodGet && knownDevices[id] {
		_, _ = io.WriteString(w, `{"id":"`+id+`","hostname":"h"}`)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"not found"}`)
}

// withScope gives the routes the device scope of package access under a
// restricted default: the accounts and their granted devices are added
// to the world's database.
func withScope(t *testing.T, grants map[int64][]string) func(d *kit.Deps) {
	return func(d *kit.Deps) {
		ctx := context.Background()
		for id, role := range map[int64]string{5: "operator", 6: "viewer"} {
			if _, err := d.Store.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, "e2e_"+role, role); err != nil {
				t.Fatal(err)
			}
			for _, device := range grants[id] {
				if _, err := d.Store.Exec(ctx, "INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?)", id, device); err != nil {
					t.Fatal(err)
				}
			}
		}
		d.Access = access.New(d.Store, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: "restricted"})
		d.DeviceGroups = devicegroups.New(devicegroups.Options{DB: d.Store, Access: d.Access})
		d.Backend = goapi.NewBackend(goapi.New(http.HandlerFunc(peers)), devicegroups.DeviceStore{Groups: d.DeviceGroups})
		cdap.Register(d)
	}
}

func account(id int64, role string) *proxytest.Account {
	a := &proxytest.Account{UserID: id}
	a.User.Username, a.User.Role = "e2e_"+role, role
	return a
}

// TestDeviceRoutesKeepToTheScope: the routes of one gateway device answer
// 403 for a device the account may not see and do not ask the API server
// (BUGS RC8). A device in the account's scope, any device for an admin,
// and an id the device layer does not know go on to the API server.
func TestDeviceRoutesKeepToTheScope(t *testing.T) {
	w := proxytest.NewWorld(t, withScope(t, map[int64][]string{5: {"dev-1"}, 6: {"dev-1"}}))
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	command := `{"widget_id":"power","action":"set"}`
	link := `{"linked_peer_id":"dev-9"}`
	routes := []struct {
		method, path string
		body         *string
	}{
		{"GET", "", nil},
		{"GET", "/manifest", nil},
		{"GET", "/state", nil},
		{"GET", "/linked", nil},
		{"POST", "/command", &command},
		{"POST", "/link", &link},
	}
	forbidden := `{"success":false,"error":"errors.forbidden"}`
	for _, route := range routes {
		writes := route.method == "POST"
		for _, c := range []struct {
			who     *proxytest.Account
			device  string
			refused bool
		}{
			{account(5, "operator"), "dev-2", true},
			{account(5, "operator"), "dev-1", false},
			{account(5, "operator"), "no-such-device", false},
			{account(6, "viewer"), "dev-2", !writes},
			{account(6, "viewer"), "dev-1", false},
			{account(1, "admin"), "dev-2", false},
		} {
			req := proxytest.Request{Method: route.method, Path: "/api/cdap/devices/" + c.device + route.path, Session: c.who, Body: route.body}
			if route.body != nil {
				req.Type = "application/json"
			}
			rec, calls := w.Do(req, ok)
			what := route.method + " " + req.Path + " as " + c.who.User.Role
			switch {
			case c.refused:
				if rec.Code != http.StatusForbidden || rec.Body.String() != forbidden || len(calls) != 0 {
					t.Errorf("%s: %d %s, %d calls to the API server; want 403 %s and none", what, rec.Code, rec.Body, len(calls), forbidden)
				}
			case c.who.User.Role == "viewer" && writes:
				// The command permission comes first
				if rec.Code != http.StatusForbidden || rec.Body.String() == forbidden || len(calls) != 0 {
					t.Errorf("%s: %d %s; want the permission's 403", what, rec.Code, rec.Body)
				}
			default:
				// An operator's command reads the manifest first: it lacks
				// cdap.terminal (TestATerminalCommandNeedsCDAPTerminal)
				if route.path == "/command" && c.who.User.Role == "operator" && len(calls) == 2 && calls[0].URL == "/api/cdap/devices/"+c.device+"/manifest" {
					calls = calls[1:]
				}
				if rec.Code != http.StatusOK || len(calls) != 1 || !strings.Contains(calls[0].URL, "/"+c.device) {
					t.Errorf("%s: %d %s, calls %+v; want the API server's answer", what, rec.Code, rec.Body, calls)
				}
			}
		}
	}
}

// TestListsKeepToTheScope: the lists of gateway devices and alerts keep
// only the entries of devices the account may see, as the routes of one
// device do (BUGS RC9): a device in the account's scope and an id the
// device layer does not know stay, and total counts what is left. An
// admin gets the API server's lists as they are.
func TestListsKeepToTheScope(t *testing.T) {
	w := proxytest.NewWorld(t, withScope(t, map[int64][]string{5: {"dev-1"}}))
	devices := &proxytest.Answer{Status: 200, Type: "application/json",
		Body: `{"devices":[{"id":"dev-1","connected":true},{"id":"dev-2","connected":true},{"id":"gw-9","connected":true}],"total":3}`}
	alerts := &proxytest.Answer{Status: 200, Type: "application/json",
		Body: `{"alerts":[{"alert_id":"a1","device_id":"dev-2"},{"alert_id":"a2","device_id":"dev-1"},{"alert_id":"a3","device_id":"gw-9"}],"total":3}`}
	alertsOfDev2 := &proxytest.Answer{Status: 200, Type: "application/json",
		Body: `{"alerts":[{"alert_id":"a1","device_id":"dev-2"}],"total":1}`}
	for _, c := range []struct {
		who  *proxytest.Account
		path string
		api  *proxytest.Answer
		want string
	}{
		{account(5, "operator"), "/api/cdap/devices", devices, `{"devices":[{"id":"dev-1","connected":true},{"id":"gw-9","connected":true}],"total":2}`},
		{account(6, "viewer"), "/api/cdap/devices", devices, `{"devices":[{"id":"gw-9","connected":true}],"total":1}`},
		{account(1, "admin"), "/api/cdap/devices", devices, `{"devices":[{"id":"dev-1","connected":true},{"id":"dev-2","connected":true},{"id":"gw-9","connected":true}],"total":3}`},
		{account(5, "operator"), "/api/cdap/alerts", alerts, `{"alerts":[{"alert_id":"a2","device_id":"dev-1"},{"alert_id":"a3","device_id":"gw-9"}],"total":2}`},
		{account(6, "viewer"), "/api/cdap/alerts", alerts, `{"alerts":[{"alert_id":"a3","device_id":"gw-9"}],"total":1}`},
		{account(1, "admin"), "/api/cdap/alerts", alerts, `{"alerts":[{"alert_id":"a1","device_id":"dev-2"},{"alert_id":"a2","device_id":"dev-1"},{"alert_id":"a3","device_id":"gw-9"}],"total":3}`},
		{account(5, "operator"), "/api/cdap/alerts?device_id=dev-2", alertsOfDev2, `{"alerts":[],"total":0}`},
		{account(1, "admin"), "/api/cdap/alerts?device_id=dev-2", alertsOfDev2, `{"alerts":[{"alert_id":"a1","device_id":"dev-2"}],"total":1}`},
	} {
		rec, calls := w.Do(proxytest.Request{Method: "GET", Path: c.path, Session: c.who}, c.api)
		if rec.Code != http.StatusOK || rec.Body.String() != c.want || len(calls) != 1 {
			t.Errorf("GET %s as %s: %d %s, %d calls; want 200 %s", c.path, c.who.User.Role, rec.Code, rec.Body, len(calls), c.want)
		}
	}
}

// TestATerminalCommandNeedsCDAPTerminal: a command to a terminal widget
// of the device's manifest runs in a shell and needs cdap.terminal: an
// operator (cdap.command alone) is refused after the manifest was read
// and the command is not sent; its command to a toggle goes out; a global
// admin, who has cdap.terminal, sends the shell command without the
// manifest being read (BUGS RV159, A96).
func TestATerminalCommandNeedsCDAPTerminal(t *testing.T) {
	w := proxytest.NewWorld(t, withScope(t, map[int64][]string{5: {"dev-1"}}))
	manifest := &proxytest.Answer{Status: 200, Type: "application/json",
		Body: `{"device":{"name":"box"},"widgets":[{"id":"power","type":"toggle"},{"id":"shell","type":" Terminal "}]}`}
	send := func(who *proxytest.Account, widget string) (int, string, []proxytest.Call) {
		body := `{"widget_id":"` + widget + `","action":"execute","value":"id"}`
		rec, calls := w.Do(proxytest.Request{Method: "POST", Path: "/api/cdap/devices/dev-1/command", Session: who, Type: "application/json", Body: &body}, manifest)
		return rec.Code, rec.Body.String(), calls
	}
	urls := func(calls []proxytest.Call) string {
		var out []string
		for _, c := range calls {
			out = append(out, c.Method+" "+c.URL)
		}
		return strings.Join(out, ", ")
	}
	operator, globalAdmin := account(5, "operator"), account(4, "global_admin")
	if code, body, calls := send(operator, "shell"); code != 403 || body != `{"success":false,"error":"Permission denied: cdap.terminal"}` || urls(calls) != "GET /api/cdap/devices/dev-1/manifest" {
		t.Errorf("the operator's shell command: %d %s after %s", code, body, urls(calls))
	}
	if code, body, calls := send(operator, "power"); code != 200 || urls(calls) != "GET /api/cdap/devices/dev-1/manifest, POST /api/cdap/devices/dev-1/command" {
		t.Errorf("the operator's toggle command: %d %s after %s", code, body, urls(calls))
	}
	if code, body, calls := send(globalAdmin, "shell"); code != 200 || urls(calls) != "POST /api/cdap/devices/dev-1/command" {
		t.Errorf("the global admin's shell command: %d %s after %s", code, body, urls(calls))
	}
}
