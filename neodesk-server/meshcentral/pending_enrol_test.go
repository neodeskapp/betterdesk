package meshcentral

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// recordedQueue is an enrollment queue that keeps the requests.
type recordedQueue struct {
	mu   sync.Mutex
	reqs []MeshEnrollment
}

func (q *recordedQueue) QueueMeshAgent(req MeshEnrollment) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reqs = append(q.reqs, req)
	return nil
}

func (q *recordedQueue) taken() []MeshEnrollment {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]MeshEnrollment(nil), q.reqs...)
}

// TestNewAgentWaitsForApproval: a new agent with the mesh id of a group is
// put into the pending registrations and refused, and becomes no device;
// once approved it comes in. A rejected, banned, disabled or deleted
// agent, one whose device was enrolled with another node id, and every new
// agent while no queue is attached are refused; a disabled agent's device
// stays disabled (RV156, A95, RV229).
func TestNewAgentWaitsForApproval(t *testing.T) {
	gw, srv := newTestGateway(t)
	queue := &recordedQueue{}
	gw.SetEnrollmentQueue(queue)
	groupID, err := NewMeshIDHex()
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.SaveGroups([]MeshGroup{{ID: "default", Name: "Default", MeshID: groupID}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(groupID)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent.ashx"
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	key, cert, nodeID := generateAgentKeyAndCert(t)
	peerID := meshPeerID(nodeID)
	if _, ok := agentHandshakeAs(t, ctx, url, key, cert, raw, "new-host"); ok {
		t.Fatal("a new agent was confirmed before approval")
	}
	if got := queue.taken(); len(got) != 1 || got[0] != (MeshEnrollment{DeviceID: peerID, NodeID: nodeID, Hostname: "new-host", IP: "127.0.0.1"}) {
		t.Fatalf("queued %+v, want the agent's request", got)
	}
	if rows := meshAgents(t, gw); len(rows) != 0 || gw.IsConnected(peerID) {
		t.Fatalf("a waiting agent became a device: %+v connected %v", rows, gw.IsConnected(peerID))
	}

	approveAgent(t, gw, nodeID)
	if _, ok := agentHandshakeAs(t, ctx, url, key, cert, raw, "new-host"); !ok {
		t.Fatal("the approved agent was refused")
	}
	for deadline := time.Now().Add(5 * time.Second); !gw.IsConnected(peerID); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the approved agent is not connected")
		}
	}
	if p, _ := gw.db.GetPeer(peerID); p == nil || p.Status != "ONLINE" || p.Hostname != "new-host" {
		t.Errorf("the approved agent's device is %+v, want it online as new-host", p)
	}

	refused := func(what string, prepare func(nodeID string)) {
		t.Helper()
		key, cert, nodeID := generateAgentKeyAndCert(t)
		prepare(nodeID)
		before := len(queue.taken())
		if _, ok := agentHandshakeAs(t, ctx, url, key, cert, raw, "other-host"); ok {
			t.Errorf("%s: the agent was confirmed", what)
		}
		if gw.IsConnected(meshPeerID(nodeID)) {
			t.Errorf("%s: the agent is connected", what)
		}
		if after := len(queue.taken()); after != before {
			t.Errorf("%s: the agent was queued", what)
		}
	}
	refused("a rejected agent", func(nodeID string) {
		if err := gw.db.SetConfig("rejected_device_"+meshPeerID(nodeID), `{"rejected":true}`); err != nil {
			t.Fatal(err)
		}
	})
	refused("a banned agent", func(nodeID string) {
		approveAgent(t, gw, nodeID)
		if err := gw.db.BanPeer(meshPeerID(nodeID), "enrollment rejected"); err != nil {
			t.Fatal(err)
		}
	})
	var disabled string
	refused("a disabled agent", func(nodeID string) {
		disabled = meshPeerID(nodeID)
		if err := gw.db.UpsertPeer(&db.Peer{ID: disabled, UUID: nodeID, DeviceType: "mesh_agent", OS: "mesh", Status: "OFFLINE", Disabled: true}); err != nil {
			t.Fatal(err)
		}
	})
	if p, _ := gw.db.GetPeer(disabled); p == nil || !p.Disabled {
		t.Errorf("the disabled agent's device after its reconnect is %+v, want it disabled", p)
	}
	refused("a deleted agent", func(nodeID string) {
		approveAgent(t, gw, nodeID)
		if err := gw.db.DeletePeer(meshPeerID(nodeID)); err != nil {
			t.Fatal(err)
		}
	})
	refused("an agent whose device has another node id", func(nodeID string) {
		if err := gw.db.UpsertPeer(&db.Peer{ID: meshPeerID(nodeID), UUID: "another-node", DeviceType: "mesh_agent", Status: "OFFLINE"}); err != nil {
			t.Fatal(err)
		}
	})
	gw.SetEnrollmentQueue(nil)
	refused("a new agent without a queue", func(string) {})
}
