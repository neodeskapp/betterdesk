package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

// onboardStores runs a test on a new SQLite database and, when
// BETTERDESK_TEST_POSTGRES_DSN names a server, on a schema of its own on
// PostgreSQL.
func onboardStores(t *testing.T, test func(t *testing.T, srv *Server)) {
	storetest.Each(t, func(t *testing.T, sdb *store.DB) {
		test(t, onboardServer(storetest.GoServer(t, sdb)))
	})
}

func onboardServer(database db.Database) *Server {
	cfg := config.DefaultConfig()
	cfg.EnrollmentMode = config.EnrollmentModeManaged
	return New(cfg, database, peer.NewMap(), nil, "1.0.0-test")
}

type onboardAnswer struct {
	Success bool        `json:"success"`
	Error   string      `json:"error"`
	Message string      `json:"message"`
	Code    string      `json:"code"`
	Record  onboardCode `json:"record"`
	Codes   []onboardCode
	Created bool `json:"created"`
	Removed bool `json:"removed"`
	Device  struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	} `json:"device"`
}

func onboardCall(t *testing.T, srv *Server, method, path string, body any) (int, onboardAnswer) {
	t.Helper()
	text := ""
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		text = string(data)
	}
	status, raw := asConsole(t, srv, method, path, text)
	var out onboardAnswer
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	return status, out
}

func makeOnboardCode(t *testing.T, srv *Server, body map[string]any) onboardAnswer {
	t.Helper()
	if _, ok := body["ttl_seconds"]; !ok {
		body["ttl_seconds"] = 3600
	}
	if _, ok := body["max_uses"]; !ok {
		body["max_uses"] = 1
	}
	status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes", body)
	if status != http.StatusCreated || !out.Success {
		t.Fatalf("create: %d %+v", status, out)
	}
	return out
}

func TestOnboardCodesAreMadeWithTheirFields(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		out := makeOnboardCode(t, srv, map[string]any{
			"kind": "join", "name": "Front desk", "folder_id": 7, "email": "ana@example.com",
			"ttl_seconds": 7 * 24 * 3600, "max_uses": 3, "created_by_id": 42,
		})
		if !validOnboardCodeFormat(out.Code) || len(out.Code) != onboardCodeLen {
			t.Fatalf("code %q", out.Code)
		}
		r := out.Record
		if r.Kind != "join" || r.Name != "Front desk" || r.FolderID != 7 || r.Email != "ana@example.com" ||
			r.MaxUses != 3 || r.UsesLeft != 3 || r.CreatedByID != 42 || r.CreatedBy != consoleBridge.Username {
			t.Fatalf("record %+v", r)
		}
		created, _ := time.Parse(time.RFC3339, r.CreatedAt)
		expires, _ := time.Parse(time.RFC3339, r.ExpiresAt)
		if got := expires.Sub(created); got != 7*24*time.Hour {
			t.Fatalf("lifetime %v", got)
		}
		// Only the hash is stored.
		raw, _ := srv.db.GetConfig(pairingCodePrefix + pairingCodeHash(out.Code))
		if raw == "" || strings.Contains(raw, out.Code) {
			t.Fatalf("stored record %q", raw)
		}
	})
}

func TestOnboardCodeCreationChecksItsFields(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		for name, tc := range map[string]struct {
			body map[string]any
			want string
		}{
			"no kind":         {map[string]any{"ttl_seconds": 3600, "max_uses": 1}, "invalid_kind"},
			"pairing kind":    {map[string]any{"kind": "", "ttl_seconds": 3600, "max_uses": 1}, "invalid_kind"},
			"too short":       {map[string]any{"kind": "join", "ttl_seconds": 60, "max_uses": 1}, "invalid_lifetime"},
			"too long":        {map[string]any{"kind": "join", "ttl_seconds": 31 * 24 * 3600, "max_uses": 1}, "invalid_lifetime"},
			"no uses":         {map[string]any{"kind": "join", "ttl_seconds": 3600, "max_uses": 0}, "invalid_uses"},
			"too many uses":   {map[string]any{"kind": "join", "ttl_seconds": 3600, "max_uses": 1001}, "invalid_uses"},
			"negative folder": {map[string]any{"kind": "join", "ttl_seconds": 3600, "max_uses": 1, "folder_id": -1}, "invalid_folder"},
		} {
			status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes", tc.body)
			if status != http.StatusBadRequest || out.Error != tc.want {
				t.Errorf("%s: %d %+v, want 400 %s", name, status, out, tc.want)
			}
		}
	})
}

func TestOnboardRoutesAnswerOnlyInProcess(t *testing.T) {
	srv := onboardServer(testSetupDB(t))
	mux := srv.mux.get(srv)
	for _, path := range []string{"/api/onboard/codes", "/api/onboard/codes/lookup", "/api/onboard/codes/redeem", "/api/onboard/devices"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"kind":"join","ttl_seconds":3600,"max_uses":1}`))
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s from the listener: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if srv.liveOnboardCount() != 0 {
		t.Fatal("a listener request made a code")
	}
}

func TestEveryUnusableCodeGetsTheSameAnswer(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		expired := makeOnboardCode(t, srv, map[string]any{"kind": "join"})
		revoked := makeOnboardCode(t, srv, map[string]any{"kind": "join"})
		used := makeOnboardCode(t, srv, map[string]any{"kind": "support"})
		support := makeOnboardCode(t, srv, map[string]any{"kind": "support"})

		later := time.Now().Add(2 * time.Hour)
		if status, _ := onboardCall(t, srv, http.MethodDelete, "/api/onboard/codes/"+revoked.Record.ID, nil); status != http.StatusOK {
			t.Fatalf("revoke: %d", status)
		}
		if status, _ := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{"code": used.Code, "kind": "support"}); status != http.StatusOK {
			t.Fatalf("redeem: %d", status)
		}

		want := `{"error":"invalid_or_expired","success":false}`
		check := func(name, path string, body any) {
			t.Helper()
			data, _ := json.Marshal(body)
			status, raw := asConsole(t, srv, http.MethodPost, path, string(data))
			if status != http.StatusNotFound || raw != want {
				t.Errorf("%s: %d %s", name, status, raw)
			}
		}
		check("wrong code", "/api/onboard/codes/lookup", map[string]any{"code": "ZZZZ-ZZZZ"})
		check("wrong form", "/api/onboard/codes/lookup", map[string]any{"code": "ABC"})
		check("revoked", "/api/onboard/codes/lookup", map[string]any{"code": revoked.Code})
		check("used", "/api/onboard/codes/lookup", map[string]any{"code": used.Code})
		check("wrong kind", "/api/onboard/codes/redeem", map[string]any{"code": support.Code, "kind": "join"})
		check("pairing code", "/api/onboard/codes/lookup", map[string]any{"code": mintCode(t, srv, "desk")})

		srv.now = func() time.Time { return later }
		check("expired", "/api/onboard/codes/lookup", map[string]any{"code": expired.Code})
		if raw, _ := srv.db.GetConfig(pairingCodePrefix + pairingCodeHash(expired.Code)); raw != "" {
			t.Fatal("the expired record is still there")
		}
	})
}

func TestJoinRedeemAdmitsTheDeviceInManagedMode(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "Reception PC"}).Code
		_ = srv.db.SetConfig(pendingDevicePrefix+"123456789", `{"device_id":"123456789"}`)
		status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
			"code": code, "kind": "join",
			"device": map[string]any{"device_id": "123456789", "uuid": "u-1", "hostname": "reception", "platform": "Windows"},
		})
		if status != http.StatusOK || !out.Success || !out.Device.Created || out.Device.ID != "123456789" || out.Record.UsesLeft != 0 {
			t.Fatalf("redeem: %d %+v", status, out)
		}
		p, err := srv.db.GetPeer("123456789")
		if err != nil || p == nil {
			t.Fatalf("no peer: %v", err)
		}
		if p.DisplayName != "Reception PC" || p.Hostname != "reception" {
			t.Fatalf("peer %+v", p)
		}
		if pending, _ := srv.db.GetConfig(pendingDevicePrefix + "123456789"); pending != "" {
			t.Fatal("the device is still pending")
		}
		// One use: the second redeem fails like a wrong code.
		status, out = onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
			"code": code, "kind": "join", "device": map[string]any{"device_id": "223456789"},
		})
		if status != http.StatusNotFound || out.Error != "invalid_or_expired" {
			t.Fatalf("second redeem: %d %+v", status, out)
		}
		if p, _ := srv.db.GetPeer("223456789"); p != nil {
			t.Fatal("the second device was admitted")
		}
	})
}

func TestMultiUseJoinCodeCountsDownAndKeepsHostnames(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "Lab", "max_uses": 2}).Code
		for i, id := range []string{"300000001", "300000002"} {
			status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
				"code": code, "kind": "join", "device": map[string]any{"device_id": id, "hostname": "lab-" + id},
			})
			if status != http.StatusOK || out.Record.UsesLeft != 1-i {
				t.Fatalf("redeem %d: %d %+v", i, status, out)
			}
			if p, _ := srv.db.GetPeer(id); p == nil || p.DisplayName != "" {
				t.Fatalf("peer %s: %+v", id, p)
			}
		}
		if status, _ := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/lookup", map[string]any{"code": code}); status != http.StatusNotFound {
			t.Fatalf("a used-up code still looks up: %d", status)
		}
	})
}

func TestRefusedDeviceSpendsNothing(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		code := makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code
		if err := srv.db.UpsertPeer(&db.Peer{ID: "400000001", UUID: "machine-a"}); err != nil {
			t.Fatal(err)
		}
		if err := srv.db.BanPeer("400000001", "test"); err != nil {
			t.Fatal(err)
		}
		status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
			"code": code, "kind": "join", "device": map[string]any{"device_id": "400000001"},
		})
		if status != http.StatusForbidden || out.Error != "device_refused" || out.Message != "Device is banned" {
			t.Fatalf("banned: %d %+v", status, out)
		}
		if err := srv.db.UpsertPeer(&db.Peer{ID: "400000002", UUID: "machine-b"}); err != nil {
			t.Fatal(err)
		}
		status, out = onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
			"code": code, "kind": "join", "device": map[string]any{"device_id": "400000002", "uuid": "machine-c"},
		})
		if status != http.StatusForbidden || out.Error != "device_refused" {
			t.Fatalf("other machine: %d %+v", status, out)
		}
		status, out = onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/lookup", map[string]any{"code": code})
		if status != http.StatusOK || out.Record.UsesLeft != 1 {
			t.Fatalf("the code was spent: %d %+v", status, out)
		}
		status, out = onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
			"code": code, "kind": "join", "device": map[string]any{"device_id": "not an id/"},
		})
		if status != http.StatusBadRequest || out.Error != "invalid_device" {
			t.Fatalf("bad id: %d %+v", status, out)
		}
	})
}

func TestConcurrentRedeemsOfAOneUseCode(t *testing.T) {
	srv := onboardServer(testSetupDB(t))
	code := makeOnboardCode(t, srv, map[string]any{"kind": "support"}).Code
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{"code": code, "kind": "support"})
			if status == http.StatusOK {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d redeems of a one-use code passed", wins)
	}
}

func TestPairingRoutesLeaveOnboardCodesAlone(t *testing.T) {
	srv := onboardServer(testSetupDB(t))
	join := makeOnboardCode(t, srv, map[string]any{"kind": "join"})
	for i := 0; i < pairingCodeMaxLive+2; i++ {
		mintCode(t, srv, "desk")
	}
	if _, record := srv.liveOnboardRecord(join.Code); record == nil {
		t.Fatal("the pairing cap deleted a join code")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.handleListPairingCodes(rec, req)
	if strings.Contains(rec.Body.String(), join.Record.ID) {
		t.Fatal("the pairing list shows a join code")
	}
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	req.SetPathValue("id", join.Record.ID)
	rec = httptest.NewRecorder()
	srv.handleDeletePairingCode(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the pairing route revoked a join code: %d", rec.Code)
	}
}

func TestOnboardListIsPerKindAndRevokeAnswersTheRecord(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		join := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "a"})
		makeOnboardCode(t, srv, map[string]any{"kind": "support"})
		status, out := onboardCall(t, srv, http.MethodGet, "/api/onboard/codes?kind=join", nil)
		if status != http.StatusOK || len(out.Codes) != 1 || out.Codes[0].ID != join.Record.ID {
			t.Fatalf("list: %d %+v", status, out)
		}
		status, out = onboardCall(t, srv, http.MethodDelete, "/api/onboard/codes/"+join.Record.ID, nil)
		if status != http.StatusOK || out.Record.Name != "a" || out.Record.Kind != "join" {
			t.Fatalf("revoke: %d %+v", status, out)
		}
		if status, out = onboardCall(t, srv, http.MethodDelete, "/api/onboard/codes/"+join.Record.ID, nil); status != http.StatusNotFound || out.Error != "not_found" {
			t.Fatalf("revoke again: %d %+v", status, out)
		}
	})
}

func TestTemporaryDeviceIsAdmittedAndRemoved(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/devices", map[string]any{
			"device_id": "500000001", "hostname": "laptop", "temporary": true,
		})
		if status != http.StatusOK || !out.Created {
			t.Fatalf("admit: %d %+v", status, out)
		}
		p, _ := srv.db.GetPeer("500000001")
		if p == nil || p.DeviceType != onboardDeviceType {
			t.Fatalf("peer %+v", p)
		}
		if err := srv.db.UpsertPeer(&db.Peer{ID: "500000002", DeviceType: "betterdesk"}); err != nil {
			t.Fatal(err)
		}
		status, out = onboardCall(t, srv, http.MethodPost, "/api/onboard/devices", map[string]any{
			"device_id": "500000002", "temporary": true, "display_name": "do not rename",
		})
		if status != http.StatusOK || out.Created {
			t.Fatalf("known device: %d %+v", status, out)
		}
		if status, out = onboardCall(t, srv, http.MethodDelete, "/api/onboard/devices/500000002", nil); status != http.StatusOK || out.Removed {
			t.Fatalf("removed a device of its own: %d %+v", status, out)
		}
		if p, _ := srv.db.GetPeer("500000002"); p == nil || p.DisplayName != "" {
			t.Fatalf("the known device changed: %+v", p)
		}
		if status, out = onboardCall(t, srv, http.MethodDelete, "/api/onboard/devices/500000001", nil); status != http.StatusOK || !out.Removed {
			t.Fatalf("remove: %d %+v", status, out)
		}
		if state, _ := srv.db.GetPeerIDState("500000001"); state != db.PeerIDMissing {
			t.Fatalf("the temporary device is still there: %v", state)
		}
	})
}

func TestTemporaryDeviceIsTakenOverOrKeptForGood(t *testing.T) {
	srv := onboardServer(testSetupDB(t))
	admit := func(id string, temporary bool) onboardAnswer {
		t.Helper()
		status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/devices", map[string]any{"device_id": id, "temporary": temporary})
		if status != http.StatusOK {
			t.Fatalf("admit %s: %d %+v", id, status, out)
		}
		return out
	}
	admit("600000001", true)
	if out := admit("600000001", true); !out.Created {
		t.Fatal("a second session does not own the temporary device")
	}
	code := makeOnboardCode(t, srv, map[string]any{"kind": "join"}).Code
	if status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
		"code": code, "kind": "join", "device": map[string]any{"device_id": "600000001"},
	}); status != http.StatusOK || out.Device.Created {
		t.Fatalf("join: %d %+v", status, out)
	}
	if p, _ := srv.db.GetPeer("600000001"); p == nil || p.DeviceType == onboardDeviceType {
		t.Fatalf("the joined device is still temporary: %+v", p)
	}
	if status, out := onboardCall(t, srv, http.MethodDelete, "/api/onboard/devices/600000001", nil); status != http.StatusOK || out.Removed {
		t.Fatalf("the joined device was removed: %d %+v", status, out)
	}
}
