package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
)

// TestClientPostsNeedTheRegisteredUUID: the heartbeat, the system
// information and the audit records of the RustDesk client API on the API
// port are taken only with the uuid the device registered with (stored as
// hex by the signal server, posted in base64 by stock clients); without
// it, or with another, the device is answered as unknown and nothing is
// written, also for a banned device (RV2, RV7, A79).
func TestClientPostsNeedTheRegisteredUUID(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	machine := []byte("4c4c4544-0042-3510-8048-b4c04f4a4e31")
	stored := fmt.Sprintf("%x", machine)
	right := base64.StdEncoding.EncodeToString(machine)
	wrong := base64.StdEncoding.EncodeToString([]byte("another-machine"))
	for _, p := range []*db.Peer{
		{ID: "PEERAAA1", UUID: stored, Hostname: "host-a", Status: "OFFLINE"},
		{ID: "PEERBAN9", UUID: stored, Hostname: "host-ban", Status: "OFFLINE"},
	} {
		if err := database.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.BanPeer("PEERBAN9", "test"); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) (int, string) {
		return listenerCall(t, srv, http.MethodPost, path, body, nil)
	}
	status := func(id string) string {
		p, err := database.GetPeer(id)
		if err != nil || p == nil {
			t.Fatalf("load %s: %v", id, err)
		}
		return p.Status
	}

	for _, uuid := range []string{"", wrong} {
		withUUID := ""
		if uuid != "" {
			withUUID = `,"uuid":"` + uuid + `"`
		}
		if _, body := post("/api/heartbeat", `{"id":"PEERAAA1"`+withUUID+`}`); strings.Contains(body, "sysinfo") || status("PEERAAA1") != "OFFLINE" {
			t.Errorf("heartbeat with uuid %q: %s, device %s, want the unknown answer and no change", uuid, body, status("PEERAAA1"))
		}
		if _, body := post("/api/heartbeat", `{"id":"PEERBAN9"`+withUUID+`}`); strings.Contains(body, "BANNED") {
			t.Errorf("heartbeat of a banned device with uuid %q: %s, want the unknown answer", uuid, body)
		}
		if _, body := post("/api/sysinfo", `{"id":"PEERAAA1","hostname":"forged-host"`+withUUID+`}`); body != "ID_NOT_FOUND" {
			t.Errorf("sysinfo with uuid %q: %s, want ID_NOT_FOUND", uuid, body)
		}
		for _, path := range []string{"/api/audit/conn", "/api/audit/file", "/api/audit/alarm"} {
			if code, body := post(path, `{"host_id":"PEERAAA1","peer_id":"PEERBBB2","alarm_type":1`+strings.Replace(withUUID, `"uuid"`, `"host_uuid"`, 1)+`}`); code != http.StatusNotFound || body != `{"error":"Unknown device"}` {
				t.Errorf("POST %s with uuid %q: %d %s, want 404 Unknown device", path, uuid, code, body)
			}
		}
	}
	if p, _ := database.GetPeer("PEERAAA1"); p.Hostname != "host-a" {
		t.Errorf("a refused sysinfo wrote the host name %q", p.Hostname)
	}
	if n, _ := database.CountAuditConnections(db.AuditFilter{}); n != 0 {
		t.Errorf("refused audit records were stored: %d connections", n)
	}

	if _, body := post("/api/heartbeat", `{"id":"PEERAAA1","uuid":"`+right+`"}`); !strings.Contains(body, "modified_at") || status("PEERAAA1") != "ONLINE" {
		t.Errorf("heartbeat with the registered uuid: %s, device %s, want it online", body, status("PEERAAA1"))
	}
	if _, body := post("/api/heartbeat", `{"id":"PEERBAN9","uuid":"`+right+`"}`); !strings.Contains(body, "BANNED") {
		t.Errorf("heartbeat of a banned device with its uuid: %s, want BANNED", body)
	}
	if _, body := post("/api/sysinfo", `{"id":"PEERAAA1","hostname":"real-host","uuid":"`+right+`"}`); body != "SYSINFO_UPDATED" {
		t.Errorf("sysinfo with the registered uuid: %s", body)
	}
	if code, body := post("/api/audit/conn", `{"host_id":"PEERAAA1","host_uuid":"`+right+`","peer_id":"PEERBBB2"}`); code != http.StatusOK {
		t.Errorf("audit record with host_uuid: %d %s", code, body)
	}
	if code, body := post("/api/audit/alarm", `{"host_id":"PEERAAA1","uuid":"`+stored+`","alarm_type":1}`); code != http.StatusOK {
		t.Errorf("alarm with the stored form of the uuid: %d %s", code, body)
	}
}
