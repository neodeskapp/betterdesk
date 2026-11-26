package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/security"
)

// TestTheListDoesNotCallABlockedIDOnline: the signal server refuses a
// blocked ID's registration, so its HTTP heartbeats do not make it online
// in the list or on its own page, and both say why (L550).
func TestTheListDoesNotCallABlockedIDOnline(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	bl := security.NewBlocklist()
	srv.SetBlocklist(bl)
	for _, id := range []string{"828416401", "828416402"} {
		if err := database.UpsertPeer(&db.Peer{ID: id, UUID: "machine-" + id, Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		if err := database.UpdatePeerStatus(id, "ONLINE", "10.10.10.75", "heartbeat"); err != nil {
			t.Fatal(err)
		}
	}
	if err := bl.BlockID("828416401", "revoked via panel"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleListPeers(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	for _, p := range list {
		switch p["id"] {
		case "828416401":
			if p["live_online"] != false || p["blocked"] != "device_revoked" {
				t.Errorf("a revoked ID: live_online %v, blocked %v", p["live_online"], p["blocked"])
			}
		case "828416402":
			if p["live_online"] != true || p["blocked"] != nil {
				t.Errorf("a device that is not blocked: live_online %v, blocked %v", p["live_online"], p["blocked"])
			}
		}
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/peers/828416401", nil)
	req.SetPathValue("id", "828416401")
	srv.handleGetPeer(rec, req)
	var one map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("peer %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	if one["live_online"] != false || one["blocked"] != "device_revoked" {
		t.Errorf("the revoked ID's own page: live_online %v, blocked %v", one["live_online"], one["blocked"])
	}
	p, _ := database.GetPeer("828416401")
	if srv.chatPeerOnline(p) {
		t.Error("the chat calls the revoked ID online")
	}
}
