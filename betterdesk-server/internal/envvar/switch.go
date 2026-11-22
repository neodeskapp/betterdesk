// Package envvar reads the values of settings the same way for the server
// and the console.
package envvar

import "strings"

// SwitchValues names the values Switch takes, for messages.
const SwitchValues = "1/0, true/false, yes/no, y/n or on/off"

// Switch reads an on/off setting: 1, true, yes, y and on are on; 0,
// false, no, n and off are off; in any letter case and with space around
// them. ok is false for any other value, the empty one included.
func Switch(value string) (on, ok bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	}
	return false, false
}

// SwitchOr is Switch with fallback for a value that is not an on/off
// value.
func SwitchOr(value string, fallback bool) bool {
	if on, ok := Switch(value); ok {
		return on
	}
	return fallback
}
