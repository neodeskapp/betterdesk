package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// postPlainHeartbeat sends a heartbeat body without a verified envelope.
func postPlainHeartbeat(t *testing.T, srv *Server, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", bytes.NewReader([]byte(body)))
	req.RemoteAddr = "198.51.100.20:1234"
	rec := httptest.NewRecorder()
	srv.handleClientHeartbeat(rec, req)
	return rec.Body.String()
}

func queueCommand(t *testing.T, srv *Server, deviceID string) int64 {
	t.Helper()
	id, err := srv.db.QueueTelemetryCommand(&db.TelemetryCommand{
		DeviceID:  deviceID,
		Command:   "files.read",
		Args:      `{"path":"C:/Users/boss/secret.txt"}`,
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func pendingIDs(t *testing.T, srv *Server, deviceID string) map[int64]bool {
	t.Helper()
	commands, err := srv.db.GetPendingTelemetryCommands(deviceID, 20)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, c := range commands {
		ids[c.ID] = true
	}
	return ids
}

// TestHeartbeatWithoutEnvelopeIsMetricsOnly: a heartbeat without a verified
// envelope stores cpu, memory and disk only: it gets no queued command,
// completes none and writes no snapshot. Inside a verified envelope a
// device gets its commands and completes its own, never another device's,
// and never writes the verified identity snapshot itself (RV168).
func TestHeartbeatWithoutEnvelopeIsMetricsOnly(t *testing.T) {
	srv, deviceID, deviceUUID, devicePriv, responsePub, responsePriv := newConnectionModeFixture(t)
	if err := srv.db.UpsertPeer(&db.Peer{ID: "PEERBBB2", Hostname: "host-b"}); err != nil {
		t.Fatal(err)
	}
	own := queueCommand(t, srv, deviceID)
	other := queueCommand(t, srv, "PEERBBB2")

	if body := postPlainHeartbeat(t, srv, `{"id":"`+deviceID+`"}`); strings.Contains(body, "telemetry_commands") || strings.Contains(body, "secret.txt") {
		t.Errorf("a heartbeat without credential got the device's commands: %s", body)
	}
	forged := `{"id":"PEERBBB2","cpu":12.5,"telemetry":{"results":[{"id":` + itoa(uint64(own)) + `,"status":"ok","result":{"forged":true}}],` +
		`"snapshots":[{"kind":"identity_verified","status":"ok","data":{"forged":true}},{"kind":"hardware","status":"ok","data":{"cpu":"forged"}}]}}`
	postPlainHeartbeat(t, srv, forged)
	if !pendingIDs(t, srv, deviceID)[own] {
		t.Errorf("a heartbeat without credential completed another device's command")
	}
	for _, kind := range []string{"hardware", db.VerifiedIdentityKind} {
		if snap, _ := srv.db.GetTelemetrySnapshot("PEERBBB2", kind); snap != nil {
			t.Errorf("a heartbeat without credential wrote the %s snapshot: %+v", kind, snap)
		}
	}

	queueCommand(t, srv, deviceID) // stays pending, so the answer hands it out
	payload := desktopIdentity(deviceID, deviceUUID, "")
	payload["telemetry"] = map[string]any{
		"results": []map[string]any{
			{"id": other, "status": "ok", "result": map[string]any{"from": "wrong device"}},
			{"id": own, "status": "ok", "result": map[string]any{"from": "own device"}},
		},
		"snapshots": []map[string]any{
			{"kind": db.VerifiedIdentityKind, "status": "ok", "data": map[string]any{"forged_marker": true}},
			{"kind": "hardware", "status": "ok", "data": map[string]any{"cpu": "real"}},
		},
	}
	response := postHeartbeat(t, srv, deviceID, deviceUUID, sealClientHeartbeat(t, srv, deviceID, devicePriv, 1, payload, responsePub))
	opened := openSealedHeartbeat(t, srv, response, responsePriv)
	if _, ok := opened["telemetry_commands"]; !ok {
		t.Errorf("a verified heartbeat got no commands: %v", opened)
	}
	if pendingIDs(t, srv, deviceID)[own] {
		t.Errorf("a verified heartbeat did not complete its own command")
	}
	if !pendingIDs(t, srv, "PEERBBB2")[other] {
		t.Errorf("a verified heartbeat completed another device's command")
	}
	if snap, _ := srv.db.GetTelemetrySnapshot(deviceID, "hardware"); snap == nil {
		t.Errorf("a verified heartbeat's hardware snapshot was not stored")
	}
	if snap, _ := srv.db.GetTelemetrySnapshot(deviceID, db.VerifiedIdentityKind); snap != nil && strings.Contains(snap.Payload, "forged_marker") {
		t.Errorf("the device wrote the verified identity snapshot itself: %s", snap.Payload)
	}
}
