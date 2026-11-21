package cdap

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// newSessionID is a session id: its kind ("term", "desk", ...) and 32
// random hex digits.
func newSessionID(kind string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return kind + "_" + hex.EncodeToString(b)
}

// owns reports whether the session with the id in sessions was opened on
// the device connection dc: a device's messages reach its own sessions
// only.
func owns(sessions *sync.Map, sessionID string, dc *DeviceConn) bool {
	v, ok := sessions.Load(sessionID)
	if !ok {
		return false
	}
	switch s := v.(type) {
	case *TerminalSession:
		return s.deviceConn == dc
	case *DesktopSession:
		return s.deviceConn == dc
	case *VideoSession:
		return s.deviceConn == dc
	case *FileSession:
		return s.deviceConn == dc
	case *AudioSession:
		return s.deviceConn == dc
	}
	return false
}
