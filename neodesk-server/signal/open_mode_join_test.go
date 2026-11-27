package signal

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
)

// A device that waited in Managed mode and registers after the mode was
// switched to Open is no longer waiting: its request is closed as approved
// and it can start sessions like any other device.
func TestWaitingDeviceJoiningInOpenModeCanStartSessions(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeManaged)
	const addr = "198.51.100.80:51000"

	if got := registerPkResult(srv.processRegisterPk(newRegisterPk("OPENJOIN1"), addr)); got != pb.RegisterPkResponse_NOT_DEPLOYED {
		t.Fatalf("managed RegisterPk = %v, want NOT_DEPLOYED", got)
	}
	if pending, _ := database.GetConfig("pending_device_OPENJOIN1"); pending == "" {
		t.Fatal("managed mode should queue the device")
	}

	srv.cfg.EnrollmentMode = config.EnrollmentModeOpen
	if got := registerPkResult(srv.processRegisterPk(newRegisterPk("OPENJOIN1"), addr)); got != pb.RegisterPkResponse_OK {
		t.Fatalf("open RegisterPk = %v, want OK", got)
	}

	if pending, _ := database.GetConfig("pending_device_OPENJOIN1"); pending != "" {
		t.Fatalf("request still waiting after the device joined in open mode: %s", pending)
	}
	raw, _ := database.GetConfig("enrollment_decision_OPENJOIN1")
	var decision struct {
		DeviceID  string `json:"device_id"`
		Status    string `json:"status"`
		IP        string `json:"ip"`
		CreatedAt string `json:"created_at"`
		DecidedAt string `json:"decided_at"`
	}
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		t.Fatalf("decision %q: %v", raw, err)
	}
	if decision.DeviceID != "OPENJOIN1" || decision.Status != "approved" || decision.IP != "198.51.100.80" ||
		decision.CreatedAt == "" || decision.DecidedAt == "" {
		t.Fatalf("decision = %+v, want an approval of OPENJOIN1 that keeps the request's address and time", decision)
	}

	putOnlinePeer(srv, "OPENTGT1", "203.0.113.81", 52000, peer.ConnUDP)
	putOnlinePeer(srv, "OPENJOIN1", "198.51.100.80", 51000, peer.ConnUDP)
	resp := srv.handlePunchHoleRequestTCP(&pb.PunchHoleRequest{Id: "OPENTGT1"}, udpAddr("198.51.100.80", 51000))
	phr := resp.GetPunchHoleResponse()
	if phr != nil && phr.Failure == pb.PunchHoleResponse_ID_NOT_EXIST {
		t.Fatalf("device let in by open mode was refused as an initiator: %+v", phr)
	}
}

// Open mode does not approve a device that is banned while its request waits.
func TestOpenModeLeavesBannedDevicesRequestAlone(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	if err := database.SetConfig("pending_device_OPENBAN1", `{"device_id":"OPENBAN1"}`); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "OPENBAN1"}); err != nil {
		t.Fatalf("UpsertPeer: %v", err)
	}
	if err := database.BanPeer("OPENBAN1", "test"); err != nil {
		t.Fatalf("BanPeer: %v", err)
	}
	srv.closePendingEnrollmentInOpenMode("OPENBAN1", "198.51.100.82")
	if pending, _ := database.GetConfig("pending_device_OPENBAN1"); pending == "" {
		t.Fatal("a banned device's request was closed as approved")
	}
	if raw, _ := database.GetConfig("enrollment_decision_OPENBAN1"); raw != "" {
		t.Fatalf("a banned device got an approval: %s", raw)
	}
}

// A waiting device's request keeps the uuid of its first RegisterPk, so the
// API can take the device's name from system information with that uuid.
func TestWaitingRequestKeepsTheRegisterPkUUID(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeManaged)
	const addr = "198.51.100.83:51000"
	srv.processRegisterPk(newRegisterPk("WAITUUID1"), addr)
	other := newRegisterPk("WAITUUID1")
	other.Uuid = []byte("another-uuid")
	srv.processRegisterPk(other, addr)

	raw, _ := database.GetConfig("pending_device_WAITUUID1")
	request := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatalf("request %q: %v", raw, err)
	}
	want := fmt.Sprintf("%x", []byte("test-uuid-WAITUUID1"))
	if request["register_uuid"] != want {
		t.Fatalf("register_uuid = %v, want %s (the first RegisterPk's)", request["register_uuid"], want)
	}

	// Filling in an address later keeps the fields this package does not know
	srv.recordPendingEnrollment("WAITUUID1", "", pendingEnrollmentMeta{Hostname: "late-name"})
	raw, _ = database.GetConfig("pending_device_WAITUUID1")
	request = map[string]any{}
	_ = json.Unmarshal([]byte(raw), &request)
	if request["register_uuid"] != want || request["hostname"] != "late-name" {
		t.Fatalf("request after filling in = %v", request)
	}
}
