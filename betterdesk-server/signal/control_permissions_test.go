package signal

import (
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/peer"
	pb "github.com/unitronix/betterdesk-server/proto"
)

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
