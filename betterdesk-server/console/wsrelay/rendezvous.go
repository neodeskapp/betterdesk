package wsrelay

import (
	"google.golang.org/protobuf/proto"

	pb "github.com/unitronix/betterdesk-server/proto"
)

// The guest check reads the browser's frames as hbb.RendezvousMessage
// with the decoder the signal and relay servers use (proto.Unmarshal of
// the generated code, codec.ReadRawProto), so it sees the request the
// server acts on: of the members of the oneof only the last one counts,
// and a field whose wire type is not the one its type needs is an unknown
// field, not the request.

// rendezvousRequest is a punch_hole_request or request_relay as the check
// reads it.
type rendezvousRequest struct {
	// ID is the request's id, "" when it has none.
	ID string
}

// rendezvousMessage is what the check reads of a decoded
// hbb.RendezvousMessage: at most one of the two requests is set.
type rendezvousMessage struct {
	PunchHoleRequest *rendezvousRequest
	RequestRelay     *rendezvousRequest
}

// decodeRendezvous decodes a payload as the servers do; err is the
// decoder's refusal.
func decodeRendezvous(payload []byte) (*rendezvousMessage, error) {
	var msg pb.RendezvousMessage
	if err := proto.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}
	out := &rendezvousMessage{}
	if req := msg.GetPunchHoleRequest(); req != nil {
		out.PunchHoleRequest = &rendezvousRequest{ID: req.GetId()}
	}
	if req := msg.GetRequestRelay(); req != nil {
		out.RequestRelay = &rendezvousRequest{ID: req.GetId()}
	}
	return out, nil
}

// guestTargetMatches is the check on a rendezvous frame of a guest: a
// punch_hole_request, else a request_relay, must name the device of the
// link; a message with neither passes.
func guestTargetMatches(m *rendezvousMessage, peerID string) bool {
	if m == nil || peerID == "" {
		return false
	}
	req := m.PunchHoleRequest
	if req == nil {
		req = m.RequestRelay
	}
	return req == nil || req.ID == peerID
}
