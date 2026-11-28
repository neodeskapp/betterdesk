package policies

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
)

// TestReceiverAppliesNamesRealSettings: every setting the console says the
// receiver app applies is a setting of that group, so a renamed setting
// can't leave the console naming one that no longer exists.
func TestReceiverAppliesNamesRealSettings(t *testing.T) {
	fields := map[string]map[string]bool{}
	for _, group := range api.PolicyFields() {
		fields[group.Group] = map[string]bool{}
		for _, name := range group.Fields {
			fields[group.Group][name] = true
		}
	}
	for group, names := range receiverApplies {
		if serverGroups[group] {
			t.Errorf("%s is applied by the server, not by the receiver app", group)
		}
		for _, name := range names {
			if !fields[group][name] {
				t.Errorf("receiverApplies names %s.%s, which is no policy setting", group, name)
			}
		}
	}
}
