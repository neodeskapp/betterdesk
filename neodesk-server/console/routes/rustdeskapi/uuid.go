package rustdeskapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// What a client reports without signing in (heartbeats, system information,
// audit records) is taken only with the uuid the device registered with at
// the signal server. A request that names a device without that uuid, or
// with another, is answered as one that names no known device.
//
// The signal server stores the uuid of a registration (RegisterPk) in
// peers.uuid as the hex of its bytes. A RustDesk client sends the same bytes
// in base64 in the "uuid" of its HTTP bodies; other clients may send the text
// itself. Both forms are compared as the signal server compares two uuids:
// 16 bytes stand as they are, any other even-length hex text is read as the
// bytes it spells.

// registeredUUID is the uuid the signal server stored for the device: the
// uuid of its live row in peers, "" when it has none or the database has no
// peers table.
func (s *clientStore) registeredUUID(ctx context.Context, id string) (string, error) {
	var uuid sql.NullString
	err := s.db.QueryRow(ctx, s.db.Pick(
		`SELECT uuid FROM peers WHERE id = ? AND NOT COALESCE(soft_deleted, 0)`,
		`SELECT uuid FROM peers WHERE id = ? AND NOT COALESCE(soft_deleted, FALSE)`), id).Scan(&uuid)
	switch {
	case err == nil:
		return strings.TrimSpace(uuid.String), nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case strings.Contains(jsbind.Message(err), s.db.Pick("no such table", "does not exist")):
		return "", nil
	}
	return "", fmt.Errorf("load the uuid of device %q: %w", id, err)
}

// sentUUID is the uuid a body names: its "uuid" when that is text that is
// not empty, else names[0] and on as fallbacks (the audit records' host_uuid).
func sentUUID(body middleware.JSONObject, names ...string) string {
	for _, name := range append([]string{"uuid"}, names...) {
		if text, ok := member(body, name).(string); ok && text != "" {
			return text
		}
	}
	return ""
}

// sameDeviceUUID reports whether sent, as a client sends it, is the uuid
// stored for the device. Nothing matches an empty uuid.
func sameDeviceUUID(stored, sent string) bool {
	if stored == "" || sent == "" {
		return false
	}
	want := uuidBytes([]byte(stored))
	if len(want) == 0 {
		return false
	}
	if raw, err := base64.StdEncoding.DecodeString(sent); err == nil && bytes.Equal(uuidBytes(raw), want) {
		return true
	}
	return bytes.Equal(uuidBytes([]byte(sent)), want)
}

// uuidBytes is the signal server's form of a uuid's bytes: 16 bytes as they
// are, an even-length hex text as the bytes it spells, anything else as it
// is.
func uuidBytes(raw []byte) []byte {
	if len(raw) == 16 || len(raw)%2 != 0 {
		return raw
	}
	if decoded, err := hex.DecodeString(string(raw)); err == nil && len(decoded) > 0 {
		return decoded
	}
	return raw
}

// knownDevice reports whether the body comes from the device id names: the
// signal server has a live row for the id and the body carries the uuid
// stored there.
func (h *handlers) knownDevice(ctx context.Context, id string, body middleware.JSONObject, uuidFields ...string) (bool, error) {
	if id == "" {
		return false, nil
	}
	stored, err := h.store.registeredUUID(ctx, id)
	if err != nil {
		return false, err
	}
	return sameDeviceUUID(stored, sentUUID(body, uuidFields...)), nil
}
