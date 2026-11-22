package cdapproxy

import (
	"context"
	"encoding/json"
	"slices"
)

// The account's client features (the switches of the access sheet) apply
// to the device's sockets: a channel that needs a feature the account may
// not use is refused (Channel.Feature), and the desktop drops what the
// account may not do. The desktop's messages are JSON with a type, which
// the API server reads with encoding/json too, so both see the same type.

// everyFeature is what an account may not use when its features cannot be
// looked up.
var everyFeature = []string{"remote_control", "file_transfer", "clipboard", "audio", "tunnel", "terminal", "reboot", "privacy_mode"}

// featuresOff returns the client features the account may not use; every
// feature when the lookup fails.
func (p *Proxy) featuresOff(ctx context.Context, userID int64) []string {
	if p.opts.FeaturesOff == nil {
		return nil
	}
	off, err := p.opts.FeaturesOff(ctx, userID)
	if err != nil {
		logf("WARN: [CDAP] client features of user %d: %v; every feature is off for this socket", userID, err)
		return everyFeature
	}
	return off
}

// desktopToDevice are the messages of the browser's desktop that need a
// feature: input and the controls that act on the device need remote
// control, the clipboard the clipboard, a restart the remote reboot, and
// privacy mode its own.
var desktopToDevice = map[string]string{
	"input":          "remote_control",
	"block_input":    "remote_control",
	"lock_screen":    "remote_control",
	"clipboard_set":  "clipboard",
	"restart_device": "reboot",
	"privacy_mode":   "privacy_mode",
}

// desktopToBrowser are the device's messages that need a feature: the
// device's clipboard needs the clipboard.
var desktopToBrowser = map[string]string{
	"clipboard_update": "clipboard",
}

// desktopFilters returns the filters of a desktop socket for the features
// in off: each drops the messages whose type needs one of them. Toward
// the device that is text and binary messages alike, since the API server
// reads both kinds as JSON; toward the browser only text, its binary
// messages are the screen. Nil when nothing is off.
func desktopFilters(off []string) (toDevice, toBrowser func(binary bool, data []byte) bool) {
	if len(off) == 0 {
		return nil, nil
	}
	allowed := func(needs map[string]string, data []byte) bool {
		var msg struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &msg) != nil {
			return true
		}
		feature, ok := needs[msg.Type]
		return !ok || !slices.Contains(off, feature)
	}
	toDevice = func(_ bool, data []byte) bool { return allowed(desktopToDevice, data) }
	toBrowser = func(binary bool, data []byte) bool { return binary || allowed(desktopToBrowser, data) }
	return toDevice, toBrowser
}
