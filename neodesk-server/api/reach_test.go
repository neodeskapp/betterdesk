package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/security"
)

// TestReachNamesTheRealCause: the console's viewer asks why a device it
// could not find can't be reached. Offline is only a device that has not
// registered; a revoked, blocked, banned, deleted or unknown ID and a
// refused key each have their own reason (L550, L556).
func TestReachNamesTheRealCause(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	bl := security.NewBlocklist()
	srv.SetBlocklist(bl)
	for _, p := range []*db.Peer{
		{ID: "700000001"}, {ID: "700000002"}, {ID: "700000003"}, {ID: "700000004"}, {ID: "700000005"},
	} {
		if err := database.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpdatePeerStatus("700000001", "ONLINE", "10.0.0.1", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	if err := bl.BlockID("700000002", "revoked via panel"); err != nil {
		t.Fatal(err)
	}
	if err := bl.BlockID("700000009", "revoked via panel"); err != nil {
		t.Fatal(err)
	}
	if err := database.BanPeer("700000003", "test"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeletePeer("700000004"); err != nil {
		t.Fatal(err)
	}
	srv.peers.Put(&peer.Entry{ID: "700000005", LastReg: time.Now()})

	for id, want := range map[string]string{
		"700000001": "device_offline", // a heartbeat, but not registered with the signal server
		"700000002": "device_revoked",
		"700000009": "device_revoked", // revoked and deleted: no device, still revoked
		"700000003": "device_banned",
		"700000004": "device_removed",
		"700000005": "online",
		"700000099": "device_unknown",
	} {
		status, raw := asConsole(t, srv, http.MethodGet, "/api/peers/"+id+"/reach", "")
		var out struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(raw), &out)
		if status != http.StatusOK || out.Reason != want {
			t.Errorf("%s: %d %s, want reason %s", id, status, raw, want)
		}
	}
}

// TestUnblockTakesTheIDOffTheBlocklist: an administrator unblocks a
// revoked computer from the console; the server forgets the block and
// writes it to the audit log. An ID that is not blocked is 404.
func TestUnblockTakesTheIDOffTheBlocklist(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	bl := security.NewBlocklist()
	srv.SetBlocklist(bl)
	if err := bl.BlockID("828416401", "revoked via panel"); err != nil {
		t.Fatal(err)
	}
	if status, raw := asConsole(t, srv, http.MethodPost, "/api/peers/828416401/unblock", "{}"); status != http.StatusOK {
		t.Fatalf("unblock: %d %s", status, raw)
	}
	if bl.IsIDBlocked("828416401") {
		t.Fatal("the ID is still blocked")
	}
	if status, raw := asConsole(t, srv, http.MethodPost, "/api/peers/828416401/unblock", "{}"); status != http.StatusNotFound {
		t.Fatalf("unblock again: %d %s, want 404", status, raw)
	}
}
