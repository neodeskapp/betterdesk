package static

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The English texts of the console say what the code does. Each test
// names the row of BUGS.md whose text it pins.

// english reads a text of lang/en.json by its dotted key.
func english(t *testing.T, key string) string {
	t.Helper()
	raw, err := os.ReadFile("files/lang/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(key, ".") {
		object, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("%s: no object above %q", key, part)
		}
		node = object[part]
	}
	text, ok := node.(string)
	if !ok {
		t.Fatalf("%s is no text", key)
	}
	return text
}

// The Delete action of the device panel hides the device unless the
// dialog's permanent deletion is chosen (BUGS L95).
func TestDeleteActionSaysItHidesTheDevice(t *testing.T) {
	text := english(t, "device_detail.action_delete_desc")
	if strings.Contains(strings.ToLower(text), "permanently remove") || !strings.Contains(strings.ToLower(text), "hide") {
		t.Errorf("device_detail.action_delete_desc is %q; Delete hides the device by default", text)
	}
}

// The enrollment mode decides how RustDesk clients join; new mesh agents
// always wait for approval, also under Open and Locked (BUGS L101).
func TestEnrollmentModesSayMeshAgentsWait(t *testing.T) {
	if text := english(t, "tokens.mode_open_desc"); strings.Contains(text, "Any device") || !strings.Contains(text, "RustDesk") || !strings.Contains(text, "Mesh agents") {
		t.Errorf("tokens.mode_open_desc is %q", text)
	}
	if text := english(t, "tokens.mode_locked_desc"); !strings.Contains(text, "RustDesk") || !strings.Contains(text, "Mesh agents") {
		t.Errorf("tokens.mode_locked_desc is %q", text)
	}
	if text := english(t, "tokens.enrollment_mode_note"); !strings.Contains(text, "mesh agents always wait") {
		t.Errorf("tokens.enrollment_mode_note is %q", text)
	}
	if text := english(t, "tokens.mode_notice_open"); strings.Contains(text, "new devices join") {
		t.Errorf("tokens.mode_notice_open is %q", text)
	}
}

// Under the Open visibility default, a folder or group with an access list
// is seen only by the accounts on its list; everyone else still sees every
// other device (BUGS L106).
func TestOpenVisibilitySaysListsHideTheirDevices(t *testing.T) {
	text := english(t, "settings.general.scope_open_desc")
	if strings.Contains(text, "until you give them access") || !strings.Contains(text, "access list") {
		t.Errorf("settings.general.scope_open_desc is %q", text)
	}
}
