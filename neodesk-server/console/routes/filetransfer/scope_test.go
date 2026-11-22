package filetransfer_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	service "github.com/unitronix/betterdesk-server/console/filetransfer"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/filetransfer"
)

// peersOf is the API server's peer lookup of the device layer: it knows
// the devices of the list, every other id is unknown.
func peersOf(known ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/peers/")
		for _, device := range known {
			if r.Method == http.MethodGet && id == device {
				_, _ = io.WriteString(w, `{"id":"`+id+`","hostname":"h"}`)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}
}

// withDevices gives the routes a device layer that knows the devices and
// the device scope of package access under the default mode, with the
// devices granted to the operator (id 5) and the viewer (id 6).
func withDevices(t *testing.T, mode string, grants map[int64][]string, known ...string) func(d *kit.Deps) {
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
		d.Access = access.New(d.Store, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: mode})
		d.DeviceGroups = devicegroups.New(devicegroups.Options{DB: d.Store, Access: d.Access})
		d.Backend = goapi.NewBackend(goapi.New(peersOf(known...)), devicegroups.DeviceStore{Groups: d.DeviceGroups})
	}
}

func account(id int64, role string) *proxytest.Account {
	a := &proxytest.Account{UserID: id}
	a.User.Username, a.User.Role = "e2e_"+role, role
	return a
}

// TestTransfersKeepToTheScope: the routes for people answer only for
// transfers of devices the account may see (BUGS RV43). An operator whose
// scope holds dev-1 lists the transfers of dev-1 and of a device the
// device layer does not know, gets 404 for the progress, the cancel and
// the download of a transfer of dev-2, and 403 for a start with dev-2. An
// admin sees every transfer.
func TestTransfersKeepToTheScope(t *testing.T) {
	transfers := service.New(service.Options{Dir: filepath.Join(t.TempDir(), "transfers")})
	scope := withDevices(t, "restricted", map[int64][]string{5: {"dev-1"}}, "dev-1", "dev-2")
	w := proxytest.NewWorld(t, func(d *kit.Deps) {
		scope(d)
		d.FileTransfer = transfers
		filetransfer.Register(d.At("/api/files"))
	})
	completed := func(device string) string {
		record, err := transfers.CreateTransfer(service.Params{Direction: "download", DeviceID: device, Filename: device + ".txt", Size: float64(2)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transfers.ReceiveChunk(record.ID, float64(0), base64.StdEncoding.EncodeToString([]byte("hi"))); err != nil {
			t.Fatal(err)
		}
		return record.ID
	}
	mine, other, unknown := completed("dev-1"), completed("dev-2"), completed("gone-1")
	operator, admin := account(5, "operator"), account(1, "admin")
	do := func(who *proxytest.Account, method, path string, body *string) (int, string) {
		req := proxytest.Request{Method: method, Path: path, Session: who, Body: body}
		if body != nil {
			req.Type = "application/json"
		}
		rec, _ := w.Do(req, nil)
		return rec.Code, rec.Body.String()
	}
	notFound := `{"error":"Transfer not found"}`

	if status, body := do(operator, "GET", "/api/files/transfers", nil); status != http.StatusOK || !strings.Contains(body, mine) || !strings.Contains(body, unknown) || strings.Contains(body, other) {
		t.Errorf("the operator's list: %d %s", status, body)
	}
	if status, body := do(admin, "GET", "/api/files/transfers", nil); status != http.StatusOK || !strings.Contains(body, mine) || !strings.Contains(body, unknown) || !strings.Contains(body, other) {
		t.Errorf("the admin's list: %d %s", status, body)
	}
	if status, body := do(operator, "GET", "/api/files/transfers?device_id=dev-2", nil); status != http.StatusOK || body != `[]` {
		t.Errorf("the operator's list of dev-2: %d %s", status, body)
	}
	for _, path := range []string{"/api/files/transfers/" + other, "/api/files/transfers/" + other + "/download"} {
		if status, body := do(operator, "GET", path, nil); status != http.StatusNotFound || body != notFound {
			t.Errorf("GET %s as the operator: %d %s", path, status, body)
		}
	}
	empty := `{}`
	if status, body := do(operator, "POST", "/api/files/transfers/"+other+"/cancel", &empty); status != http.StatusNotFound || body != notFound {
		t.Errorf("the operator's cancel of dev-2's transfer: %d %s", status, body)
	}
	if got := transfers.GetTransfer(other).Status; got != service.StatusCompleted {
		t.Errorf("dev-2's transfer is %s after the operator's cancel", got)
	}
	for _, id := range []string{mine, unknown} {
		if status, body := do(operator, "GET", "/api/files/transfers/"+id+"/download", nil); status != http.StatusOK || body != "hi" {
			t.Errorf("the operator's download of %s: %d %s", id, status, body)
		}
	}
	if status, body := do(admin, "GET", "/api/files/transfers/"+other+"/download", nil); status != http.StatusOK || body != "hi" {
		t.Errorf("the admin's download of dev-2's transfer: %d %s", status, body)
	}

	start := func(who *proxytest.Account, device string) (int, string) {
		body := `{"direction":"upload","device_id":"` + device + `","filename":"a.txt","size":2}`
		return do(who, "POST", "/api/files/transfer", &body)
	}
	before := len(transfers.ListTransfers(service.Filter{}))
	if status, body := start(operator, "dev-2"); status != http.StatusForbidden || body != `{"error":"Access denied"}` {
		t.Errorf("the operator's start with dev-2: %d %s", status, body)
	}
	if after := len(transfers.ListTransfers(service.Filter{})); after != before {
		t.Errorf("the refused start stored a transfer: %d transfers, %d before", after, before)
	}
	for _, device := range []string{"dev-1", "gone-1"} {
		if status, body := start(operator, device); status != http.StatusCreated {
			t.Errorf("the operator's start with %s: %d %s", device, status, body)
		}
	}
	if status, body := start(admin, "dev-2"); status != http.StatusCreated {
		t.Errorf("the admin's start with dev-2: %d %s", status, body)
	}
	if status, body := do(operator, "POST", "/api/files/transfers/"+mine+"/cancel", &empty); status != http.StatusOK {
		t.Errorf("the operator's cancel of dev-1's transfer: %d %s", status, body)
	}
}
