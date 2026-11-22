package wsrelay

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

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
	// WebClient says the message is one the web client sends: a
	// punch_hole_request, a request_relay or a health check, with no field
	// the schema does not know at any depth. A key_exchange is not: after
	// it the signal server reads the connection as ciphertext under a key
	// the client chose, which the check cannot read.
	WebClient bool
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
	switch msg.GetUnion().(type) {
	case *pb.RendezvousMessage_PunchHoleRequest, *pb.RendezvousMessage_RequestRelay, *pb.RendezvousMessage_Hc:
		out.WebClient = !hasUnknownFields(msg.ProtoReflect())
	}
	return out, nil
}

// hasUnknownFields reports whether m or a message inside it holds a field
// the schema does not know.
func hasUnknownFields(m protoreflect.Message) bool {
	if len(m.GetUnknown()) > 0 {
		return true
	}
	found := false
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					found = hasUnknownFields(mv.Message())
					return !found
				})
			}
		case fd.IsList():
			if fd.Message() != nil {
				for i := 0; i < v.List().Len() && !found; i++ {
					found = hasUnknownFields(v.List().Get(i).Message())
				}
			}
		case fd.Message() != nil:
			found = hasUnknownFields(v.Message())
		}
		return !found
	})
	return found
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
