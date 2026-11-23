package billingclock

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// On the documented Docker install the console runs in the server's own
// container without NEODESK_DOCKER_LAYOUT, and its .env file can't be
// written (EACCES on /app/.env) and is read by nobody. The settings go to
// the server, which keeps them in its database and applies them at once,
// and a read shows what the server uses (L400).
func TestClockSettingsInAContainerGoToTheServer(t *testing.T) {
	// A folder that isn't there: writing the file fails as /app/.env did
	envPath := filepath.Join(t.TempDir(), "app", ".env")
	stub := &apiStub{answer: &apiAnswer{Data: json.RawMessage(`{"config":{"ntp_servers":"time.example.org","max_skew_ms":1500,` +
		`"require_synced_clock":false,"trust_os_ntp":true,"billing_timezone":"Europe/Berlin"},"status":{"synced":true}}`)}}
	s := New(Options{
		EnvPath:  envPath,
		Docker:   true,
		GoAPI:    goapi.New(stub),
		HostZone: func() string { return "UTC" },
		TZ:       func() string { return "" },
	})
	if !s.SavesThroughServer() {
		t.Fatal("a console in a container does not save through the server")
	}
	body := goapi.NewObject("ntp_servers", "time.example.org", "max_skew_ms", 1500.0, "require_synced_clock", false,
		"trust_os_ntp", true, "billing_timezone", "Europe/Berlin")
	result, err := s.SaveClockSettings(context.Background(), body)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !result.DockerMode || result.Settings.BillingTimezone != "Europe/Berlin" || result.Settings.MaxSkewMS != 1500 {
		t.Errorf("saved %+v", result)
	}
	if len(stub.calls) != 1 || stub.calls[0].Method != "PUT" || stub.calls[0].Path != "/timesync/config" {
		t.Fatalf("calls %+v, want one PUT /timesync/config", stub.calls)
	}
	if _, err := os.Stat(envPath); err == nil {
		t.Error("an .env file was written")
	}

	stub.calls = nil
	stub.answer = &apiAnswer{Data: json.RawMessage(`{"ntp_servers":"time.example.org","max_skew_ms":1500,` +
		`"require_synced_clock":false,"trust_os_ntp":true,"billing_timezone":"Europe/Berlin"}`)}
	got, err := s.GetRuntimeClockSettings(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.NTPServers != "time.example.org" || got.MaxSkewMS != 1500 || got.RequireSyncedClock || got.BillingTimezone != "Europe/Berlin" {
		t.Errorf("read %+v, want what the server reports", got)
	}
	if len(stub.calls) != 1 || stub.calls[0].Method != "GET" || stub.calls[0].Path != "/timesync/config" {
		t.Errorf("calls %+v, want one GET /timesync/config", stub.calls)
	}
}
