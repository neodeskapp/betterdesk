//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Folders: in every role's turn the role creates folders, changes one,
// puts devices into it and deletes them again; roles without device.edit
// get 403 and work on an id nothing has. Admin sets up a folder whose
// access list names only e2e_viewer and puts one device into it, so the
// counts and the device checks of the other roles see a limited scope,
// and deletes it at the end of the turn.
//
// The devices used, 900100020 and 900100021, are in no folder in the
// seed and are in none afterwards.
func init() {
	long := strings.Repeat("x", 50)
	contract.Group("folders",
		// Setup: a folder only e2e_viewer may see, with one device
		contract.Case{
			Name: "setup-restricted", Method: "POST", Path: "/api/folders", As: "admin",
			Body:    map[string]any{"name": "Contract restricted", "allowed_users": []any{"e2e_viewer"}},
			Capture: map[string]string{"restricted": "data.id"},
		},
		contract.Case{
			Name: "setup-restricted-device", Method: "POST", Path: "/api/folders/{restricted}/devices", As: "admin",
			Body: map[string]any{"deviceIds": []any{"900100020"}},
		},

		// Reading, with the scope of the role
		contract.Case{Name: "list", Path: "/api/folders"},

		// Creating
		contract.Case{
			Name: "create-without-name", Method: "POST", Path: "/api/folders",
			Body: map[string]any{"color": "#ffffff"},
		},
		contract.Case{
			Name: "create-blank-name", Method: "POST", Path: "/api/folders",
			Body: map[string]any{"name": " \t  "},
		},
		contract.Case{
			Name: "create-name-too-long", Method: "POST", Path: "/api/folders",
			Body: map[string]any{"name": " " + long},
		},
		contract.Case{
			Name: "create-name-number", Method: "POST", Path: "/api/folders",
			Body: map[string]any{"name": 5},
		},
		contract.Case{
			Name: "create-array-body", Method: "POST", Path: "/api/folders",
			RawBody: `[{"name":"In a list"}]`, ContentType: "application/json",
		},
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/folders",
			Body: map[string]any{
				"name": "  Contract folder ", "color": "#123456", "icon": "star",
				"allowed_users": []any{"e2e_viewer", "nobody-has-this-name", " e2e_viewer "}, "allowed_groups": []any{},
			},
			Capture: map[string]string{"folder": "data.id"},
		},
		contract.Case{
			Name: "create-with-defaults", Method: "POST", Path: "/api/folders",
			Body:    map[string]any{"name": long},
			Capture: map[string]string{"plain": "data.id"},
		},
		contract.Case{
			Name: "create-odd-types", Method: "POST", Path: "/api/folders",
			Body:    map[string]any{"name": "Odd", "color": 5, "icon": "", "allowed_users": "e2e_operator, e2e_viewer"},
			Capture: map[string]string{"odd": "data.id"},
		},
		contract.Case{Name: "list-after-create", Path: "/api/folders"},

		// Changing
		contract.Case{
			Name: "update-unknown", Method: "PATCH", Path: "/api/folders/999999999",
			Body: map[string]any{"name": "Nobody"},
		},
		contract.Case{
			Name: "update-id-not-a-number", Method: "PATCH", Path: "/api/folders/abc",
			Body: map[string]any{"name": "Nobody"},
		},
		contract.Case{
			Name: "update-blank-name", Method: "PATCH", Path: "/api/folders/{folder}",
			Body: map[string]any{"name": "   "},
		},
		contract.Case{
			Name: "update-name-too-long", Method: "PATCH", Path: "/api/folders/{folder}",
			Body: map[string]any{"name": long + "y"},
		},
		contract.Case{
			Name: "update-null-name", Method: "PATCH", Path: "/api/folders/{folder}",
			Body: map[string]any{"name": nil},
		},
		contract.Case{
			Name: "update", Method: "PATCH", Path: "/api/folders/{folder}",
			Body: map[string]any{"name": " Contract renamed ", "color": "#654321", "allowed_users": []any{"e2e_viewer", "e2e_operator"}},
		},
		contract.Case{
			Name: "update-icon-only", Method: "PATCH", Path: "/api/folders/{folder}",
			Body: map[string]any{"icon": "box", "allowed_groups": []any{"no-such-group"}},
		},
		contract.Case{
			Name: "update-nothing", Method: "PATCH", Path: "/api/folders/{plain}",
			Body: map[string]any{},
		},
		contract.Case{Name: "list-after-update", Path: "/api/folders"},

		// Devices into folders
		contract.Case{
			Name: "assign-no-devices", Method: "POST", Path: "/api/folders/{folder}/devices",
			Body: map[string]any{"deviceIds": []any{}},
		},
		contract.Case{
			Name: "assign-not-a-list", Method: "POST", Path: "/api/folders/{folder}/devices",
			Body: map[string]any{"deviceIds": "900100021"},
		},
		contract.Case{
			Name: "assign-unknown-folder", Method: "POST", Path: "/api/folders/999999999/devices",
			Body: map[string]any{"deviceIds": []any{"900100021"}},
		},
		contract.Case{
			Name: "assign-unknown-device", Method: "POST", Path: "/api/folders/{folder}/devices",
			Body: map[string]any{"deviceIds": []any{"900100021", "999999998"}},
		},
		contract.Case{
			Name: "assign-deleted-device", Method: "POST", Path: "/api/folders/{folder}/devices",
			Body: map[string]any{"deviceIds": []any{"900100030"}},
		},
		contract.Case{
			Name: "assign-out-of-scope", Method: "POST", Path: "/api/folders/{folder}/devices",
			Body: map[string]any{"deviceIds": []any{"900100021", "900100020"}},
		},
		contract.Case{
			Name: "assign", Method: "POST", Path: "/api/folders/{folder}/devices",
			Body: map[string]any{"deviceIds": []any{"900100021", 900100021}},
		},
		contract.Case{Name: "list-after-assign", Path: "/api/folders"},
		contract.Case{
			Name: "unassign-with-folder-zero", Method: "POST", Path: "/api/folders/0/devices",
			Body: map[string]any{"deviceIds": []any{"900100021"}},
		},

		// One device
		contract.Case{
			Name: "move-unknown-device", Method: "PATCH", Path: "/api/devices/999999999/folder",
			Body: map[string]any{"folderId": "{folder}"},
		},
		contract.Case{
			Name: "move-to-unknown-folder", Method: "PATCH", Path: "/api/devices/900100021/folder",
			Body: map[string]any{"folderId": 999999999},
		},
		contract.Case{
			Name: "move-to-folder-not-a-number", Method: "PATCH", Path: "/api/devices/900100021/folder",
			Body: map[string]any{"folderId": "abc"},
		},
		contract.Case{
			Name: "move-out-of-scope", Method: "PATCH", Path: "/api/devices/900100020/folder",
			Body: map[string]any{"folderId": "{restricted}"},
		},
		contract.Case{
			Name: "move", Method: "PATCH", Path: "/api/devices/900100021/folder",
			Body: map[string]any{"folderId": "{folder}"},
		},
		contract.Case{Name: "list-after-move", Path: "/api/folders"},
		contract.Case{
			Name: "move-out", Method: "PATCH", Path: "/api/devices/900100021/folder",
			Body: map[string]any{"folderId": nil},
		},
		contract.Case{
			Name: "move-without-folder", Method: "PATCH", Path: "/api/devices/900100021/folder",
			Body: map[string]any{},
		},

		// Deleting
		contract.Case{Name: "delete-unknown", Method: "DELETE", Path: "/api/folders/999999999"},
		contract.Case{Name: "delete-id-not-a-number", Method: "DELETE", Path: "/api/folders/abc"},
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/folders/{folder}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/folders/{folder}"},
		contract.Case{Name: "delete-plain", Method: "DELETE", Path: "/api/folders/{plain}"},
		contract.Case{Name: "delete-odd", Method: "DELETE", Path: "/api/folders/{odd}"},

		// Cleanup
		contract.Case{Name: "cleanup-restricted", Method: "DELETE", Path: "/api/folders/{restricted}", As: "admin"},
		contract.Case{Name: "list-at-the-end", Path: "/api/folders"},
	)
}
