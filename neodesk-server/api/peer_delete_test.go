package api

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestDeleteCascadeOnlyToTheNamedLinkedPeers: a delete with cascade and
// cascade_only deletes the linked peers named there and keeps a peer linked
// that the list leaves out, as one linked after the console checked the
// account's scope on the linked peers; without cascade_only every linked
// peer goes (BUGS DE13).
func TestDeleteCascadeOnlyToTheNamedLinkedPeers(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, id := range []string{"MAIN001", "LINKED1", "LATER01", "MAIN002", "LINKED2"} {
		if err := database.UpsertPeer(&db.Peer{ID: id, Status: "OFFLINE"}); err != nil {
			t.Fatal(err)
		}
	}
	for id, main := range map[string]string{"LINKED1": "MAIN001", "LATER01": "MAIN001", "LINKED2": "MAIN002"} {
		if err := database.UpdatePeerFields(id, map[string]string{"linked_peer_id": main}); err != nil {
			t.Fatal(err)
		}
	}
	present := func(id string) bool {
		t.Helper()
		p, err := database.GetPeer(id)
		if err != nil {
			t.Fatal(err)
		}
		return p != nil && !p.SoftDeleted
	}

	status, body := asConsole(t, srv, http.MethodDelete, "/api/peers/MAIN001?cascade=true&cascade_only=LINKED1,OTHER01", "")
	if status != http.StatusOK || body != `{"cascaded":["LINKED1"],"id":"MAIN001","revoked":false,"status":"deleted"}` {
		t.Errorf("delete with cascade_only: %d %s", status, body)
	}
	if present("LINKED1") || !present("LATER01") {
		t.Errorf("after the delete with cascade_only: LINKED1 present %v, LATER01 present %v", present("LINKED1"), present("LATER01"))
	}

	status, body = asConsole(t, srv, http.MethodDelete, "/api/peers/MAIN002?cascade=true", "")
	if status != http.StatusOK || present("LINKED2") {
		t.Errorf("delete with cascade and no list: %d %s, LINKED2 present %v", status, body, present("LINKED2"))
	}
}
