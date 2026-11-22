package api

import (
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
)

// TestViewOnlyGuestLinksKeepToMeshAndCDAPDevices: a view_only guest link is
// refused for a RustDesk device, where the server cannot keep the guest
// from controlling it, also when only one of its devices is one; mesh
// agents and CDAP devices keep it, and a link with control is unchanged
// (RV209, A94).
func TestViewOnlyGuestLinksKeepToMeshAndCDAPDevices(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.UpsertPeer(&db.Peer{ID: "MMESHAGENT0001", Hostname: "mesh-host", DeviceType: "mesh_agent", Status: "OFFLINE"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "plc-7", Hostname: "plc", DeviceType: "iot", Status: "OFFLINE"}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("cdap_manifest_plc-7", `{"device":{"name":"plc","type":"iot"}}`); err != nil {
		t.Fatal(err)
	}
	create := func(body string) (int, string) {
		return asConsole(t, srv, http.MethodPost, "/api/guest/access-links", body)
	}

	for _, body := range []string{
		`{"peer_ids":["PEERAAA1"],"view_only":true}`,
		`{"peer_ids":["MMESHAGENT0001","PEERBBB2"],"view_only":true}`,
	} {
		status, answer := create(body)
		if want := `{"error":"view_only is not available for RustDesk devices: the server cannot keep a guest from controlling PEER`; status != http.StatusBadRequest || len(answer) < len(want) || answer[:len(want)] != want {
			t.Errorf("POST %s: %d %s, want 400 with the RustDesk refusal", body, status, answer)
		}
	}
	for _, body := range []string{
		`{"peer_ids":["MMESHAGENT0001"],"view_only":true}`,
		`{"peer_ids":["plc-7"],"view_only":true}`,
		`{"peer_ids":["PEERAAA1"],"view_only":false}`,
	} {
		if status, answer := create(body); status != http.StatusCreated {
			t.Errorf("POST %s: %d %s, want 201", body, status, answer)
		}
	}
}
