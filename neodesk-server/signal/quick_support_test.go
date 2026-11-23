package signal

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// quickSupportOffBits is what a quick support session turns off: all but
// the screen, the mouse and the keyboard.
func quickSupportOffBits(except ...pb.ControlPermissions_Permission) uint64 {
	skip := map[pb.ControlPermissions_Permission]bool{}
	for _, p := range except {
		skip[p] = true
	}
	var off []pb.ControlPermissions_Permission
	for _, p := range []pb.ControlPermissions_Permission{
		pb.ControlPermissions_remote_printer, pb.ControlPermissions_clipboard, pb.ControlPermissions_file,
		pb.ControlPermissions_audio, pb.ControlPermissions_camera, pb.ControlPermissions_terminal,
		pb.ControlPermissions_tunnel, pb.ControlPermissions_restart, pb.ControlPermissions_recording,
		pb.ControlPermissions_block_input, pb.ControlPermissions_remote_modify, pb.ControlPermissions_privacy_mode,
		pb.ControlPermissions_usb,
	} {
		if !skip[p] {
			off = append(off, p)
		}
	}
	return bitsOff(off...)
}

// TestQuickSupportGivesWhatTheConsentNames: the person allowed the
// screen, the mouse and the keyboard. Every session at their device turns
// off the rest (files, clipboard and all others), on every path that
// starts one; the keyboard stays with the device (L337).
func TestQuickSupportGivesWhatTheConsentNames(t *testing.T) {
	for _, path := range sessionPaths {
		t.Run(path.name, func(t *testing.T) {
			srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
			srv.cfg.P2PFirst = false
			srv.localIP.Store("198.51.100.20")
			recv, targetAddr := attachTestUDPPair(t, srv)
			putOnlinePeer(srv, "QUICKSUP1", targetAddr.IP.String(), targetAddr.Port, peer.ConnUDP)
			if err := database.SetConfig("quick_support_device_QUICKSUP1", `{"session":"0123456789abcdef","grants":[]}`); err != nil {
				t.Fatal(err)
			}
			_, token := signedInController(t, database, "supporter")

			path.run(srv, "QUICKSUP1", token, udpAddr("203.0.113.71", 51000))
			if got, want := permissionsAtTarget(t, recv).GetPermissions(), quickSupportOffBits(); got != want {
				t.Fatalf("control_permissions = %#x, want %#x", got, want)
			}
		})
	}
}

// TestQuickSupportLeavesOnWhatThePersonAllowed: a person who also allowed
// the clipboard and files on the consent screen gets them, and only then
// does a file transfer session start.
func TestQuickSupportLeavesOnWhatThePersonAllowed(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	addr := udpAddr("203.0.113.72", 51000)
	_, token := signedInController(t, database, "supporter2")
	if err := database.SetConfig("quick_support_device_QUICKSUP2", `{"session":"0123456789abcdef","grants":["clipboard"]}`); err != nil {
		t.Fatal(err)
	}
	policy := srv.sessionPolicy(token, addr, false, pb.ConnType_DEFAULT_CONN)
	srv.limitToQuickSupport(&policy, "QUICKSUP2", pb.ConnType_DEFAULT_CONN)
	if got, want := policy.permissions.GetPermissions(), quickSupportOffBits(pb.ControlPermissions_clipboard); got != want {
		t.Errorf("with the clipboard allowed: %#x, want %#x", got, want)
	}
	files := srv.sessionPolicy(token, addr, false, pb.ConnType_FILE_TRANSFER)
	srv.limitToQuickSupport(&files, "QUICKSUP2", pb.ConnType_FILE_TRANSFER)
	if files.refusal == "" {
		t.Error("a file transfer session the person did not allow is not refused")
	}
	if err := database.SetConfig("quick_support_device_QUICKSUP2", `{"session":"0123456789abcdef","grants":["file_transfer"]}`); err != nil {
		t.Fatal(err)
	}
	files = srv.sessionPolicy(token, addr, false, pb.ConnType_FILE_TRANSFER)
	srv.limitToQuickSupport(&files, "QUICKSUP2", pb.ConnType_FILE_TRANSFER)
	if files.refusal != "" || files.permissions.GetPermissions() != quickSupportOffBits(pb.ControlPermissions_file, pb.ControlPermissions_remote_printer) {
		t.Errorf("files allowed: refusal %q, permissions %#x", files.refusal, files.permissions.GetPermissions())
	}
	// Another device keeps what the device itself decides
	other := srv.sessionPolicy(token, addr, false, pb.ConnType_DEFAULT_CONN)
	srv.limitToQuickSupport(&other, "OTHERDEV1", pb.ConnType_DEFAULT_CONN)
	if other.permissions != nil {
		t.Errorf("a device without quick support got %#x", other.permissions.GetPermissions())
	}
}
