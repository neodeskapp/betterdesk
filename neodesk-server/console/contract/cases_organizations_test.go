//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Organizations: every route on the seeded organization (Demo Northwind
// Clinic, the last by name) and on one the role creates in its turn,
// which the roles with org.create get and the others try. The turn ends
// by deleting that organization and the device group the console made
// for it, which outlives the organization.
//
// Reading needs a session only (BUGS G16), so every signed-in role reads
// the seeded organization.
func init() {
	contract.Group("organizations",
		// Setup
		contract.Case{
			Name: "setup-seeded", Path: "/api/panel/org", As: "admin",
			Capture: map[string]string{"org": "organizations[2].id"},
		},

		// The seeded organization
		contract.Case{Name: "list", Path: "/api/panel/org"},
		contract.Case{Name: "get", Path: "/api/panel/org/{org}"},
		contract.Case{Name: "get-unknown", Path: "/api/panel/org/999999999"},
		contract.Case{Name: "get-invalid-id", Path: "/api/panel/org/bad$id"},
		contract.Case{Name: "users", Path: "/api/panel/org/{org}/users"},
		// An account carries last_login once it has signed in, which
		// depends on the groups that ran before; the list itself is pinned
		contract.Case{Name: "available-users", Path: "/api/panel/org/{org}/available-users", Volatile: []string{contract.Optional + "users[*].last_login"}},
		contract.Case{Name: "invitations", Path: "/api/panel/org/{org}/invitations"},
		contract.Case{Name: "devices", Path: "/api/panel/org/{org}/devices"},
		contract.Case{Name: "settings", Path: "/api/panel/org/{org}/settings"},
		contract.Case{Name: "address-book", Path: "/api/panel/org/{org}/address-book"},
		contract.Case{Name: "peer-credentials", Path: "/api/panel/org/{org}/peer-credentials"},
		contract.Case{Name: "device-groups", Path: "/api/panel/org/{org}/device-groups"},
		contract.Case{Name: "device-groups-unknown", Path: "/api/panel/org/999999999/device-groups"},
		contract.Case{Name: "device-groups-invalid-id", Path: "/api/panel/org/bad$id/device-groups"},

		// An organization of the role's own
		contract.Case{
			Name: "create-without-name", Method: "POST", Path: "/api/panel/org",
			Body: map[string]any{"slug": "contract-nameless"},
		},
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/panel/org",
			Body:    map[string]any{"name": "  Contract Org  ", "slug": "contract-org"},
			Capture: map[string]string{"created": "id"},
		},
		contract.Case{Name: "get-created", Path: "/api/panel/org/{created}"},
		// The id of the group depends on how many groups the groups before
		// this one made
		contract.Case{
			Name: "created-device-groups", Path: "/api/panel/org/{created}/device-groups",
			Capture:  map[string]string{"group": "device_groups[0].guid"},
			Volatile: []string{"device_groups[*].id"},
		},
		contract.Case{
			Name: "update", Method: "PUT", Path: "/api/panel/org/{created}",
			Body: map[string]any{"name": "Contract Org renamed"},
		},
		contract.Case{
			Name: "update-invalid-id", Method: "PUT", Path: "/api/panel/org/bad$id",
			Body: map[string]any{"name": "x"},
		},

		// Users of the organization
		contract.Case{
			Name: "create-user", Method: "POST", Path: "/api/panel/org/{created}/users",
			Body: map[string]any{
				"username": "contract.member", "display_name": "Contract Member",
				"email": "member@contract.example.invalid", "password": "Contract-Password-member-42!", "role": "user",
			},
			Capture: map[string]string{"orgUser": "id"},
		},
		contract.Case{Name: "created-users", Path: "/api/panel/org/{created}/users"},
		contract.Case{
			Name: "update-user", Method: "PUT", Path: "/api/panel/org/{created}/users/{orgUser}",
			Body: map[string]any{"display_name": "Contract Member renamed"},
		},
		contract.Case{
			Name: "update-user-invalid-id", Method: "PUT", Path: "/api/panel/org/{created}/users/bad$id",
			Body: map[string]any{"display_name": "x"},
		},
		contract.Case{Name: "delete-user", Method: "DELETE", Path: "/api/panel/org/{created}/users/{orgUser}"},

		// Console accounts as members
		contract.Case{
			Name: "link-member", Method: "POST", Path: "/api/panel/org/{created}/members",
			Body: map[string]any{"user_id": 1, "role": "user"},
		},
		contract.Case{Name: "created-available-users", Path: "/api/panel/org/{created}/available-users", Volatile: []string{contract.Optional + "users[*].last_login"}},
		contract.Case{Name: "unlink-member-hex-id", Method: "DELETE", Path: "/api/panel/org/{created}/members/0x1"},
		contract.Case{Name: "unlink-member-again", Method: "DELETE", Path: "/api/panel/org/{created}/members/1"},
		contract.Case{Name: "unlink-member-unknown", Method: "DELETE", Path: "/api/panel/org/{created}/members/999999999"},
		contract.Case{Name: "unlink-member-text", Method: "DELETE", Path: "/api/panel/org/{created}/members/someone"},
		contract.Case{Name: "unlink-member-invalid", Method: "DELETE", Path: "/api/panel/org/{created}/members/bad$id"},

		// Invitations. The answer of invite carries the invitation's code
		// once; the list does not
		contract.Case{
			Name: "invite", Method: "POST", Path: "/api/panel/org/{created}/invite",
			Body:          map[string]any{"email": "invitee@contract.example.invalid", "role": "user"},
			Capture:       map[string]string{"invitation": "id"},
			CaptureSecret: map[string]string{"invitation_code": "token"},
		},
		contract.Case{Name: "created-invitations", Path: "/api/panel/org/{created}/invitations"},
		// Redeeming one makes a sign-in for the NeoDesk client in the
		// organization (BUGS L42; recorded from Go, Node had no such
		// routes). The page's routes need no session and allow 20 requests
		// per address in 15 minutes, so the chain runs in the admin's turn
		contract.Case{
			Name: "invitation-lookup-not-a-code", Method: "POST", Path: "/api/invitations/lookup",
			Body: map[string]any{"token": "contract"}, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "invitation-lookup", Method: "POST", Path: "/api/invitations/lookup",
			Body: map[string]any{"token": "{secret:invitation_code}"}, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "invitation-redeem-short-password", Method: "POST", Path: "/api/invitations/redeem",
			Body:  map[string]any{"token": "{secret:invitation_code}", "username": "contract.invitee", "password": "short"},
			Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "invitation-redeem", Method: "POST", Path: "/api/invitations/redeem",
			Body:  map[string]any{"token": "{secret:invitation_code}", "username": "contract.invitee", "password": "Contract-Invitee-42!"},
			Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "invitation-redeem-again", Method: "POST", Path: "/api/invitations/redeem",
			Body:  map[string]any{"token": "{secret:invitation_code}", "username": "contract.other", "password": "Contract-Invitee-42!"},
			Roles: contract.Roles("admin"),
		},
		// Revoking: the admin's invitation is used (409), the other roles'
		// is open (204) or was never made
		contract.Case{Name: "revoke-invitation", Method: "DELETE", Path: "/api/panel/org/{created}/invitations/{invitation}"},
		contract.Case{Name: "revoke-invitation-unknown", Method: "DELETE", Path: "/api/panel/org/{created}/invitations/999999999"},

		// Devices
		contract.Case{
			Name: "assign-device-without-id", Method: "POST", Path: "/api/panel/org/{created}/devices",
			Body: map[string]any{"department": "Nowhere"},
		},
		contract.Case{
			Name: "assign-device", Method: "POST", Path: "/api/panel/org/{created}/devices",
			Body: map[string]any{"device_id": "900100021", "department": "Canteen", "location": "Ground floor", "tags": "kiosk"},
		},
		contract.Case{Name: "created-devices", Path: "/api/panel/org/{created}/devices"},
		contract.Case{Name: "unassign-device", Method: "DELETE", Path: "/api/panel/org/{created}/devices/900100021"},
		contract.Case{Name: "unassign-device-invalid", Method: "DELETE", Path: "/api/panel/org/{created}/devices/bad$id"},

		// Settings and the address book
		contract.Case{
			Name: "set-setting-without-key", Method: "PUT", Path: "/api/panel/org/{created}/settings",
			Body: map[string]any{"value": "x"},
		},
		contract.Case{
			Name: "set-setting", Method: "PUT", Path: "/api/panel/org/{created}/settings",
			Body: map[string]any{"key": "timezone", "value": "Europe/Berlin"},
		},
		contract.Case{Name: "created-settings", Path: "/api/panel/org/{created}/settings"},
		contract.Case{
			Name: "set-address-book", Method: "PUT", Path: "/api/panel/org/{created}/address-book",
			Body: map[string]any{"enabled": true, "data": map[string]any{"tags": []any{"contract"}, "peers": []any{}}},
		},
		contract.Case{Name: "created-address-book", Path: "/api/panel/org/{created}/address-book"},

		// The peer credential vault
		contract.Case{
			Name: "set-peer-credential", Method: "PUT", Path: "/api/panel/org/{created}/peer-credentials/900100021",
			Body: map[string]any{"password": "Contract-Preset-42!"},
		},
		contract.Case{
			Name: "set-peer-credential-odd-id", Method: "PUT", Path: "/api/panel/org/{created}/peer-credentials/a:b@c",
			Body: map[string]any{"password": "x"},
		},
		contract.Case{Name: "created-peer-credentials", Path: "/api/panel/org/{created}/peer-credentials"},
		contract.Case{Name: "delete-peer-credential", Method: "DELETE", Path: "/api/panel/org/{created}/peer-credentials/900100021"},

		// Deleting
		contract.Case{Name: "delete-invalid-id", Method: "DELETE", Path: "/api/panel/org/bad$id"},
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/panel/org/{created}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/panel/org/{created}"},
		contract.Case{Name: "cleanup-device-group", Method: "DELETE", Path: "/api/device-groups/{group}", As: "admin"},
		contract.Case{Name: "list-at-the-end", Path: "/api/panel/org"},
	)
}
