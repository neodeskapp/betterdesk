package api

import (
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/meshcentral"
)

// maxPendingEnrollments is the most requests the enrollment queue takes
// from new mesh agents: anyone with a group's .msh file can send one.
const maxPendingEnrollments = 500

// QueueMeshAgent puts a new mesh agent into the enrollment queue
// (pending_device_<id>), where the registrations page lists it for
// approval: its device id, host name, platform "mesh" and address, with
// its node id as the uuid a later request must repeat. Approving it
// creates the agent's device; the agent comes in when it connects again.
// A new request is refused while maxPendingEnrollments wait.
func (s *Server) QueueMeshAgent(req meshcentral.MeshEnrollment) error {
	existing, err := s.db.GetConfig(pendingDevicePrefix + req.DeviceID)
	if err != nil {
		return fmt.Errorf("load pending request: %w", err)
	}
	if existing == "" {
		pending, err := s.db.ListConfigByPrefix(pendingDevicePrefix)
		if err != nil {
			return fmt.Errorf("count pending requests: %w", err)
		}
		if len(pending) >= maxPendingEnrollments {
			return errors.New("too many pending enrollment requests")
		}
	}
	if err := s.storePendingDevice(&EnrollmentRequest{
		DeviceID:   req.DeviceID,
		UUID:       req.NodeID,
		Hostname:   req.Hostname,
		Platform:   "mesh",
		DeviceType: "mesh_agent",
	}, req.IP); err != nil {
		return fmt.Errorf("store pending request: %w", err)
	}
	if existing == "" && s.eventBus != nil {
		s.eventBus.Publish(events.Event{
			Type: events.EventEnrollmentPending,
			Data: map[string]string{"device_id": req.DeviceID, "ip": req.IP},
		})
	}
	return nil
}
