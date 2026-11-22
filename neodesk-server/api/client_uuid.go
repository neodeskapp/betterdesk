package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// sameRegisteredUUID reports whether sent, the uuid a client posts, is the
// uuid stored for its device. The signal server stores the uuid's bytes as
// hex and stock RustDesk clients post them in base64, so both are read in
// every form they come in: as they are, as hex and as base64. A device
// without a stored uuid, or a request without one, never matches.
func sameRegisteredUUID(stored, sent string) bool {
	stored, sent = strings.TrimSpace(stored), strings.TrimSpace(sent)
	if stored == "" || sent == "" {
		return false
	}
	for _, a := range uuidForms(stored) {
		for _, b := range uuidForms(sent) {
			if bytes.Equal(a, b) {
				return true
			}
		}
	}
	return false
}

// uuidForms are the bytes a uuid's text can stand for.
func uuidForms(s string) [][]byte {
	forms := [][]byte{[]byte(s)}
	if b, err := hex.DecodeString(s); err == nil && len(b) > 0 {
		forms = append(forms, b)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) > 0 {
			forms = append(forms, b)
		}
	}
	return forms
}

// clientUUID is the uuid a client's audit record names its device by:
// host_uuid, else uuid.
func clientUUID(body map[string]any) string {
	if v := coerceStr(body["host_uuid"]); v != "" {
		return v
	}
	return coerceStr(body["uuid"])
}

// knownClientDevice reports whether hostID is a device the server has and
// the record carries the uuid it registered with.
func (s *Server) knownClientDevice(hostID string, body map[string]any) (bool, error) {
	if hostID == "" {
		return false, nil
	}
	p, err := s.db.GetPeer(hostID)
	if err != nil {
		return false, err
	}
	return p != nil && sameRegisteredUUID(p.UUID, clientUUID(body)), nil
}
