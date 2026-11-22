package signal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
	pb "github.com/unitronix/betterdesk-server/proto"
)

// signedInController makes an account with a client login session and
// returns the account's id and the session's token.
func signedInController(t *testing.T, database db.Database, name string) (int64, string) {
	t.Helper()
	user := &db.User{Username: name, PasswordHash: "hash", Role: "operator"}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token := strings.Repeat(hex.EncodeToString([]byte{byte(user.ID)}), 32)
	sum := sha256.Sum256([]byte(token))
	if err := database.CreateClientSession(&db.ClientSession{
		TokenHash: hex.EncodeToString(sum[:]),
		UserID:    user.ID,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02 15:04:05"),
		CreatedAt: time.Now().UTC().Format("2006-01-02 15:04:05"),
	}); err != nil {
		t.Fatalf("CreateClientSession: %v", err)
	}
	return user.ID, token
}

// featuresOf answers SetControllerFeatures from a fixed list per account.
func featuresOf(off map[int64][]string) ControllerFeatures {
	return func(_ context.Context, userID int64) ([]string, error) {
		return off[userID], nil
	}
}

// sessionPath is a request that starts a session at the target.
type sessionPath struct {
	name string
	run  func(srv *Server, target, token string, initiator *net.UDPAddr)
}

var sessionPaths = []sessionPath{
	{"punch hole over UDP", func(srv *Server, target, token string, initiator *net.UDPAddr) {
		srv.handlePunchHoleRequest(&pb.PunchHoleRequest{Id: target, Token: token}, initiator)
	}},
	{"punch hole over TCP", func(srv *Server, target, token string, initiator *net.UDPAddr) {
		srv.handlePunchHoleRequestTCP(&pb.PunchHoleRequest{Id: target, Token: token}, initiator)
	}},
	{"forced relay of a UDP punch hole", func(srv *Server, target, token string, initiator *net.UDPAddr) {
		srv.handlePunchHoleRequest(&pb.PunchHoleRequest{Id: target, Token: token, ForceRelay: true}, initiator)
	}},
	{"relay over UDP", func(srv *Server, target, token string, initiator *net.UDPAddr) {
		srv.handleRequestRelay(&pb.RequestRelay{Id: target, Uuid: "features-relay", Token: token}, initiator)
	}},
	{"relay over TCP", func(srv *Server, target, token string, initiator *net.UDPAddr) {
		srv.handleRequestRelayTCP(&pb.RequestRelay{Id: target, Uuid: "features-relay", Token: token}, initiator, peer.ConnTCP)
	}},
}

// permissionsAtTarget reads the next session message of the target and
// returns its control_permissions.
func permissionsAtTarget(t *testing.T, recv *net.UDPConn) *pb.ControlPermissions {
	t.Helper()
	msg := readUDPRendezvous(t, recv)
	switch {
	case msg.GetPunchHole() != nil:
		return msg.GetPunchHole().GetControlPermissions()
	case msg.GetRequestRelay() != nil:
		return msg.GetRequestRelay().GetControlPermissions()
	case msg.GetFetchLocalAddr() != nil:
		return msg.GetFetchLocalAddr().GetControlPermissions()
	}
	t.Fatalf("the target got %v, not a session message", msg)
	return nil
}

// bitsOff is control_permissions with the permissions turned off.
func bitsOff(permissions ...pb.ControlPermissions_Permission) uint64 {
	var bits uint64
	for _, p := range permissions {
		bits |= 1 << (2 * uint(p))
	}
	return bits
}

// A session a signed-in controller starts carries the features its
// account may not use as "off" to the device, on every path that starts
// one; nothing is ever turned on.
func TestSessionsOfASignedInControllerTurnItsFeaturesOff(t *testing.T) {
	want := bitsOff(pb.ControlPermissions_file, pb.ControlPermissions_remote_printer, pb.ControlPermissions_clipboard)
	for _, path := range sessionPaths {
		t.Run(path.name, func(t *testing.T) {
			srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
			srv.cfg.P2PFirst = false
			srv.localIP.Store("198.51.100.20")
			recv, targetAddr := attachTestUDPPair(t, srv)
			putOnlinePeer(srv, "FEATTGT01", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
			userID, token := signedInController(t, database, "feat-op")
			srv.SetControllerFeatures(featuresOf(map[int64][]string{userID: {"file_transfer", "clipboard", "client_actions"}}))

			path.run(srv, "FEATTGT01", token, udpAddr("203.0.113.71", 51000))
			got := permissionsAtTarget(t, recv)
			if got.GetPermissions() != want {
				t.Fatalf("control_permissions = %#x, want %#x", got.GetPermissions(), want)
			}
		})
	}
}

// The LAN address exchange carries them too.
func TestLANExchangeOfASignedInControllerTurnsItsFeaturesOff(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	srv.cfg.SameNATRelay = false
	srv.cfg.P2PFallbackMs = 500
	recv, targetAddr := attachTestUDPPair(t, srv)
	_, initiatorAddr := openLANInitiator(t)
	t.Cleanup(func() { srv.cancelPendingLAN(pendingLANKey("FEATLAN01", initiatorAddr)) })
	putOnlinePeer(srv, "FEATLAN01", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
	userID, token := signedInController(t, database, "feat-lan")
	srv.SetControllerFeatures(featuresOf(map[int64][]string{userID: {"remote_control", "terminal"}}))

	srv.handlePunchHoleRequest(&pb.PunchHoleRequest{Id: "FEATLAN01", Token: token}, initiatorAddr)
	fetch := readUDPRendezvous(t, recv).GetFetchLocalAddr()
	if fetch == nil {
		t.Fatal("expected FetchLocalAddr at the target")
	}
	if want := bitsOff(pb.ControlPermissions_keyboard, pb.ControlPermissions_terminal); fetch.GetControlPermissions().GetPermissions() != want {
		t.Fatalf("control_permissions = %#x, want %#x", fetch.GetControlPermissions().GetPermissions(), want)
	}
}

// What control_permissions a session gets for each kind of controller:
// the device decides alone without an account or with every feature on,
// and every feature is off when the account's features cannot be looked
// up.
func TestSessionPermissionsByController(t *testing.T) {
	allOff := bitsOff(pb.ControlPermissions_keyboard, pb.ControlPermissions_file, pb.ControlPermissions_remote_printer,
		pb.ControlPermissions_clipboard, pb.ControlPermissions_audio, pb.ControlPermissions_tunnel,
		pb.ControlPermissions_terminal, pb.ControlPermissions_restart, pb.ControlPermissions_privacy_mode)
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	addr := udpAddr("203.0.113.72", 51000)
	userID, token := signedInController(t, database, "feat-kinds")

	if got := srv.sessionPermissions(token, addr); got != nil {
		t.Errorf("before the console sets the lookup: %#x", got.GetPermissions())
	}
	srv.SetControllerFeatures(featuresOf(map[int64][]string{userID: {"client_actions"}}))
	if got := srv.sessionPermissions(token, addr); got != nil {
		t.Errorf("every feature on: %#x", got.GetPermissions())
	}
	if got := srv.sessionPermissions("", addr); got != nil {
		t.Errorf("no token: %#x", got.GetPermissions())
	}
	if got := srv.sessionPermissions(strings.Repeat("cd", 32), addr); got != nil {
		t.Errorf("a token nobody holds: %#x", got.GetPermissions())
	}
	everything := []string{}
	for _, f := range access.ClientFeatures {
		everything = append(everything, f.Key)
	}
	srv.SetControllerFeatures(featuresOf(map[int64][]string{userID: everything}))
	if got := srv.sessionPermissions(token, addr).GetPermissions(); got != allOff {
		t.Errorf("every feature off: %#x, want %#x", got, allOff)
	}
	srv.SetControllerFeatures(func(context.Context, int64) ([]string, error) { return nil, errors.New("store down") })
	if got := srv.sessionPermissions(token, addr).GetPermissions(); got != allOff {
		t.Errorf("failed lookup: %#x, want %#x", got, allOff)
	}
	srv.SetControllerFeatures(nil)
	if got := srv.sessionPermissions(token, addr); got != nil {
		t.Errorf("after the console stopped: %#x", got.GetPermissions())
	}
}

// Every switch of the access sheet that is not locked turns off at least
// one session permission, so no switch is saved without an effect on
// RustDesk sessions.
func TestEverySwitchTurnsOffASessionPermission(t *testing.T) {
	for _, feature := range access.ClientFeatures {
		if feature.HardLock {
			continue
		}
		if len(featurePermissions[feature.Key]) == 0 {
			t.Errorf("switch %q turns off no session permission", feature.Key)
		}
	}
}

// enableAll is a control_permissions value that turns every session
// permission on: two bits per permission, 2 meaning enable.
const enableAll uint64 = 0xAAAAAAAAAAAAAAAA

// The device gets the relay request without the controller's own
// control_permissions and login token, on the UDP and the TCP path: a
// controller cannot switch on a feature the device turned off, and the
// device never sees the controller's token.
func TestRelayRequestToTheDeviceDropsTheControllersPermissionsAndToken(t *testing.T) {
	for _, path := range []string{"udp", "tcp"} {
		t.Run(path, func(t *testing.T) {
			srv, _ := newTestSignalServer(t, config.EnrollmentModeOpen)
			srv.localIP.Store("198.51.100.20")
			recv, targetAddr := attachTestUDPPair(t, srv)
			putOnlinePeer(srv, "PERMTGT01", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
			putOnlinePeer(srv, "PERMINIT1", "203.0.113.70", 51000, peer.ConnTCP)
			req := &pb.RequestRelay{
				Id:                 "PERMTGT01",
				Uuid:               "perm-relay-uuid",
				Token:              "controller-login-token",
				ControlPermissions: &pb.ControlPermissions{Permissions: enableAll},
			}
			if path == "udp" {
				srv.handleRequestRelay(req, udpAddr("203.0.113.70", 51000))
			} else {
				srv.handleRequestRelayTCP(req, udpAddr("203.0.113.70", 51000), peer.ConnTCP)
			}
			got := readUDPRendezvous(t, recv).GetRequestRelay()
			if got == nil {
				t.Fatal("the device got no relay request")
			}
			if got.GetUuid() != "perm-relay-uuid" {
				t.Fatalf("uuid = %q", got.GetUuid())
			}
			if got.GetControlPermissions() != nil {
				t.Errorf("the device got the controller's control_permissions %#x", got.GetControlPermissions().GetPermissions())
			}
			if got.GetToken() != "" {
				t.Errorf("the device got the controller's token %q", got.GetToken())
			}
		})
	}
}
