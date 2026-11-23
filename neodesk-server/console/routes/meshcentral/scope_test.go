package meshcentral_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/meshcentral"
)

// peers is the API server's peer lookup of the device layer: dev-1 and
// dev-2 exist, every other id is unknown.
func peers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(r.URL.Path, "/api/peers/")
	if r.Method == http.MethodGet && (id == "dev-1" || id == "dev-2") {
		_, _ = io.WriteString(w, `{"id":"`+id+`","hostname":"h"}`)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"not found"}`)
}

// scopedWorld is the group under a restricted device scope where the
// operator (id 5) is granted dev-1 only.
func scopedWorld(t *testing.T) *proxytest.World { return scopedWorldOf(t, "restricted") }

// scopedWorldOf is scopedWorld under a default device scope ("open" shows
// the operator every device in no closed group).
func scopedWorldOf(t *testing.T, scopeDefault string) *proxytest.World {
	return proxytest.NewWorld(t, func(d *kit.Deps) {
		ctx := context.Background()
		if _, err := d.Store.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'e2e_operator', 'x', 'operator')"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Store.Exec(ctx, "INSERT INTO user_peer_grants (user_id, peer_id) VALUES (5, 'dev-1')"); err != nil {
			t.Fatal(err)
		}
		d.Access = access.New(d.Store, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: scopeDefault})
		d.DeviceGroups = devicegroups.New(devicegroups.Options{DB: d.Store, Access: d.Access})
		d.Backend = goapi.NewBackend(goapi.New(http.HandlerFunc(peers)), devicegroups.DeviceStore{Groups: d.DeviceGroups})
		meshcentral.Register(d)
	})
}

func account(id int64, role string) *proxytest.Account {
	a := &proxytest.Account{UserID: id}
	a.User.Username, a.User.Role = "e2e_"+role, role
	return a
}

const forbidden = `{"success":false,"error":"errors.forbidden"}`

// TestDeviceRoutesKeepToTheScope: every route of one mesh device answers
// 403 for a device the operator may not see and does not ask the API
// server; a device in the scope, and any device for an admin, go on
// (BUGS RV159).
func TestDeviceRoutesKeepToTheScope(t *testing.T) {
	w := scopedWorld(t)
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	body := `{}`
	operator, admin := account(5, "operator"), account(1, "admin")
	reached := 0
	for _, action := range []string{"desktop", "terminal", "files", "share", "tcp", "udp", "power", "group", "exec"} {
		send := func(who *proxytest.Account, device string) (int, string, int) {
			req := proxytest.Request{Method: "POST", Path: "/api/mesh/devices/" + device + "/" + action, Session: who, Type: "application/json", Body: &body}
			rec, calls := w.Do(req, ok)
			return rec.Code, rec.Body.String(), len(calls)
		}
		if code, _, calls := send(admin, "dev-2"); code != 200 || calls != 1 {
			t.Errorf("%s of dev-2 as admin: %d with %d calls, want the API server's answer", action, code, calls)
		}
		code, _, calls := send(operator, "dev-1")
		if code != 200 || calls != 1 {
			// The operator lacks this route's permission: nothing to compare.
			continue
		}
		reached++
		if code, got, calls := send(operator, "dev-2"); code != 403 || got != forbidden || calls != 0 {
			t.Errorf("%s of dev-2 as the operator: %d %s with %d calls, want 403 %s and none", action, code, got, calls, forbidden)
		}
	}
	if reached < 5 {
		t.Fatalf("the operator reached %d routes for dev-1, want at least desktop, share, tcp, udp and power", reached)
	}
}

// recordingIDs reads the ids of a recording list.
func recordingIDs(t *testing.T, body string) []string {
	t.Helper()
	var list struct {
		Recordings []struct {
			ID string `json:"id"`
		} `json:"recordings"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("list %s: %v", body, err)
	}
	var ids []string
	for _, r := range list.Recordings {
		ids = append(ids, r.ID)
	}
	return ids
}

// TestRecordingsKeepToTheScope: the recording lists keep the recordings
// of devices the operator sees (the device is the start of the file
// name), and a recording of another device, or of no known device, is
// not downloaded. An admin gets every recording (BUGS RV159).
func TestRecordingsKeepToTheScope(t *testing.T) {
	w := scopedWorld(t)
	const (
		own     = "dev-1_kvm_20261121-101010_abcd1234.mcrec"
		other   = "dev-2_terminal_20261121-101010_abcd1234.mcrec"
		unknown = "gone-9_kvm_20261121-101010_abcd1234.mcrec"
		odd     = "odd.mcrec"
	)
	list := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"recordings":[` +
		`{"id":"` + own + `","peer_id":"dev"},{"id":"` + other + `","peer_id":"dev"},{"id":"` + unknown + `"},{"id":"` + odd + `"}],"note":"n"}`}
	operator, admin := account(5, "operator"), account(1, "admin")
	for _, path := range []string{"/api/mesh/recordings", "/api/session/recordings"} {
		rec, _ := w.Do(proxytest.Request{Method: "GET", Path: path, Session: operator}, list)
		if got := strings.Join(recordingIDs(t, rec.Body.String()), " "); rec.Code != 200 || got != own {
			t.Errorf("%s as the operator: %d %q, want only %s", path, rec.Code, got, own)
		}
		if path == "/api/session/recordings" && !strings.Contains(rec.Body.String(), `"note":"n"`) {
			t.Errorf("%s lost its note: %s", path, rec.Body)
		}
		rec, _ = w.Do(proxytest.Request{Method: "GET", Path: path, Session: admin}, list)
		if got := recordingIDs(t, rec.Body.String()); len(got) != 4 {
			t.Errorf("%s as admin: %q, want all four", path, got)
		}
	}

	file := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"file":true}`}
	for _, c := range []struct {
		who     *proxytest.Account
		id      string
		allowed bool
	}{
		{operator, own, true},
		{operator, other, false},
		{operator, unknown, false},
		{operator, odd, false},
		{admin, other, true},
		{admin, odd, true},
	} {
		rec, calls := w.Do(proxytest.Request{Method: "GET", Path: "/api/mesh/recordings/" + c.id, Session: c.who}, file)
		switch {
		case c.allowed && (rec.Code != 200 || len(calls) != 1):
			t.Errorf("download %s as %s: %d %s, want the API server's answer", c.id, c.who.User.Role, rec.Code, rec.Body)
		case !c.allowed && (rec.Code != 403 || rec.Body.String() != forbidden || len(calls) != 0):
			t.Errorf("download %s as %s: %d %s with %d calls, want 403 and none", c.id, c.who.User.Role, rec.Code, rec.Body, len(calls))
		}
	}
}

// TestRecordingOfNoKnownDeviceIsForAdmins: under an open device scope an
// operator sees the recordings of every known device, and still not one
// whose device the device layer does not know (BUGS RV159).
func TestRecordingOfNoKnownDeviceIsForAdmins(t *testing.T) {
	w := scopedWorldOf(t, "open")
	file := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"file":true}`}
	operator := account(5, "operator")
	if rec, calls := w.Do(proxytest.Request{Method: "GET", Path: "/api/mesh/recordings/dev-2_kvm_20261121-101010_abcd1234.mcrec", Session: operator}, file); rec.Code != 200 || len(calls) != 1 {
		t.Fatalf("a recording of dev-2 under an open scope: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{"gone-9_kvm_20261121-101010_abcd1234.mcrec", "odd.mcrec"} {
		if rec, calls := w.Do(proxytest.Request{Method: "GET", Path: "/api/mesh/recordings/" + id, Session: operator}, file); rec.Code != 403 || len(calls) != 0 {
			t.Errorf("%s under an open scope: %d %s with %d calls, want 403", id, rec.Code, rec.Body, len(calls))
		}
	}
}
