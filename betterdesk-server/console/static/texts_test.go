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
