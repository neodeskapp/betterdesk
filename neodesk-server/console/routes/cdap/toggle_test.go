package cdap_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/cdap"
)

// configServer is an API server that answers the read and the save of
// cdap_enabled each in its own way and notes the calls.
type configServer struct {
	mu    sync.Mutex
	calls []string
	read  string
	save  int
}

func (s *configServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, s.read)
		return
	}
	w.WriteHeader(s.save)
	if s.save != http.StatusOK {
		_, _ = io.WriteString(w, `{"error":"failed to save"}`)
		return
	}
	_, _ = io.WriteString(w, `{"success":true}`)
}

func (s *configServer) took() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := strings.Join(s.calls, ",")
	s.calls = nil
	return out
}

// The gateway switch adds the change to the restart transaction before it
// saves the setting (BUGS RC4): while another operator's transaction is
// pending the switch is refused with 409 and cdap_enabled is not written,
// and a save the API server refuses is answered with its error.
func TestToggleWritesNothingWhenTheTransactionIsTaken(t *testing.T) {
	api := &configServer{read: `{"key":"cdap_enabled","value":"N"}`, save: http.StatusOK}
	dataDir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(dataDir, ".restart-transaction.json")
	w := proxytest.NewWorld(t, func(d *kit.Deps) {
		d.GoAPI = goapi.New(api)
		d.Restart = restart.New(restart.Options{DataDir: dataDir, GoAPI: d.GoAPI})
		cdap.Register(d)
	})
	body := `{"enabled":true}`
	toggle := func(who *proxytest.Account) (int, string) {
		rec, _ := w.Do(proxytest.Request{Method: "POST", Path: "/api/cdap/toggle", Session: who, Type: "application/json", Body: &body}, nil)
		return rec.Code, rec.Body.String()
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taken := `{"id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","userId":"1","phase":"pending","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z","changes":[{"key":"connection-mode","label":"Connection mode","rollback":{"type":"connection-mode","settings":{}}}]}`
	if err := os.WriteFile(statePath, []byte(taken), 0o600); err != nil {
		t.Fatal(err)
	}

	status, answer := toggle(account(3, "server_admin"))
	if status != http.StatusConflict || answer != `{"success":false,"error":"Failed to toggle CDAP"}` {
		t.Errorf("toggle while another operator's transaction is pending: %d %s", status, answer)
	}
	if calls := api.took(); calls != "GET /api/config/cdap_enabled" {
		t.Errorf("the API server got %q; want the read only", calls)
	}
	if state, _ := os.ReadFile(statePath); string(state) != taken {
		t.Errorf("the other operator's transaction changed: %s", state)
	}

	// The operator's own transaction takes the change, and the save goes
	// out after it
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if status, answer := toggle(account(3, "server_admin")); status != http.StatusOK || !strings.Contains(answer, `"restart_required":true`) {
		t.Errorf("toggle: %d %s", status, answer)
	}
	if calls := api.took(); calls != "GET /api/config/cdap_enabled,PUT /api/config/cdap_enabled" {
		t.Errorf("the API server got %q; want the read and the save", calls)
	}

	// A save the API server refuses is the answer
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	api.save = http.StatusInternalServerError
	status, answer = toggle(account(3, "server_admin"))
	if status != http.StatusInternalServerError || answer != `{"success":false,"error":"failed to save"}` {
		t.Errorf("toggle with a refused save: %d %s", status, answer)
	}
}

// A save the API server refuses leaves the operator's restart transaction
// as it was (BUGS RC4): no transaction is started, no change is added to
// one that is pending, and the way back of an earlier switch stays.
func TestARefusedSaveLeavesTheTransactionAsItWas(t *testing.T) {
	api := &configServer{read: `{"key":"cdap_enabled","value":"N"}`, save: http.StatusInternalServerError}
	dataDir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(dataDir, ".restart-transaction.json")
	w := proxytest.NewWorld(t, func(d *kit.Deps) {
		d.GoAPI = goapi.New(api)
		d.Restart = restart.New(restart.Options{DataDir: dataDir, GoAPI: d.GoAPI})
		cdap.Register(d)
	})
	toggle := func(enabled string) (int, string) {
		body := `{"enabled":` + enabled + `}`
		rec, _ := w.Do(proxytest.Request{Method: "POST", Path: "/api/cdap/toggle", Session: account(3, "server_admin"), Type: "application/json", Body: &body}, nil)
		return rec.Code, rec.Body.String()
	}
	state := func() string {
		file, err := os.ReadFile(statePath)
		if os.IsNotExist(err) {
			return "(no file)"
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(file)
	}

	// no transaction yet: none is started
	if status, answer := toggle("true"); status != http.StatusInternalServerError || answer != `{"success":false,"error":"failed to save"}` {
		t.Errorf("toggle with a refused save: %d %s", status, answer)
	}
	if got := state(); got != "(no file)" {
		t.Errorf("a refused save started a transaction:\n%s", got)
	}

	// a pending transaction of the operator gets no change
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pending := `{"id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","userId":"3","phase":"pending","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z","changes":[{"key":"connection-mode","label":"Connection mode","rollback":{"type":"connection-mode","settings":{}}}]}`
	if err := os.WriteFile(statePath, []byte(pending), 0o600); err != nil {
		t.Fatal(err)
	}
	toggle("true")
	if got := state(); got != pending {
		t.Errorf("a refused save changed the pending transaction:\n%s", got)
	}

	// a switch the API server took, then one it refused: the way back is
	// still the value before the first
	api.save = http.StatusOK
	if status, _ := toggle("true"); status != http.StatusOK {
		t.Fatalf("toggle: %d", status)
	}
	took := state()
	api.read, api.save = `{"key":"cdap_enabled","value":"Y"}`, http.StatusInternalServerError
	toggle("false")
	if got := state(); got != took {
		t.Errorf("a refused save changed the transaction:\n%s\nwant\n%s", got, took)
	}
	if !strings.Contains(took, `"key": "cdap-enabled"`) || !strings.Contains(took, `"value": "N"`) {
		t.Errorf("the transaction after the first switch:\n%s", took)
	}
}
