package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
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
