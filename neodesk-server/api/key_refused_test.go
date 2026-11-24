package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestTheListDoesNotCallARefusedKeyOnline: while the signal server refuses
// a device's new key, its heartbeats do not make it online, so the device
// list says what the viewer says: no session reaches it (L335).
func TestTheListDoesNotCallARefusedKeyOnline(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	if err := database.UpsertPeer(&db.Peer{ID: "289485454", UUID: "machine-1", Status: "ONLINE", IP: "10.10.10.75"}); err != nil {
		t.Fatal(err)
	}
	from := &net.UDPAddr{IP: net.ParseIP("10.10.10.75"), Port: 41000}
	srv.peers.Put(&peer.Entry{ID: "289485454", UDPAddr: from, IP: from.String(), ConnType: peer.ConnUDP, LastReg: time.Now()})
	if !srv.peers.RefuseKey("289485454", from.IP) {
		t.Fatal("the refusal from the device's own address was not noted")
	}
	postHeartbeat(t, srv, "289485454", "machine-1", nil)
	if err := database.UpdatePeerStatus("289485454", "ONLINE", "10.10.10.75", "test"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleListPeers(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	if list[0]["live_online"] != false || list[0]["key_refused"] != true {
		t.Errorf("a device whose key is refused: live_online %v, key_refused %v", list[0]["live_online"], list[0]["key_refused"])
	}
}

// TestARefusedKeyHoldsWhileTheDeviceIsOutOfTheLiveMap: the refused
// heartbeats of a device whose key the signal server refuses do not renew
// its live entry, so the sweeper takes the entry out (writing OFFLINE,
// which stamps last_online) until the next heartbeat puts it back. In
// that gap only the signal server's note of the refusal
// (device_key_refused_<id>) says the key is refused: the device's HTTP
// heartbeat must not make it online, the list must not take the fresh
// last_online for a sign of life, and its sysinfo is refused like that of
// an unknown device, until the key is accepted or an admin approves the
// new one (L452).
func TestARefusedKeyHoldsWhileTheDeviceIsOutOfTheLiveMap(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, p := range []*db.Peer{
		{ID: "REFUSED01", UUID: "machine-r", Hostname: "front", Status: "ONLINE"},
		{ID: "TRUSTED01", UUID: "machine-t", Hostname: "back", Status: "OFFLINE"},
	} {
		if err := database.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.SetConfig("device_key_refused_REFUSED01", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	// The sweeper took the refused entry out of the map
	if err := database.UpdatePeerStatus("REFUSED01", "OFFLINE", "", "signal"); err != nil {
		t.Fatal(err)
	}
	// Each call from its own address: the client routes allow one call per
	// address a few times a second
	calls := 0
	post := func(handler http.HandlerFunc, path, body string) string {
		calls++
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", calls)
		handler(rec, req)
		return rec.Body.String()
	}
	heartbeat := func(id, uuid string) {
		post(srv.handleClientHeartbeat, "/api/heartbeat", `{"id":"`+id+`","uuid":"`+uuid+`"}`)
	}
	sysinfo := func(id, uuid string) string {
		return post(srv.handleClientSysinfo, "/api/sysinfo", `{"id":"`+id+`","uuid":"`+uuid+`","hostname":"changed"}`)
	}
	online := func() map[string]any {
		rec := httptest.NewRecorder()
		srv.handleListPeers(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
		var list []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("list %d %s: %v", rec.Code, rec.Body.String(), err)
		}
		out := map[string]any{}
		for _, p := range list {
			out[p["id"].(string)] = p["live_online"]
		}
		return out
	}

	if got := online()["REFUSED01"]; got != false {
		t.Errorf("right after the sweep the refused device is live_online %v", got)
	}
	heartbeat("REFUSED01", "machine-r")
	heartbeat("TRUSTED01", "machine-t")
	if got := online(); got["REFUSED01"] != false || got["TRUSTED01"] != true {
		t.Errorf("after HTTP heartbeats: refused %v, trusted %v; want false, true", got["REFUSED01"], got["TRUSTED01"])
	}
	if p, _ := database.GetPeer("REFUSED01"); p == nil || p.Status == "ONLINE" {
		t.Errorf("the refused device's heartbeat set its status %v", p)
	}
	if got := sysinfo("REFUSED01", "machine-r"); got != "ID_NOT_FOUND" {
		t.Errorf("sysinfo of the refused device: %q, want ID_NOT_FOUND", got)
	}
	if p, _ := database.GetPeer("REFUSED01"); p == nil || p.Hostname != "front" {
		t.Errorf("the refused device's sysinfo was stored: %+v", p)
	}
	if got := sysinfo("TRUSTED01", "machine-t"); got != "SYSINFO_UPDATED" {
		t.Errorf("sysinfo of a trusted device: %q", got)
	}

	// Approved or registered with the stored key: the note goes, and the
	// device counts again
	if err := database.DeleteConfig("device_key_refused_REFUSED01"); err != nil {
		t.Fatal(err)
	}
	heartbeat("REFUSED01", "machine-r")
	if got := online()["REFUSED01"]; got != true {
		t.Errorf("after the refusal ended: live_online %v", got)
	}
}
