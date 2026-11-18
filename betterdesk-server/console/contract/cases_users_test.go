//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The account routes (routes/users.routes.js): the account list, user
// groups, creating, changing, renaming and deleting accounts, password
// resets, the device scope of an account and its organizations.
//
// Creating an account and resetting a password count against a limit of
// three in five minutes per address, which they share with the password
// change of the auth group (BUGS C4). The auth group runs earlier in the
// same few minutes and spends two, so this group spends one: the account
// "contract_users_tmp" is created once, by admin in the signed-out turn,
// and deleted at the end of that turn. Every other request to the two
// routes is one the guards refuse before the limit counts it. What the
// roles that may create accounts are refused (a role above their own,
// weak passwords, the limit itself) is in the recorded Node answers of
// console/routes/users (TestNodeAnswers).
//
// The seeded accounts are admin (1), demo_operator (2), demo_viewer (3),
// demo_serveradmin (4) and e2e_<role> from 5 (admin) to 12 (pro). Writes
// on demo_viewer are only ones that are refused before anything changes.
// A user group is created and deleted by admin in each turn.
const (
	usersTmpUser     = "contract_users_tmp"
	usersTmpPassword = "Contract-Users-1!x"
	usersTmpNew      = "Contract-Users-2!y"
)

func init() {
	signedOut := contract.Roles("signed-out")
	// The roles the guards refuse before the password limit counts:
	// no session, no permission, or no write access to the users section.
	refusedEarly := contract.Roles("signed-out", "pro", "guest_issuer", "viewer", "operator", "server_admin")
	some := contract.Roles("signed-out", "viewer", "operator", "global_admin", "admin")
	// Accounts sign in in each group's turn, so who has a last_login
	// depends on the groups before this one.
	listMarks := []string{"~data.users[*].last_login"}

	contract.Group("users",
		// Reading
		contract.Case{Name: "list", Path: "/api/users", Volatile: listMarks},
		contract.Case{Name: "user-groups", Path: "/api/panel/user-groups"},
		contract.Case{Name: "scope-of-demo-viewer", Path: "/api/users/3/effective-scope"},
		contract.Case{Name: "scope-of-admin", Path: "/api/users/1/effective-scope", Roles: some},
		contract.Case{Name: "scope-id-with-letters-behind", Path: "/api/users/3abc/effective-scope", Roles: some},
		contract.Case{Name: "scope-id-zero", Path: "/api/users/0/effective-scope", Roles: some},
		contract.Case{Name: "scope-id-not-a-number", Path: "/api/users/abc/effective-scope", Roles: some},
		contract.Case{Name: "scope-unknown", Path: "/api/users/999999999/effective-scope", Roles: some},
		contract.Case{Name: "organizations-of-demo-operator", Path: "/api/users/2/organizations"},
		contract.Case{Name: "organizations-unknown", Path: "/api/users/999999999/organizations", Roles: some},
		contract.Case{Name: "organizations-id-negative", Path: "/api/users/-2/organizations", Roles: some},
		contract.Case{
			Name: "assign-organization-unknown-user", Method: "POST", Path: "/api/users/999999999/organizations",
			Body: map[string]any{"org_id": "contract-org", "role": "member"},
		},
		contract.Case{
			Name: "assign-organization-id-not-a-number", Method: "POST", Path: "/api/users/abc/organizations",
			Body: map[string]any{}, Roles: some,
		},

		// Changes on demo_viewer that are refused before anything is written
		contract.Case{Name: "update-unknown", Method: "PATCH", Path: "/api/users/999999999", Body: map[string]any{"role": "viewer"}},
		contract.Case{Name: "rename-unknown", Method: "PATCH", Path: "/api/users/999999999", Body: map[string]any{"username": "contract_nobody"}, Roles: some},
		contract.Case{Name: "rename-invalid", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"username": "no spaces"}, Roles: some},
		contract.Case{Name: "rename-taken", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"username": "e2e_viewer"}, Roles: some},
		contract.Case{Name: "update-id-not-a-number", Method: "PATCH", Path: "/api/users/abc", Body: map[string]any{}, Roles: some},
		contract.Case{Name: "update-invalid-role", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"role": "nonsense"}},
		contract.Case{Name: "update-role-guest-issuer", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"role": "guest_issuer"}, Roles: some},
		contract.Case{
			Name: "update-role-above-own", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"role": "admin"},
			Roles: contract.Roles("signed-out", "viewer", "operator", "server_admin", "global_admin"),
		},
		contract.Case{Name: "update-weak-password", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"password": "weak"}},
		contract.Case{
			Name: "update-invalid-group", Method: "PATCH", Path: "/api/users/3",
			Body: map[string]any{"groupGuids": []any{"no such group"}}, Roles: some,
		},
		contract.Case{Name: "update-invalid-email", Method: "PATCH", Path: "/api/users/3", Body: map[string]any{"email": "not-an-email"}, Roles: some},
		contract.Case{
			Name: "update-own-role-down", Method: "PATCH", Path: "/api/users/6", Body: map[string]any{"role": "operator"},
			Roles: contract.Roles("super_admin"),
		},
		contract.Case{
			Name: "update-own-role-down-admin", Method: "PATCH", Path: "/api/users/5", Body: map[string]any{"role": "viewer"},
			Roles: contract.Roles("admin"),
		},
		contract.Case{Name: "delete-unknown", Method: "DELETE", Path: "/api/users/999999999"},
		contract.Case{Name: "delete-id-not-a-number", Method: "DELETE", Path: "/api/users/abc", Roles: some},
		contract.Case{Name: "delete-self", Method: "DELETE", Path: "/api/users/8", Roles: contract.Roles("global_admin")},
		contract.Case{Name: "delete-self-super-admin", Method: "DELETE", Path: "/api/users/6", Roles: contract.Roles("super_admin")},
		contract.Case{Name: "delete-self-admin", Method: "DELETE", Path: "/api/users/5", Roles: contract.Roles("admin")},
		contract.Case{
			Name: "create-refused", Method: "POST", Path: "/api/users",
			Body: map[string]any{"username": "contract_refused", "password": usersTmpPassword}, Roles: refusedEarly,
		},
		contract.Case{
			Name: "reset-password-refused", Method: "POST", Path: "/api/users/3/reset-password",
			Body: map[string]any{"newPassword": usersTmpPassword}, Roles: refusedEarly,
		},

		// A user group of the turn
		contract.Case{
			Name: "group-create", Method: "POST", Path: "/api/panel/user-groups", As: "admin",
			Body:    map[string]any{"name": "  Contract users group  ", "note": "made by the contract suite", "team_id": "contract-team"},
			Capture: map[string]string{"group": "data.group.guid"},
		},
		contract.Case{Name: "group-create-by-role", Method: "POST", Path: "/api/panel/user-groups", Body: map[string]any{"note": "no name"}},
		contract.Case{
			Name: "group-create-long-name", Method: "POST", Path: "/api/panel/user-groups",
			Body: map[string]any{"name": strings.Repeat("n", 81)}, Roles: some,
		},
		contract.Case{
			Name: "group-create-invalid-team", Method: "POST", Path: "/api/panel/user-groups",
			Body: map[string]any{"name": "x", "team_id": "no spaces allowed"}, Roles: some,
		},
		contract.Case{
			Name: "group-update", Method: "PATCH", Path: "/api/panel/user-groups/{group}",
			Body: map[string]any{"name": "Contract users group renamed", "note": ""},
		},
		contract.Case{Name: "group-update-without-name", Method: "PATCH", Path: "/api/panel/user-groups/{group}", Body: map[string]any{"note": "x"}, Roles: some},
		contract.Case{Name: "group-update-unknown", Method: "PATCH", Path: "/api/panel/user-groups/no-such-group", Body: map[string]any{"name": "x"}, Roles: some},
		contract.Case{Name: "group-update-invalid-guid", Method: "PATCH", Path: "/api/panel/user-groups/no%20such%20group", Body: map[string]any{"name": "x"}, Roles: some},
		contract.Case{Name: "group-list-after-update", Path: "/api/panel/user-groups", As: "admin"},
		contract.Case{Name: "group-delete-by-role", Method: "DELETE", Path: "/api/panel/user-groups/{group}"},
		contract.Case{Name: "group-delete", Method: "DELETE", Path: "/api/panel/user-groups/{group}", As: "admin"},
		contract.Case{Name: "group-list-after-delete", Path: "/api/panel/user-groups", As: "admin"},

		// The account of the signed-out turn
		contract.Case{
			Name: "account-group", Method: "POST", Path: "/api/panel/user-groups", As: "admin", Roles: signedOut,
			Body:    map[string]any{"name": "Contract account group"},
			Capture: map[string]string{"accountGroup": "data.group.guid"},
		},
		contract.Case{
			Name: "account-create", Method: "POST", Path: "/api/users", As: "admin", Roles: signedOut,
			Body: map[string]any{
				"username": usersTmpUser, "password": usersTmpPassword, "role": "operator",
				"email": "  Tmp@Example.invalid ", "groupGuids": []any{"{accountGroup}"},
				"peerIds": "900100001, 900100002", "strategyGuid": "",
			},
			Capture:  map[string]string{"account": "data.id"},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "account-in-list", Path: "/api/users", As: "admin", Roles: signedOut,
			Volatile: append([]string{"data.users[12].id"}, listMarks...),
		},
		contract.Case{Name: "account-groups", Path: "/api/panel/user-groups", As: "operator", Roles: signedOut},
		contract.Case{Name: "account-scope", Path: "/api/users/{account}/effective-scope", As: "operator", Roles: signedOut},
		contract.Case{Name: "account-organizations", Path: "/api/users/{account}/organizations", As: "admin", Roles: signedOut},
		contract.Case{
			Name: "account-role-above-global-admin", Method: "PATCH", Path: "/api/users/{account}", As: "global_admin", Roles: signedOut,
			Body: map[string]any{"role": "server_admin"},
		},
		contract.Case{
			Name: "account-role-by-global-admin", Method: "PATCH", Path: "/api/users/{account}", As: "global_admin", Roles: signedOut,
			Body:     map[string]any{"role": "viewer", "groupGuids": []any{}, "peerIds": []any{"900100003"}},
			Volatile: []string{"data.id", "data.created_at"},
		},
		contract.Case{
			Name: "account-weak-password", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body: map[string]any{"password": "alllower"},
		},
		contract.Case{
			Name: "account-password", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body:     map[string]any{"password": usersTmpNew, "email": ""},
			Volatile: []string{"data.id", "data.created_at"},
		},
		contract.Case{
			Name: "account-invalid-email", Method: "PATCH", Path: "/api/users/{account}", As: "super_admin", Roles: signedOut,
			Body: map[string]any{"email": "two@@at.example"},
		},
		contract.Case{
			Name: "account-change-by-operator", Method: "PATCH", Path: "/api/users/{account}", As: "operator", Roles: signedOut,
			Body: map[string]any{"role": "viewer"},
		},
		contract.Case{
			Name: "account-unchanged", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body: map[string]any{}, Volatile: []string{"data.id", "data.created_at"},
		},
		// A new name: refused when malformed or taken, then given, with the
		// rows that hang on the name; the rest of the request goes on under
		// the new name.
		contract.Case{
			Name: "account-rename-too-short", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body: map[string]any{"username": "ab"},
		},
		contract.Case{
			Name: "account-rename-taken", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body: map[string]any{"username": "e2e_operator"},
		},
		contract.Case{
			Name: "account-rename-by-operator", Method: "PATCH", Path: "/api/users/{account}", As: "operator", Roles: signedOut,
			Body: map[string]any{"username": "contract.renamed@tmp"},
		},
		contract.Case{
			Name: "account-rename", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body:     map[string]any{"username": "  contract.renamed@tmp  ", "role": "operator"},
			Volatile: []string{"data.id", "data.created_at"},
		},
		contract.Case{
			Name: "account-rename-same-name", Method: "PATCH", Path: "/api/users/{account}", As: "global_admin", Roles: signedOut,
			Body:     map[string]any{"username": "contract.renamed@tmp"},
			Volatile: []string{"data.id", "data.created_at"},
		},
		contract.Case{
			Name: "account-rename-then-invalid-role", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body: map[string]any{"username": "contract_renamed_tmp", "role": "nonsense"},
		},
		contract.Case{
			Name: "account-after-rename", Method: "PATCH", Path: "/api/users/{account}", As: "admin", Roles: signedOut,
			Body: map[string]any{}, Volatile: []string{"data.id", "data.created_at"},
		},
		contract.Case{Name: "account-delete-by-operator", Method: "DELETE", Path: "/api/users/{account}", As: "operator", Roles: signedOut},
		contract.Case{Name: "account-delete", Method: "DELETE", Path: "/api/users/{account}", As: "admin", Roles: signedOut},
		contract.Case{Name: "account-delete-again", Method: "DELETE", Path: "/api/users/{account}", As: "admin", Roles: signedOut},
		contract.Case{Name: "account-gone-scope", Path: "/api/users/{account}/effective-scope", As: "admin", Roles: signedOut},
		contract.Case{Name: "account-group-delete", Method: "DELETE", Path: "/api/panel/user-groups/{accountGroup}", As: "admin", Roles: signedOut},
		contract.Case{Name: "list-at-the-end", Path: "/api/users", As: "admin", Roles: signedOut, Volatile: listMarks},
	)
}
