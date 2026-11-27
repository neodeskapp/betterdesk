package signal

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// A waiting device that tries again with every heartbeat writes one log
// line, not two or three per attempt.
func TestWaitingDeviceLogsOnceWhileItRetries(t *testing.T) {
	srv, _ := newTestSignalServer(t, config.EnrollmentModeManaged)
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	addr := udpAddr("198.51.100.90", 51000)
	for i := 0; i < 5; i++ {
		srv.handleRegisterPeer(&pb.RegisterPeer{Id: "WAITLOG1", Serial: int32(i)}, addr)
		srv.processRegisterPk(newRegisterPk("WAITLOG1"), addr.String())
	}
	lines := strings.Count(buf.String(), "WAITLOG1")
	if lines != 1 {
		t.Fatalf("%d log lines for one waiting device, want 1:\n%s", lines, buf.String())
	}
	if !strings.Contains(buf.String(), "waits for approval") {
		t.Fatalf("the line does not say the device waits for approval:\n%s", buf.String())
	}
}
