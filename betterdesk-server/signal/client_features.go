package signal

import (
	"context"
	"log"
	"net"
	"strings"
	"time"

	pb "github.com/unitronix/betterdesk-server/proto"
)

// The client features of a console account (the switches of the access
// sheet: remote control, file transfer, clipboard and the rest) apply to
// the remote sessions the account starts. The signal server knows the
// account behind a punch or relay request when the RustDesk client sends
// its login token, asks the console which features the account may not
// use, and tells the device in control_permissions on every message that
// starts a session there (PunchHole, RequestRelay, FetchLocalAddr).
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
// turns off on the device. RustDesk has one permission for TCP tunnels,
// so Tunnel and Port forwarding both turn it off. File transfer also
// turns off printing from the device to the controller's printer, which
// moves files the same way.
var featurePermissions = map[string][]pb.ControlPermissions_Permission{
	"remote_control":  {pb.ControlPermissions_keyboard},
	"file_transfer":   {pb.ControlPermissions_file, pb.ControlPermissions_remote_printer},
	"clipboard":       {pb.ControlPermissions_clipboard},
	"audio":           {pb.ControlPermissions_audio},
	"tunnel":          {pb.ControlPermissions_tunnel},
	"port_forwarding": {pb.ControlPermissions_tunnel},
	"terminal":        {pb.ControlPermissions_terminal},
	"reboot":          {pb.ControlPermissions_restart},
	"privacy_mode":    {pb.ControlPermissions_privacy_mode},
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
// request: the user of the client's login token, 0 when there is none,
// or accountUnknown when the token could not be looked up.
func (s *Server) controllerAccount(token string) int64 {
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

// controllerFeaturesOff returns the client features the account behind a
// request may not use, and whether the request has an account. A lookup
// that fails turns every feature off.
func (s *Server) controllerFeaturesOff(token string, raddr *net.UDPAddr) ([]string, bool) {
	lookup := s.controllerFeatures.Load()
	if lookup == nil {
		return nil, false
	}
	userID := s.controllerAccount(token)
	switch {
	case userID == 0:
		return nil, false
	case userID == accountUnknown:
		return allFeatureKeys(), true
	}
	ctx, cancel := context.WithTimeout(context.Background(), featureLookupTimeout)
	defer cancel()
	off, err := (*lookup)(ctx, userID)
	if err != nil {
		log.Printf("[signal] client features of user %d from %s: %v; every feature is off for this session", userID, raddr, err)
		return allFeatureKeys(), true
	}
	return off, true
}

// sessionPermissions is the control_permissions the device gets for a
// session the request starts: the features the account may not use
// turned off, or nil for a request without an account.
func (s *Server) sessionPermissions(token string, raddr *net.UDPAddr) *pb.ControlPermissions {
	off, _ := s.controllerFeaturesOff(token, raddr)
	return disabledPermissions(off)
}
