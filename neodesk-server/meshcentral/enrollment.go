package meshcentral

import (
	"fmt"
	"log"

	"github.com/unitronix/betterdesk-server/audit"
	"github.com/unitronix/betterdesk-server/db"
)

// MeshEnrollment is a new mesh agent's request to be enrolled, with what
// the pending registrations show of it.
type MeshEnrollment struct {
	// DeviceID is the id the agent's device gets (meshPeerID).
	DeviceID string
	// NodeID is the agent's node id, the hash of its certificate's key.
	NodeID   string
	Hostname string
	IP       string
}

// EnrollmentQueue keeps new mesh agents in the pending registrations until
// someone with enrollment.approve approves or rejects them.
type EnrollmentQueue interface {
	QueueMeshAgent(req MeshEnrollment) error
}

type enrollmentQueue struct{ EnrollmentQueue }

// SetEnrollmentQueue attaches the queue for new mesh agents. Without one a
// new agent is refused.
func (g *Gateway) SetEnrollmentQueue(q EnrollmentQueue) {
	if q == nil {
		g.enroll.Store(nil)
		return
	}
	g.enroll.Store(&enrollmentQueue{q})
}

// admitAgent decides whether an agent with the mesh id of a group comes
// in. An agent whose device the server has (enrolled before, or approved
// from the pending registrations) comes in unless the device is banned,
// disabled or was enrolled with another node id. A new agent is put into the pending
// registrations and refused until it is approved; a rejected or deleted
// one is refused.
func (g *Gateway) admitAgent(ac *AgentConn) error {
	peerID := meshPeerID(ac.meshNodeID)
	p, err := g.db.GetPeer(peerID)
	if err != nil {
		return fmt.Errorf("load device %s: %w", peerID, err)
	}
	if p != nil {
		if p.Banned {
			return fmt.Errorf("mesh agent %s is banned", peerID)
		}
		if p.Disabled {
			// Coming in would also write the device as enabled again
			return fmt.Errorf("mesh agent %s is disabled", peerID)
		}
		node, err := g.db.GetConfig("mesh_node_id_" + peerID)
		if err != nil {
			return fmt.Errorf("load node id of %s: %w", peerID, err)
		}
		if (node != "" && node != ac.meshNodeID) || (p.UUID != "" && p.UUID != ac.meshNodeID) {
			return fmt.Errorf("mesh agent %s was enrolled with another node id", peerID)
		}
		return nil
	}
	state, err := g.db.GetPeerIDState(peerID)
	if err != nil {
		return fmt.Errorf("load state of %s: %w", peerID, err)
	}
	if state == db.PeerIDSoftDeleted {
		return fmt.Errorf("mesh agent %s was deleted", peerID)
	}
	rejected, err := g.db.GetConfig("rejected_device_" + peerID)
	if err != nil {
		return fmt.Errorf("load rejection of %s: %w", peerID, err)
	}
	if rejected != "" {
		return fmt.Errorf("mesh agent %s was rejected", peerID)
	}
	q := g.enroll.Load()
	if q == nil {
		return fmt.Errorf("mesh agent %s needs approval and no enrollment queue is attached", peerID)
	}
	if err := q.QueueMeshAgent(MeshEnrollment{DeviceID: peerID, NodeID: ac.meshNodeID, Hostname: ac.hostname, IP: ac.clientIP}); err != nil {
		return fmt.Errorf("queue mesh agent %s: %w", peerID, err)
	}
	if g.auditLog != nil {
		g.auditLog.Log(audit.ActionPeerUpdated, "mesh", peerID, map[string]string{
			"event": "mesh_agent_pending",
			"node":  ac.meshNodeID,
		})
	}
	log.Printf("[mesh] new agent %s from %s waits for approval", peerID, ac.clientIP)
	return fmt.Errorf("mesh agent %s waits for approval", peerID)
}
