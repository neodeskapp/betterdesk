package signal

import (
	"context"
	"log"
	"net"
	"slices"
	"strings"
	"time"

	pb "github.com/unitronix/betterdesk-server/proto"
)

// The client features of a console account (the switches of the access
// sheet: remote control, file transfer, clipboard and the rest) apply to
// the remote sessions the account starts. The signal server knows the
// account behind a punch or relay request when the RustDesk client sends
// its login token, or when the request comes from the console's web
// client of a signed-in account (the account its panel proof names, see
// panel_proof.go). It asks the console which features the account may not
// use, and tells the device in control_permissions on every message that
// starts a session there (PunchHole, RequestRelay, FetchLocalAddr). A
// request for a kind of session the account may not open (file transfer,
// terminal, TCP tunnel) is refused right away (connTypeFeatures).
// RustDesk 1.4.5 and later apply control_permissions on the controlled
// side over the device's own settings, so the server only ever sends
// "off": a feature the account may use is left to the device. A request
// without an account gets no control_permissions.

// ControllerFeatures returns the keys of the client features (package
// console/access, ClientFeatures) the console account with the id may
// not use.
type ControllerFeatures func(ctx context.Context, userID int64) ([]string, error)

// SetControllerFeatures sets how the server looks up the client features
// of an account. Until it is set, no session gets control_permissions.
func (s *Server) SetControllerFeatures(f ControllerFeatures) {
	if f == nil {
		s.controllerFeatures.Store(nil)
		return
	}
	s.controllerFeatures.Store(&f)
}

// featureLookupTimeout bounds one lookup of an account's features.
const featureLookupTimeout = 3 * time.Second

// featurePermissions are the session permissions each client feature
// turns off on the device. Tunnels and port forwarding are one RustDesk
// permission, so one switch. File transfer also turns off printing from
// the device to the controller's printer, which moves files the same way.
var featurePermissions = map[string][]pb.ControlPermissions_Permission{
	"remote_control": {pb.ControlPermissions_keyboard},
	"file_transfer":  {pb.ControlPermissions_file, pb.ControlPermissions_remote_printer},
	"clipboard":      {pb.ControlPermissions_clipboard},
	"audio":          {pb.ControlPermissions_audio},
	"tunnel":         {pb.ControlPermissions_tunnel},
	"terminal":       {pb.ControlPermissions_terminal},
	"reboot":         {pb.ControlPermissions_restart},
	"privacy_mode":   {pb.ControlPermissions_privacy_mode},
}

// permissionOff is the two-bit value of control_permissions that turns a
// permission off (0 leaves it to the device, 2 would turn it on).
const permissionOff = 1

// disabledPermissions is the control_permissions that turns off the
// permissions of the features in off. Keys without a permission are left
// out; nil when nothing is turned off.
func disabledPermissions(off []string) *pb.ControlPermissions {
	var bits uint64
	for _, key := range off {
		for _, p := range featurePermissions[key] {
			bits |= permissionOff << (2 * uint(p))
		}
	}
	if bits == 0 {
		return nil
	}
	return &pb.ControlPermissions{Permissions: bits}
}

// allFeatureKeys are the keys of every feature with a session
// permission: what an account gets when its features cannot be looked up.
func allFeatureKeys() []string {
	keys := make([]string, 0, len(featurePermissions))
	for key := range featurePermissions {
		keys = append(keys, key)
	}
	return keys
}

// accountUnknown is the account of a request whose account the server
// could not look up.
const accountUnknown int64 = -1

// controllerAccount is the console account behind a punch or relay
// request: on the web client's connection (panel) the account its proof
// named, else the user of the client's login token; 0 when there is none,
// accountUnknown when the token could not be looked up.
func (s *Server) controllerAccount(token string, raddr *net.UDPAddr, panel bool) int64 {
	if panel {
		return s.panelAccount(raddr)
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" || s.db == nil || !opaqueClientTokenRegexp.MatchString(token) {
		return 0
	}
	sess, err := s.db.GetClientSessionByTokenHash(hashOpaqueClientToken(token))
	if err != nil {
		log.Printf("[signal] client features: token lookup failed: %v", err)
		return accountUnknown
	}
	if sess == nil {
		return 0
	}
	return sess.UserID
}

// featuresOff returns the client features the account behind a request
// (controllerAccount) may not use; none for a request without an account.
// A lookup that fails turns every feature off.
func (s *Server) featuresOff(userID int64, raddr *net.UDPAddr) []string {
	lookup := s.controllerFeatures.Load()
	if lookup == nil {
		return nil
	}
	switch {
	case userID == 0:
		return nil
	case userID == accountUnknown:
		return allFeatureKeys()
	}
	ctx, cancel := context.WithTimeout(context.Background(), featureLookupTimeout)
	defer cancel()
	off, err := (*lookup)(ctx, userID)
	if err != nil {
		log.Printf("[signal] client features of user %d from %s: %v; every feature is off for this session", userID, raddr, err)
		return allFeatureKeys()
	}
	return off
}

// connTypeFeatures are the features a kind of session needs, by the
// connection type the controller names in its request. An unmodified
// RustDesk app names the kind it opens, so the server refuses it with a
// message the app shows, also for devices that ignore
// control_permissions. An app that names another kind is still held by
// the device's own check.
var connTypeFeatures = map[pb.ConnType][]string{
	pb.ConnType_FILE_TRANSFER: {"file_transfer"},
	pb.ConnType_PORT_FORWARD:  {"tunnel"},
	pb.ConnType_RDP:           {"tunnel"},
	pb.ConnType_TERMINAL:      {"terminal"},
}

// featureRefusals are the messages of refused requests, by feature.
var featureRefusals = map[string]string{
	"file_transfer": "File transfer is turned off for your account on this server",
	"tunnel":        "TCP tunnels are turned off for your account on this server",
	"terminal":      "The terminal is turned off for your account on this server",
}

// sessionPolicy is what the server does with a session a request starts.
type sessionPolicy struct {
	// account is the console account behind the request
	// (controllerAccount), which billing records as the session's operator.
	account int64
	// permissions go to the device: the features the account may not
	// use turned off, nil for a request without an account.
	permissions *pb.ControlPermissions
	// refusal is the message of a request the server refuses, feature
	// the feature it needs; "" lets the request through.
	refusal, feature string
}

// sessionPolicy decides a punch or relay request for connType.
func (s *Server) sessionPolicy(token string, raddr *net.UDPAddr, panel bool, connType pb.ConnType) sessionPolicy {
	account := s.controllerAccount(token, raddr, panel)
	off := s.featuresOff(account, raddr)
	policy := sessionPolicy{account: account, permissions: disabledPermissions(off)}
	for _, need := range connTypeFeatures[connType] {
		if slices.Contains(off, need) {
			policy.refusal, policy.feature = featureRefusals[need], need
			break
		}
	}
	return policy
}

// refuseForFeature logs and audits a request refused by sessionPolicy.
func (s *Server) refuseForFeature(raddr *net.UDPAddr, initiatorID, targetID string, policy sessionPolicy) {
	s.logUnauthorizedInitiatorDetails(raddr, initiatorID, targetID, "initiator_feature_off", map[string]string{"feature": policy.feature})
}

// featureRefusedPunchResponse answers a refused punch request: no address
// and the message, which the RustDesk app shows.
func featureRefusedPunchResponse(refusal string) *pb.RendezvousMessage {
	return &pb.RendezvousMessage{
		Union: &pb.RendezvousMessage_PunchHoleResponse{
			PunchHoleResponse: &pb.PunchHoleResponse{OtherFailure: refusal},
		},
	}
}
