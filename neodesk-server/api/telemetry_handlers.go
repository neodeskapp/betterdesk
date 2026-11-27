package api

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

const (
	telemetryMaxPayloadBytes = 512 * 1024
	telemetryCommandTTL      = 10 * time.Minute
)

var allowedTelemetryCommands = map[string]bool{
	"collect.hardware":  true,
	"collect.metrics":   true,
	"collect.services":  true,
	"collect.processes": true,
	"collect.events":    true,
	"collect.activity":  true,
	"files.browse":      true,
	"files.read":        true,
	"service.control":   true,
	"process.terminate": true,
}

type clientTelemetryEnvelope struct {
	Schema      int                            `json:"telemetry_schema"`
	SampleID    string                         `json:"sample_id"`
	CollectedAt string                         `json:"collected_at"`
	Metrics     json.RawMessage                `json:"metrics"`
	Status      map[string]string              `json:"status"`
	Snapshots   []clientTelemetrySnapshot      `json:"snapshots"`
	Results     []clientTelemetryCommandResult `json:"results"`
}

type clientTelemetrySnapshot struct {
	Kind        string          `json:"kind"`
	SampleID    string          `json:"sample_id"`
	Status      string          `json:"status"`
	CollectedAt string          `json:"collected_at"`
	Data        json.RawMessage `json:"data"`
}

type clientTelemetryCommandResult struct {
	ID     int64           `json:"id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

type queuedTelemetryCommand struct {
	ID      int64           `json:"id"`
	Command string          `json:"command"`
	Args    json.RawMessage `json:"args"`
	Expires string          `json:"expires_at"`
}

func telemetryStatus(status string) string {
	switch status {
	case "ok", "unsupported", "permission_denied", "unavailable", "error":
		return status
	default:
		return "error"
	}
}

func telemetryJSON(raw json.RawMessage, max int) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if len(raw) > max || !json.Valid(raw) {
		return "", fmt.Errorf("invalid telemetry JSON")
	}
	return string(raw), nil
}

func (s *Server) saveTelemetrySnapshot(deviceID, kind, sampleID, status, collectedAt string, data json.RawMessage) {
	if strings.TrimSpace(kind) == "" || len(kind) > 32 {
		return
	}
	payload, err := telemetryJSON(data, telemetryMaxPayloadBytes)
	if err != nil {
		return
	}
	if collectedAt == "" {
		collectedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.db.SaveTelemetrySnapshot(&db.TelemetrySnapshot{
		DeviceID:    deviceID,
		Kind:        kind,
		SampleID:    sampleID,
		Status:      telemetryStatus(status),
		Payload:     payload,
		CollectedAt: collectedAt,
	}); err != nil {
		s.auditLog.Log(audit.ActionTelemetrySnapshotError, deviceID, kind,
			map[string]string{"error": err.Error()})
	}
}

func (s *Server) saveClientTelemetry(deviceID string, raw json.RawMessage) {
	if len(raw) == 0 || len(raw) > telemetryMaxPayloadBytes {
		return
	}
	var envelope clientTelemetryEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	if envelope.Schema != 0 && envelope.Schema != 1 {
		return
	}
	if len(envelope.Metrics) > 0 {
		s.saveTelemetrySnapshot(deviceID, "metrics", envelope.SampleID, envelope.Status["metrics"],
			envelope.CollectedAt, envelope.Metrics)
		s.savePeerUsage(deviceID, envelope.Status["metrics"], envelope.Metrics)
	}
	for _, snapshot := range envelope.Snapshots {
		s.saveDeviceSnapshot(deviceID, snapshot)
	}
	var open map[int64]string
	if len(envelope.Results) > 0 {
		open = s.openTelemetryCommands(deviceID, envelope.Results)
	}
	for _, result := range envelope.Results {
		// A device completes only commands queued for it.
		command := open[result.ID]
		if result.ID <= 0 || command == "" {
			continue
		}
		var resultObject struct {
			Snapshot *clientTelemetrySnapshot `json:"snapshot"`
		}
		if len(result.Result) > 0 && json.Unmarshal(result.Result, &resultObject) == nil &&
			resultObject.Snapshot != nil {
			s.saveDeviceSnapshot(deviceID, *resultObject.Snapshot)
		}
		resultJSON, err := telemetryJSON(result.Result, 256*1024)
		if err != nil {
			resultJSON = `{"status":"error","code":"invalid_result"}`
		}
		status := result.Status
		if status == "running" {
			// The device started it and answers again when it is done: the
			// command stays open for that answer and is not handed out again.
			keep := s.fleetAnswer(deviceID, command, result.ID, status, result.Result)
			if err := s.db.MarkTelemetryCommandRunning(deviceID, result.ID, time.Now().Add(keep)); err != nil {
				s.auditLog.Log(audit.ActionTelemetryCommandResult, deviceID, fmt.Sprint(result.ID),
					map[string]string{"error": err.Error()})
			}
			continue
		}
		switch status {
		case "queued", "ok", "rejected", "unsupported", "permission_denied", "error", "timeout":
		default:
			status = "error"
		}
		if err := s.db.CompleteTelemetryCommand(deviceID, result.ID, status, resultJSON); err != nil {
			s.auditLog.Log(audit.ActionTelemetryCommandResult, deviceID, fmt.Sprint(result.ID),
				map[string]string{"error": err.Error()})
			continue
		}
		s.fleetAnswer(deviceID, command, result.ID, status, result.Result)
	}
}

// telemetryMetrics is the metrics object of a NeoDesk client's telemetry.
type telemetryMetrics struct {
	CPU       *float64 `json:"cpu_percent"`
	CPUWindow *float64 `json:"cpu_window_seconds"`
	Memory    *float64 `json:"memory_percent"`
	Disk      *float64 `json:"disk_percent"`
	Network   *struct {
		Received    *float64 `json:"received_per_second"`
		Transmitted *float64 `json:"transmitted_per_second"`
	} `json:"network"`
}

// percent is a share between 0 and 100, or nil.
func percent(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	p := math.Min(100, math.Max(0, *v))
	return &p
}

// rate is a non-negative rate, or nil.
func rate(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return nil
	}
	return v
}

// savePeerUsage adds the metrics of a telemetry heartbeat to the device's
// usage history (peer_metrics), which the device page draws. The CPU value
// counts only with the time it covers: clients up to 1.5.0-rc.7 sent the
// reading of a single refresh, 100 on Windows and 0 on Linux whatever the
// load (L480).
func (s *Server) savePeerUsage(deviceID, status string, raw json.RawMessage) {
	if status != "" && status != "ok" {
		return
	}
	var m telemetryMetrics
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	usage := db.PeerUsage{Memory: percent(m.Memory), Disk: percent(m.Disk)}
	if m.CPUWindow != nil && *m.CPUWindow > 0 {
		usage.CPU = percent(m.CPU)
	}
	if m.Network != nil {
		usage.NetRx, usage.NetTx = rate(m.Network.Received), rate(m.Network.Transmitted)
	}
	if usage.Empty() {
		return
	}
	if err := s.db.SavePeerUsage(deviceID, usage); err != nil {
		log.Printf("[api] Failed to save the usage sample of %s: %v", deviceID, err)
	}
}

// saveDeviceSnapshot stores a snapshot a device sent about itself. The
// verified identity is the server's record of a verified heartbeat, so a
// device never writes it.
func (s *Server) saveDeviceSnapshot(deviceID string, snapshot clientTelemetrySnapshot) {
	if snapshot.Kind == db.VerifiedIdentityKind {
		return
	}
	s.saveTelemetrySnapshot(deviceID, snapshot.Kind, snapshot.SampleID,
		snapshot.Status, snapshot.CollectedAt, snapshot.Data)
}

// openTelemetryCommands are the commands of the results that wait for
// the device's answer, by id with their names: queued for this device,
// pending or running, and not expired.
func (s *Server) openTelemetryCommands(deviceID string, results []clientTelemetryCommandResult) map[int64]string {
	ids := make([]int64, 0, len(results))
	for _, result := range results {
		if result.ID > 0 && len(ids) < 64 {
			ids = append(ids, result.ID)
		}
	}
	open, err := s.db.OpenTelemetryCommands(deviceID, ids)
	if err != nil {
		return nil
	}
	return open
}

func (s *Server) telemetryCommandsForDevice(deviceID string) []queuedTelemetryCommand {
	commands, err := s.db.GetPendingTelemetryCommands(deviceID, 10)
	if err != nil {
		return nil
	}
	result := make([]queuedTelemetryCommand, 0, len(commands))
	for _, command := range commands {
		args := json.RawMessage(command.Args)
		if !json.Valid(args) {
			args = json.RawMessage(`{}`)
		}
		result = append(result, queuedTelemetryCommand{
			ID: command.ID, Command: command.Command, Args: args, Expires: command.ExpiresAt,
		})
	}
	return result
}

// handleGetPeerTelemetry returns the latest bounded snapshots for a device.
func (s *Server) handleGetPeerTelemetry(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	peer, err := s.db.GetPeer(deviceID)
	if err != nil || peer == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "device_not_found"})
		return
	}

	kinds := []string{"identity", "metrics", "hardware", "services", "processes", "events", "activity", "files"}
	snapshots := make(map[string]any, len(kinds))
	for _, kind := range kinds {
		snapshot, err := s.db.GetTelemetrySnapshot(deviceID, kind)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "telemetry_read_failed"})
			return
		}
		if snapshot == nil {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(snapshot.Payload), &payload); err != nil {
			payload = map[string]string{"status": "error"}
		}
		snapshots[kind] = map[string]any{
			"sample_id":    snapshot.SampleID,
			"status":       snapshot.Status,
			"collected_at": snapshot.CollectedAt,
			"received_at":  snapshot.ReceivedAt,
			"data":         payload,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id": deviceID,
		"schema":    1,
		"snapshots": snapshots,
	})
}

// handleRefreshPeerHardware queues a bounded inventory refresh command.
func (s *Server) handleRefreshPeerHardware(w http.ResponseWriter, r *http.Request) {
	s.queueTelemetryCommand(w, r, "collect.hardware", json.RawMessage(`{}`))
}

// handleQueuePeerTelemetryCommand queues an allowlisted device command.
func (s *Server) handleQueuePeerTelemetryCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string          `json:"command"`
		Args    json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_command"})
		return
	}
	s.queueTelemetryCommand(w, r, body.Command, body.Args)
}

// telemetryCommandNeeds is the permission a queued command needs on top
// of the route's: the files are the agent's file tool (L388).
var telemetryCommandNeeds = map[string]string{
	"files.browse": auth.PermAgentFiles,
	"files.read":   auth.PermAgentFiles,
}

func (s *Server) queueTelemetryCommand(w http.ResponseWriter, r *http.Request, command string, args json.RawMessage) {
	deviceID := r.PathValue("id")
	if !allowedTelemetryCommands[command] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command_not_allowed"})
		return
	}
	if perm := telemetryCommandNeeds[command]; perm != "" && !s.permitted(w, r, perm) {
		return
	}
	peer, err := s.db.GetPeer(deviceID)
	if err != nil || peer == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "device_not_found"})
		return
	}
	if command != "collect.metrics" && s.isSupportAgent(deviceID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "support_agent_restricted"})
		return
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	argsJSON, err := telemetryJSON(args, 32*1024)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_command_args"})
		return
	}
	commandID, err := s.db.QueueTelemetryCommand(&db.TelemetryCommand{
		DeviceID:  deviceID,
		Command:   command,
		Args:      argsJSON,
		ExpiresAt: time.Now().UTC().Add(telemetryCommandTTL).Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "command_queue_failed"})
		return
	}
	s.auditLog.Log(audit.ActionTelemetryCommandQueued, "operator", deviceID,
		map[string]string{"command": command, "command_id": fmt.Sprint(commandID)})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":   true,
		"command_id": commandID,
		"device_id":  deviceID,
	})
}
