package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A stock RustDesk client sends its system information before it is
// approved. The waiting request then shows its name, system and version,
// but only from system information with the uuid of the request's
// RegisterPk; the answer stays ID_NOT_FOUND.
func TestWaitingRequestShowsTheNameFromSysinfo(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	machine := []byte("4c4c4544-0042-3510-8048-b4c04f4a4e31")
	right := base64.StdEncoding.EncodeToString(machine)
	wrong := base64.StdEncoding.EncodeToString([]byte("another-machine"))
	if err := database.SetConfig(pendingDevicePrefix+"WAITNAME1", fmt.Sprintf(
		`{"device_id":"WAITNAME1","hostname":"","platform":"","version":"","ip":"198.51.100.7","created_at":"2026-11-27T00:00:00Z","register_uuid":"%x"}`, machine)); err != nil {
		t.Fatal(err)
	}
	request := func() map[string]any {
		t.Helper()
		raw, _ := database.GetConfig(pendingDevicePrefix + "WAITNAME1")
		out := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("request %q: %v", raw, err)
		}
		return out
	}
	post := func(uuid, hostname string) string {
		_, body := listenerCall(t, srv, http.MethodPost, "/api/sysinfo",
			`{"id":"WAITNAME1","uuid":"`+uuid+`","hostname":"`+hostname+`","os":"Linux / Ubuntu 24.04","version":"1.5.3"}`, nil)
		return body
	}

	if body := post(wrong, "forged-host"); body != "ID_NOT_FOUND" || request()["hostname"] != "" {
		t.Fatalf("another uuid: %s, request %v; want ID_NOT_FOUND and no name", body, request())
	}
	if body := post(right, "office-pc"); body != "ID_NOT_FOUND" {
		t.Fatalf("waiting device's sysinfo answered %q, want ID_NOT_FOUND", body)
	}
	got := request()
	if got["hostname"] != "office-pc" || got["platform"] != "Linux / Ubuntu 24.04" || got["version"] != "1.5.3" {
		t.Fatalf("request %v, want the name, system and version from sysinfo", got)
	}
	if got["ip"] != "198.51.100.7" || got["register_uuid"] == "" {
		t.Fatalf("request lost its fields: %v", got)
	}
	// The first name stays
	post(right, "renamed-pc")
	if request()["hostname"] != "office-pc" {
		t.Fatalf("a later sysinfo replaced the name: %v", request())
	}
	// The listing shows it
	pending := srv.listPendingDevices()
	if len(pending) != 1 || pending[0].Hostname != "office-pc" || pending[0].Platform != "Linux / Ubuntu 24.04" {
		t.Fatalf("pending list %+v", pending)
	}
}
