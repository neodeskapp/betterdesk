package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/meshcentral"
)

// TestMeshAgentEnrollmentQueue: a new mesh agent's request is listed in
// the enrollment queue with its host name, platform mesh and address;
// approving it creates the agent's device bound to its node id, a request
// with another node id under the same device id is refused, and the queue
// takes no new request while it is full (RV156, A95).
func TestMeshAgentEnrollmentQueue(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	req := meshcentral.MeshEnrollment{DeviceID: "Mabcdefghijklmno", NodeID: "abcdefghijklmnopNODE", Hostname: "lab-pc", IP: "198.51.100.4"}
	if err := srv.QueueMeshAgent(req); err != nil {
		t.Fatal(err)
	}
	if err := srv.QueueMeshAgent(req); err != nil {
		t.Fatalf("the same agent again: %v", err)
	}
	other := req
	other.NodeID = "abcdefghijklmnopOTHER"
	if err := srv.QueueMeshAgent(other); err == nil {
		t.Error("a request with another node id under the same device id was taken")
	}

	status, body := asConsole(t, srv, http.MethodGet, "/api/enrollment/pending", "")
	for _, want := range []string{`"device_id":"Mabcdefghijklmno"`, `"hostname":"lab-pc"`, `"platform":"mesh"`, `"device_type":"mesh_agent"`, `"ip":"198.51.100.4"`, `"count":1`} {
		if status != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("pending list: %d %s, want %s", status, body, want)
		}
	}

	if status, body := asConsole(t, srv, http.MethodPost, "/api/enrollment/approve/Mabcdefghijklmno", `{}`); status != http.StatusOK {
		t.Fatalf("approve: %d %s", status, body)
	}
	p, err := database.GetPeer("Mabcdefghijklmno")
	if err != nil || p == nil || p.DeviceType != "mesh_agent" || p.UUID != req.NodeID || p.Hostname != "lab-pc" {
		t.Fatalf("the approved agent's device is %+v (%v), want a mesh_agent bound to its node id", p, err)
	}

	for i := 0; i < maxPendingEnrollments; i++ {
		if err := database.SetConfig(fmt.Sprintf("%sFILL%04d", pendingDevicePrefix, i), `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.QueueMeshAgent(meshcentral.MeshEnrollment{DeviceID: "Mzzzzzzzzzzzzzz", NodeID: "zzzz", IP: "198.51.100.5"}); err == nil {
		t.Error("a full queue took a new request")
	}
}
