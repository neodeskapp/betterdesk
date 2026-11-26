package users_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// fix is a step of the recordings that the Go console answers otherwise
// on purpose: Node's answer was a bug that is fixed here (bug names the
// row of BUGS.md).
type fix struct {
	bug string
	// status is the Go console's status; 0 keeps the recorded one. A step
	// with a status of its own makes no calls to the API server.
	status int
	// answer makes the Go console's answer from the recorded one; nil
	// keeps it.
	answer func(node string) string
	// node writes what Node's request wrote and the Go console's does
	// not, or takes back what the Go console's wrote and Node's did not,
	// after the step, so the steps after it start from the rows the
	// recording's did. nil writes nothing.
	node func(n *nodeWrites)
	// endsSessionsOf are the roles whose sessions the step ends in the Go
	// console and not in Node.
	endsSessionsOf []string
	// undo are the tables, parents first, whose rows are put back after
	// the step as they were before it (restore): Node's request failed and
	// wrote nothing where the Go console's goes through.
	undo []string
	// pinnedBy names the recording that pins a step this one does not
	// compare: neither its answer nor its rows nor its calls. It is for a
	// step whose request Node failed and the Go console carries out, and
	// the steps after it that see the difference, where the other
	// recording made the same requests and Node carried them out.
	pinnedBy string
}

// replaced is the recorded answer with one part of it replaced.
func replaced(old, new string) func(string) string {
	return func(node string) string {
		if !strings.Contains(node, old) {
			return "recorded answer without " + old
		}
		return strings.Replace(node, old, new, 1)
	}
}

// totpFlag is the last field of an account as the list and an update send
// it in the recordings.
var totpFlag = regexp.MustCompile(`("totp_enabled":(?:true|false))`)

// withDisabledFlag is a recorded answer with "disabled" after the second
// factor of every account in it: the list and an update say whether an
// account is disabled, and no account of the recordings is (L107).
func withDisabledFlag(answer string) string {
	return withSignInState(withDisplayName(totpFlag.ReplaceAllString(answer, `$1,"disabled":false`)))
}

// listedName is an account of the list in the recordings from its
// username to its picture, with the disabled flag.
var listedName = regexp.MustCompile(`"username":("(?:[^"\\]|\\.)*"|[^,"]*),(.*?"totp_enabled":(?:true|false),"disabled":false),"has_avatar"`)

// withDisplayName is a recorded list with the display name of each
// account before its picture: no account of the recordings has a first
// or last name, so it is the username, as text.
func withDisplayName(answer string) string {
	return listedName.ReplaceAllStringFunc(answer, func(m string) string {
		parts := listedName.FindStringSubmatch(m)
		name := parts[1]
		if !strings.HasPrefix(name, `"`) {
			name = `"` + name + `"`
		}
		return `"username":` + parts[1] + "," + parts[2] + `,"display_name":` + name + `,"has_avatar"`
	})
}

// listedPicture is the end of an account as the list sends it in the
// recordings, with the disabled flag.
var listedPicture = regexp.MustCompile(`"totp_enabled":(true|false),"disabled":false,(?:"display_name":"(?:[^"\\]|\\.)*",)?"has_avatar":(?:true|false),"avatar_version":(?:null|"[^"]*"|-?[0-9]+)`)

// withSignInState is a recorded list with what it says about each
// account's sign-in after its picture: no account of the recordings is
// locked, must change its password or has a reset link waiting, and two-
// factor sign-in is on where it was, else off.
func withSignInState(answer string) string {
	return listedPicture.ReplaceAllStringFunc(answer, func(m string) string {
		state := "off"
		if strings.HasPrefix(m, `"totp_enabled":true`) {
			state = "on"
		}
		return m + `,"locked":false,"locked_until":null,"locks":[],"must_change_password":false,"two_factor_required":false,"two_factor_state":"` +
			state + `","two_factor_due":false,"password_reset_until":null`
	})
}

// is answers the same text on every database.
func is(answer string) func(string) string { return func(string) string { return answer } }

const (
	roleAboveOwn     = `{"success":false,"error":"Cannot assign a role higher than your own"}`
	invalidUsername  = `{"success":false,"error":"A username has 3 to 32 characters: letters, numbers and underscores only."}`
	invalidRole      = `{"success":false,"error":"Invalid role"}`
	cannotDemoteSelf = `{"success":false,"error":"You cannot demote yourself"}`
	invalidEmail     = `{"success":false,"error":"Enter a valid email address."}`
	emailTooLong     = `{"success":false,"error":"An email address has at most 200 characters."}`
	// The recordings' language file has no text for the key
	passwordTooShort = `{"success":false,"error":"users.password_too_short"}`
	// Refusals of values that were stored or dropped (L397)
	unknownUserGroup = `{"success":false,"error":"A chosen user group doesn't exist any more. Reload the page and choose again."}`
	unknownFolder    = `{"success":false,"error":"A chosen folder doesn't exist any more. Reload the page and choose again."}`
	strategyNotFound = `{"success":false,"error":"This RustDesk Pro strategy doesn't exist any more. Reload the page and choose again."}`
)

// refusedCreate is a new account the Go console refuses for a value Node
// stored or dropped without a word (BUGS L397): a user group or folder
// that doesn't exist, a role the server doesn't know. Node made the
// account with what it could read; its answer names the id, the user
// groups and the folders, which are written so the steps after it find
// the rows the recording's did.
func refusedCreate(answer string) fix {
	return fix{bug: "L397", status: http.StatusBadRequest, answer: is(answer), node: func(n *nodeWrites) {
		n.p.t.Helper()
		var node struct {
			Data struct {
				ID         json.Number `json:"id"`
				Username   string      `json:"username"`
				UserGroups []string    `json:"user_groups"`
				FolderIDs  []float64   `json:"folder_ids"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(n.step.Answer), &node); err != nil {
			n.p.t.Fatalf("step %q: the recorded answer: %v", n.step.Name, err)
		}
		id, err := node.Data.ID.Int64()
		if err != nil {
			n.p.t.Fatalf("step %q: the recorded id: %v", n.step.Name, err)
		}
		n.createdAccount(id, node.Data.Username, node.Data.Username)
		d := n.p.world.Server.Deps()
		if _, err := d.DeviceGroups.SetUserGroupMemberships(context.Background(), id, node.Data.UserGroups); err != nil {
			n.p.t.Fatal(err)
		}
		if len(node.Data.FolderIDs) > 0 {
			folders := make([]any, len(node.Data.FolderIDs))
			for i, f := range node.Data.FolderIDs {
				folders[i] = f
			}
			if _, err := d.Access.SyncUserFolderAccess(context.Background(), node.Data.Username, folders); err != nil {
				n.p.t.Fatal(err)
			}
		}
	}}
}

// ownAddressNeedsMail is the admin's change of its own address on a
// console without mail settings, which the Go console refuses (A272) and
// Node stored with its audit entry.
func ownAddressNeedsMail() fix {
	return fix{bug: "A272", status: http.StatusConflict,
		answer: is(`{"success":false,"error":"users.email_change_needs_mail","code":"email_change_needs_mail"}`),
		node: func(n *nodeWrites) {
			n.exec(`UPDATE users SET email = 'id@example.invalid' WHERE id = 1`)
			n.audit("user_updated", "Updated user: e2e_admin")
		}}
}

// fixFor is the fix of a step: the one for the recording's shape
// ("sqlite-go: 509 PATCH ..."), or the one for every shape.
func fixFor(rec recording, name string) (fix, bool) {
	variant := rec.Dialect
	if rec.Shape == "go" {
		variant += "-go"
	}
	if f, ok := fixes[variant+": "+name]; ok {
		return f, true
	}
	if f, ok := fixes[name]; ok {
		return f, true
	}
	// On the SQLite file the Node console set up, address_books is keyed
	// by the account's id and every rename failed from step 530 on, so the
	// accounts kept their names to the end of the recording. The Go
	// console renames there as on the other shapes (BUGS UM23); the
	// sqlite-go recording has the same requests, which Node carried out.
	if number, _, _ := strings.Cut(name, " "); variant == "sqlite" && len(number) == 3 && number >= "530" {
		return fix{bug: "UM23", pinnedBy: "sqlite-go"}, true
	}
	return fix{}, false
}

// deletedGroup is a group delete Node failed: the group's policy, which
// the delete empties first, has no section rows and needs no ceiling, so
// the Go console deletes the group (BUGS UM2). Node's group stayed, and
// the steps after it find it again.
var deletedGroup = fix{
	bug: "UM2", status: http.StatusOK, answer: is(`{"success":true}`),
	undo: []string{"user_groups", "user_group_members", "device_group_user_group_access", "panel_access_policies", "client_feature_policies", "audit_log"},
}

// fixes are the steps the Go console answers otherwise, by the step's
// name. The names of the steps before the renames (section 9 of the
// recording) are the same on every database shape; later ones carry the
// shape in front.
var fixes = map[string]fix{
	// A rename answers the group's members, which Node counted as 0
	// (L397)
	"151 PATCH /api/panel/user-groups/%20ug-beta%20": {bug: "L397", answer: replaced(`"member_count":0`, `"member_count":1`)},
	// Values the account routes stored or dropped without a word (L397)
	"199 POST /api/users":                           refusedCreate(unknownUserGroup),
	"204 POST /api/users":                           refusedCreate(unknownUserGroup),
	"209 POST /api/users":                           refusedCreate(unknownUserGroup),
	"233 POST /api/users":                           refusedCreate(unknownFolder),
	"257 POST /api/users":                           refusedCreate(invalidRole),
	"259 POST /api/users":                           refusedCreate(invalidRole),
	"269 POST /api/users":                           refusedCreate(invalidRole),
	"271 POST /api/users":                           refusedCreate(invalidRole),
	"281 POST /api/users":                           refusedCreate(invalidRole),
	"283 POST /api/users":                           refusedCreate(invalidRole),
	"161 DELETE /api/panel/user-groups/{patched}":   deletedGroup,
	"162 DELETE /api/panel/user-groups/{patched}":   deletedGroup,
	"163 DELETE /api/panel/user-groups/{byGlobal}":  deletedGroup,
	"164 DELETE /api/panel/user-groups/%20ug-gamma": deletedGroup,
	// A username that is no text: Node created viewers named as each
	// database's driver stored the value; true failed on SQLite.
	"190 POST /api/users": {bug: "US6", status: http.StatusBadRequest, answer: is(invalidUsername), node: func(n *nodeWrites) {
		n.createdAccount(22, n.pick("12345.0", "12345"), "12345")
	}},
	"191 POST /api/users": {bug: "US6", status: http.StatusBadRequest, answer: is(invalidUsername), node: func(n *nodeWrites) {
		n.createdAccount(23, "1.5", "1.5")
	}},
	"192 POST /api/users": {bug: "US6", status: http.StatusBadRequest, answer: is(invalidUsername), node: func(n *nodeWrites) {
		if n.p.db.IsPostgres() {
			n.createdAccount(24, "true", "true")
		}
	}},
	"193 POST /api/users": {bug: "US6", status: http.StatusBadRequest, answer: is(invalidUsername), node: func(n *nodeWrites) {
		if n.p.db.IsPostgres() {
			n.createdAccount(25, `{"abc"}`, "abc")
		} else {
			n.createdAccount(24, "abc", "abc")
		}
	}},
	"196 POST /api/users": {bug: "US6", status: http.StatusBadRequest, answer: is(invalidUsername)},

	// A role change and a deleted account end every session of the
	// account, which Node's went on with: the test accounts that are
	// changed sign in again
	// A directory account gets its address from the directory: the Go
	// console refuses to change it (the account life cycle), Node stored it
	"339 PATCH /api/users/9": {bug: "LIFE1", status: http.StatusBadRequest, answer: is(`{"success":false,"error":"users.email_from_provider"}`), node: func(n *nodeWrites) {
		n.exec(`UPDATE users SET email = 'ldap@example.invalid' WHERE id = 9`)
		n.audit("user_updated", "Updated user: ldap_op")
	}},
	// The admin's own address (1.5 is account 1): without mail settings
	// nothing can confirm a new one, so the Go console refuses it (A272),
	// Node stored it. Step 333 sends the same address again.
	"329 PATCH /api/users/1.5":       ownAddressNeedsMail(),
	"346 PATCH /api/users/2":         {bug: "S9", endsSessionsOf: []string{"super_admin"}},
	"348 PATCH /api/users/1":         {bug: "S9", endsSessionsOf: []string{"admin"}},
	"349 PATCH /api/users/4":         {bug: "S9", endsSessionsOf: []string{"global_admin"}},
	"473 text id PATCH /api/users/2": {bug: "S9", endsSessionsOf: []string{"super_admin"}},
	"474 DELETE /api/users/2":        {bug: "S9", endsSessionsOf: []string{"super_admin"}},

	// global_admin changes and deletes accounts above its own role. Once
	// Node made target_super a viewer, global_admin may change it.
	"363 PATCH /api/users/12": {bug: "US1", status: http.StatusForbidden, answer: is(roleAboveOwn), node: func(n *nodeWrites) {
		n.exec(`UPDATE users SET role = 'viewer' WHERE id = 12`)
		n.audit("user_updated", "Updated user: target_super")
	}},
	"459 DELETE /api/users/11": {bug: "US1", status: http.StatusForbidden, answer: is(roleAboveOwn), node: func(n *nodeWrites) {
		n.deleteAccount(11)
		n.audit("user_deleted", "Deleted user: target_admin")
	}},
	"477 DELETE /api/users/16": {bug: "US1", status: http.StatusForbidden, answer: is(roleAboveOwn)},
	"478 PATCH /api/users/16": {bug: "US1", status: http.StatusForbidden, answer: is(roleAboveOwn), node: func(n *nodeWrites) {
		n.exec(`UPDATE users SET role = 'operator' WHERE id = 16`)
		n.audit("user_updated", "Updated user: target_glob")
	}},
	"480 DELETE /api/users/16": {bug: "US1", status: http.StatusForbidden, answer: is(roleAboveOwn), node: func(n *nodeWrites) {
		n.deleteAccount(16)
		n.audit("user_deleted", "Deleted user: target_glob")
	}},

	// Node set a password on a directory account, which signs in with its
	// directory's: the Go console refuses and names the provider (PW1)
	"447 POST /api/users/9/reset-password": {bug: "PW1", status: http.StatusBadRequest, answer: is(`{"success":false,"error":"users.external_password"}`), node: func(n *nodeWrites) {
		n.exec(`UPDATE users SET password_hash = 'node-hash' WHERE id = 9`)
		n.audit("password_reset", "Reset password for user: ldap_op")
	}},

	// A session whose id is text: Node let the admin delete its own
	// account. On PostgreSQL the audit entry then failed its foreign key.
	"472 text id DELETE /api/users/1": {bug: "US3", status: http.StatusBadRequest,
		answer: is(`{"success":false,"error":"You cannot delete your own account"}`), node: func(n *nodeWrites) {
			n.deleteAccount(1)
			if !n.p.db.IsPostgres() {
				n.audit("user_deleted", "Deleted user: e2e_admin")
			}
		}},

	// The answer to a new account: Node listed the user group guids as
	// they were sent, and no email for null.
	"226 POST /api/users": {bug: "US7", answer: replaced(`"role":"viewer","user_groups"`, `"role":"viewer","email":"","user_groups"`)},

	// Checks after writes: Node created the account, then refused its
	// email or strategy. The strategy gave it its guid first.
	"227 POST /api/users": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) { n.account(n.id(40, 41), "mail_bad") }},
	"228 POST /api/users": {bug: "US4", answer: is(emailTooLong), node: func(n *nodeWrites) { n.account(n.id(41, 42), "mail_long") }},
	"229 POST /api/users": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) { n.account(n.id(42, 43), "mail_num") }},
	"230 POST /api/users": {bug: "US4", node: func(n *nodeWrites) { n.account(n.id(43, 44), "mail_tostr") }},
	"231 POST /api/users": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) { n.account(n.id(44, 45), "mail_true") }},
	// An unknown strategy is 400, not 500 (L397)
	"239 POST /api/users": {bug: "US4, L397", status: http.StatusBadRequest, answer: is(strategyNotFound), node: func(n *nodeWrites) {
		id := n.id(52, 53)
		n.account(id, "scope_missing")
		n.guid(id)
	}},
	// Node wrote the role before the weak password, the folders before the
	// unknown strategy, the user groups before the invalid email
	"366 PATCH /api/users/13": {bug: "US4, L94", answer: is(passwordTooShort), node: func(n *nodeWrites) { n.exec(`UPDATE users SET role = 'viewer' WHERE id = 13`) }},
	"378 PATCH /api/users/13": {bug: "US4, L397", status: http.StatusBadRequest, answer: is(strategyNotFound), node: func(n *nodeWrites) {
		if _, err := n.p.world.Server.Deps().Access.SyncUserFolderAccess(context.Background(), "target_op", []any{1.0}); err != nil {
			n.p.t.Fatal(err)
		}
	}},
	"381 PATCH /api/users/13": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) {
		if _, err := n.p.world.Server.Deps().DeviceGroups.SetUserGroupMemberships(context.Background(), 13, []string{"ug-beta"}); err != nil {
			n.p.t.Fatal(err)
		}
	}},
	// A new name with a role or a demotion that is refused: Node renamed
	// first. Where Node made address_books itself, the rename failed.
	"sqlite: 501 PATCH /api/users/17":             {bug: "US4", status: http.StatusBadRequest, answer: is(invalidRole)},
	"sqlite: 542 PATCH /api/users/{rnAlpha}":      {bug: "US4", status: http.StatusBadRequest, answer: is(invalidRole)},
	"sqlite: 555 PATCH /api/users/2":              {bug: "US4", status: http.StatusBadRequest, answer: is(cannotDemoteSelf)},
	"sqlite-go: 509 PATCH /api/users/17":          {bug: "US4", node: renamedTo("srv_renamed")},
	"sqlite-go: 554 PATCH /api/users/{rnAlpha}":   {bug: "US4", node: renamedTo("rn_final")},
	"sqlite-go: 571 PATCH /api/users/2":           {bug: "US4", node: renamedTo("super_self")},
	"postgres: 505 PATCH /api/users/17":           {bug: "US4", node: renamedTo("srv_renamed")},
	"postgres: 548 PATCH /api/users/{rnAlpha}":    {bug: "US4", node: renamedTo("rn_final")},
	"postgres: 562 PATCH /api/users/2":            {bug: "US4", node: renamedTo("super_self")},
	"postgres-go: 509 PATCH /api/users/17":        {bug: "US4", node: renamedTo("srv_renamed")},
	"postgres-go: 554 PATCH /api/users/{rnAlpha}": {bug: "US4", node: renamedTo("rn_final")},
	"postgres-go: 571 PATCH /api/users/2":         {bug: "US4", node: renamedTo("super_self")},

	// Node counted creates and resets against the password change limit of
	// three in five minutes per address, and refused with 429 the admin's
	// fourth account and fifth request from one address, the super admin's
	// account after three resets and the admin's fourth empty create. They
	// count against the API limit alone; what the Go console wrote for them
	// is taken back. The new account's id differs by shape.
	"sqlite: 310 POST /api/users":      {bug: "C4", status: http.StatusOK, answer: newViewer("93", "limit_3"), node: uncreated("limit_3")},
	"sqlite-go: 310 POST /api/users":   {bug: "C4", status: http.StatusOK, answer: newViewer("93", "limit_3"), node: uncreated("limit_3")},
	"postgres: 310 POST /api/users":    {bug: "C4", status: http.StatusOK, answer: newViewer("94", "limit_3"), node: uncreated("limit_3")},
	"postgres-go: 310 POST /api/users": {bug: "C4", status: http.StatusOK, answer: newViewer(`"94"`, "limit_3"), node: uncreated("limit_3")},
	"311 POST /api/users/13/reset-password": {bug: "C4", status: http.StatusOK, answer: is(`{"success":true}`), node: func(n *nodeWrites) {
		n.exec(`UPDATE users SET password_hash = 'x' WHERE id = 13`)
		n.exec(`DELETE FROM audit_log WHERE id = (SELECT MAX(id) FROM audit_log WHERE action = 'password_reset')`)
	}},
	"sqlite: 315 POST /api/users":      {bug: "C4", status: http.StatusOK, answer: newViewer("93", "limit_after_reset"), node: uncreated("limit_after_reset")},
	"sqlite-go: 315 POST /api/users":   {bug: "C4", status: http.StatusOK, answer: newViewer("93", "limit_after_reset"), node: uncreated("limit_after_reset")},
	"postgres: 315 POST /api/users":    {bug: "C4", status: http.StatusOK, answer: newViewer("94", "limit_after_reset"), node: uncreated("limit_after_reset")},
	"postgres-go: 315 POST /api/users": {bug: "C4", status: http.StatusOK, answer: newViewer(`"94"`, "limit_after_reset"), node: uncreated("limit_after_reset")},
	"325 POST /api/users":              {bug: "C4", status: http.StatusBadRequest, answer: is(`{"success":false,"error":"Please fill in all required fields"}`)},

	// One password rule: at least 8 characters of text, no strength score.
	// Node refused weak passwords with their feedback and failed with 500
	// on some that are no text; the Go console refuses every one under 8
	// characters or no text as too short, and takes 8 lower-case letters,
	// whose account is taken back so the next one gets Node's id.
	"211 POST /api/users":              {bug: "L94", answer: is(passwordTooShort)},
	"sqlite: 212 POST /api/users":      {bug: "L94", status: http.StatusOK, answer: newViewer("33", "weak_lower"), node: uncreated("weak_lower")},
	"sqlite-go: 212 POST /api/users":   {bug: "L94", status: http.StatusOK, answer: newViewer("33", "weak_lower"), node: uncreated("weak_lower")},
	"postgres: 212 POST /api/users":    {bug: "L94", status: http.StatusOK, answer: newViewer("34", "weak_lower"), node: uncreated("weak_lower")},
	"postgres-go: 212 POST /api/users": {bug: "L94", status: http.StatusOK, answer: newViewer(`"34"`, "weak_lower"), node: uncreated("weak_lower")},
	"215 POST /api/users":              {bug: "L94", answer: is(passwordTooShort)},
	"216 POST /api/users":              {bug: "L94", answer: is(passwordTooShort)},
	"217 POST /api/users":              {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"218 POST /api/users":              {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"219 POST /api/users":              {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"220 POST /api/users":              {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"221 POST /api/users":              {bug: "L94", answer: is(passwordTooShort)},
	"297 POST /api/users":              {bug: "L94", answer: is(passwordTooShort)},
	// The password is checked before the role
	"298 POST /api/users":                   {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"367 PATCH /api/users/13":               {bug: "L94", answer: is(passwordTooShort)},
	"368 PATCH /api/users/13":               {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"442 POST /api/users/14/reset-password": {bug: "L94", answer: is(passwordTooShort)},
	"443 POST /api/users/14/reset-password": {bug: "L94", answer: is(passwordTooShort)},
	"444 POST /api/users/14/reset-password": {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	"445 POST /api/users/14/reset-password": {bug: "L94", status: http.StatusBadRequest, answer: is(passwordTooShort)},
	// guest_issuer is given by the roles that give viewer, where Node made
	// a viewer of it. The account is a viewer again for the steps after.
	"256 POST /api/users": {bug: "L108", answer: replaced(`"role":"viewer"`, `"role":"guest_issuer"`), node: asViewer("by_global_admin_7")},
	"268 POST /api/users": {bug: "L108", answer: replaced(`"role":"viewer"`, `"role":"guest_issuer"`), node: asViewer("by_super_admin_7")},
	"280 POST /api/users": {bug: "L108", answer: replaced(`"role":"viewer"`, `"role":"guest_issuer"`), node: asViewer("by_admin_7")},
	// global_admin makes an account guest_issuer, where Node refused the
	// role. The account is a viewer again for the steps after, and the
	// change leaves no audit entry.
	"sqlite: 357 PATCH /api/users/18":      madeGuestIssuer(`{"success":true,"data":{"id":18,"username":"target_view","role":"guest_issuer","email":"","auth_provider":"local","created_at":"2026-11-18 16:47:14","last_login":null,"user_groups":[],"folder_ids":[],"peer_grants":[],"strategy_guid":"","totp_enabled":false}}`),
	"sqlite-go: 357 PATCH /api/users/18":   madeGuestIssuer(`{"success":true,"data":{"id":18,"username":"target_view","role":"guest_issuer","email":"","auth_provider":"local","created_at":"2026-11-18 16:47:50","last_login":"","user_groups":[],"folder_ids":[],"peer_grants":[],"strategy_guid":"","totp_enabled":false}}`),
	"postgres: 357 PATCH /api/users/18":    madeGuestIssuer(`{"success":true,"data":{"id":18,"username":"target_view","role":"guest_issuer","email":"","auth_provider":"local","created_at":"2026-11-18T16:48:29.026Z","last_login":null,"user_groups":[],"folder_ids":[],"peer_grants":[],"strategy_guid":"","totp_enabled":false}}`),
	"postgres-go: 357 PATCH /api/users/18": madeGuestIssuer(`{"success":true,"data":{"id":"18","username":"target_view","role":"guest_issuer","email":"","auth_provider":"local","created_at":"2026-11-18T16:49:17.380Z","last_login":null,"user_groups":[],"folder_ids":[],"peer_grants":[],"strategy_guid":"","totp_enabled":false}}`),
}

// madeGuestIssuer is the fix of a role change to guest_issuer that Node
// refused with 400: the Go console's answer, and the account and audit
// log put back as Node left them.
func madeGuestIssuer(answer string) fix {
	return fix{bug: "L108", status: http.StatusOK, answer: is(answer), node: func(n *nodeWrites) {
		n.exec(`UPDATE users SET role = 'viewer' WHERE id = 18`)
		n.exec(`DELETE FROM audit_log WHERE id = (SELECT MAX(id) FROM audit_log WHERE action = 'user_updated')`)
	}}
}

// asViewer makes the account a viewer, as Node's request left it, also in
// the audit entry of its creation.
func asViewer(username string) func(n *nodeWrites) {
	return func(n *nodeWrites) {
		n.exec(`UPDATE users SET role = 'viewer' WHERE username = ?`, username)
		n.exec(`UPDATE audit_log SET details = REPLACE(details, '(guest_issuer)', '(viewer)') WHERE id = (SELECT MAX(id) FROM audit_log WHERE action = 'user_created')`)
	}
}

// newViewer is the answer to a new account with a username and a password
// alone. id is written as the shape's driver gave it.
func newViewer(id, username string) func(string) string {
	return is(`{"success":true,"data":{"id":` + id + `,"username":"` + username + `","role":"viewer","email":"","user_groups":[],"folder_ids":[],"peer_grants":[]}}`)
}

// uncreated takes back an account the Go console created and Node did
// not: its row, its audit entry and the id it took, so the next account
// gets the id Node gave it.
func uncreated(username string) func(n *nodeWrites) {
	return func(n *nodeWrites) {
		n.p.t.Helper()
		var id int64
		if err := n.p.db.QueryRow(context.Background(), `SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
			n.p.t.Fatalf("step %q, the account %s: %v", n.step.Name, username, err)
		}
		n.exec(`DELETE FROM users WHERE id = ?`, id)
		n.exec(`DELETE FROM audit_log WHERE id = (SELECT MAX(id) FROM audit_log WHERE action = 'user_created')`)
		if n.p.db.IsPostgres() {
			n.exec(`SELECT setval(pg_get_serial_sequence('users', 'id'), ?)`, id-1)
		} else {
			n.exec(`UPDATE sqlite_sequence SET seq = ? WHERE name = 'users'`, id-1)
		}
	}
}

// renamedTo gives the step's account the name Node gave it, through the
// route: the rename, the rows keyed by the name and the rename's audit
// entry. Node notes the change as a whole only when it went through, and
// had no answer that could give the account a guid, so the entry goes
// again and the guid stays as it was.
func renamedTo(name string) func(n *nodeWrites) {
	return func(n *nodeWrites) {
		n.p.t.Helper()
		ctx := context.Background()
		target := n.p.fill(n.step.Path)
		id := target[strings.LastIndex(target, "/")+1:]
		var guid sql.NullString
		if err := n.p.db.QueryRow(ctx, `SELECT guid FROM users WHERE CAST(id AS TEXT) = ?`, id).Scan(&guid); err != nil {
			n.p.t.Fatalf("step %q, account %s: %v", n.step.Name, id, err)
		}
		got := n.p.agent(n.step.Role).Do(n.step.Method, target, `{"username":"`+name+`"}`, "X-Forwarded-For", n.step.Headers["X-Forwarded-For"])
		if got.Status != http.StatusOK {
			n.p.t.Fatalf("step %q, Node's rename: %d %s", n.step.Name, got.Status, got.Body)
		}
		n.p.api.Calls()
		n.exec(`DELETE FROM audit_log WHERE id = (SELECT MAX(id) FROM audit_log WHERE action = 'user_updated')`)
		n.exec(`UPDATE users SET guid = ? WHERE CAST(id AS TEXT) = ?`, guid, id)
	}
}

// nodeWrites writes what the request of a step wrote in Node.
type nodeWrites struct {
	p    *replayer
	step recordedStep
}

func (n *nodeWrites) exec(query string, args ...any) {
	n.p.t.Helper()
	if _, err := n.p.db.Exec(context.Background(), query, args...); err != nil {
		n.p.t.Fatalf("step %q, Node's writes: %s: %v", n.step.Name, query, err)
	}
}

// audit writes the audit entry of the step's caller and address.
func (n *nodeWrites) audit(action, details string) {
	n.p.t.Helper()
	d := n.p.world.Server.Deps()
	if err := d.Audit.Write(context.Background(), routetest.RoleIDs[n.step.Role], action, details, n.step.Headers["X-Forwarded-For"]); err != nil {
		n.p.t.Fatalf("step %q, Node's audit entry: %v", n.step.Name, err)
	}
}

// pick is the value for SQLite or for PostgreSQL.
func (n *nodeWrites) pick(sqlite, postgres string) string {
	if n.p.db.IsPostgres() {
		return postgres
	}
	return sqlite
}

// id is the id Node gave an account on SQLite or on PostgreSQL, where
// the username true made one account more.
func (n *nodeWrites) id(sqlite, postgres int64) int64 {
	if n.p.db.IsPostgres() {
		return postgres
	}
	return sqlite
}

// account stores a viewer a create made, with its id and its username as
// it was stored. PostgreSQL's id sequence moves past it.
func (n *nodeWrites) account(id int64, stored string) {
	n.p.t.Helper()
	n.exec(`INSERT INTO users (id, username, password_hash, role, auth_provider) VALUES (?, ?, 'set by Node', 'viewer', 'local')`, id, stored)
	if n.p.db.IsPostgres() {
		n.exec(`SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`)
	}
}

// createdAccount is account with the audit entry of a create that went
// through, which names the account as it was sent.
func (n *nodeWrites) createdAccount(id int64, stored, sent string) {
	n.p.t.Helper()
	n.account(id, stored)
	n.audit("user_created", "Created user: "+sent+" (viewer)")
}

// guid gives an account a random guid.
func (n *nodeWrites) guid(id int64) {
	n.p.t.Helper()
	n.exec(`UPDATE users SET guid = ? WHERE id = ?`, randomGUID(n.p.t), id)
}

// randomGUID is a random guid as crypto.randomUUID makes them.
func randomGUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// nodeGUIDs gives every account a recorded answer showed with its
// strategy (the list, the answer to a change) a guid when it has none, as
// Node did: it read the strategy through a call that made the guid (BUGS
// US8). The Go console gives an account its guid only when a strategy is
// assigned to it.
func (p *replayer) nodeGUIDs(step recordedStep) {
	p.t.Helper()
	if step.Status != http.StatusOK {
		return
	}
	var answer struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(step.Answer), &answer) != nil || answer.Data == nil {
		return
	}
	accounts := []json.RawMessage{answer.Data}
	var list struct {
		Users []json.RawMessage `json:"users"`
	}
	if json.Unmarshal(answer.Data, &list) == nil && list.Users != nil {
		accounts = list.Users
	}
	for _, raw := range accounts {
		var account map[string]json.RawMessage
		if json.Unmarshal(raw, &account) != nil {
			continue
		}
		if _, shown := account["strategy_guid"]; !shown {
			continue
		}
		id := strings.Trim(string(account["id"]), `"`)
		if _, err := p.db.Exec(context.Background(), `UPDATE users SET guid = ? WHERE CAST(id AS TEXT) = ? AND (guid IS NULL OR guid = '')`, randomGUID(p.t), id); err != nil {
			p.t.Fatalf("step %q, Node's guid of account %s: %v", step.Name, id, err)
		}
	}
}

// deleteAccount deletes an account as the route does: its panel access
// policy, its user group memberships, its places on device group access
// lists, its picture and its row.
func (n *nodeWrites) deleteAccount(id int64) {
	n.p.t.Helper()
	d := n.p.world.Server.Deps()
	if _, err := d.Access.SavePolicy(context.Background(), access.SubjectUser, strconv.FormatInt(id, 10), nil, nil); err != nil {
		n.p.t.Fatalf("step %q, Node's writes: %v", n.step.Name, err)
	}
	for _, query := range []string{
		`DELETE FROM user_group_members WHERE user_id = ?`,
		`DELETE FROM device_group_user_access WHERE user_id = ?`,
		`DELETE FROM user_avatars WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		n.exec(query, id)
	}
}

// nodeUserLabel is the user label rule of Node's audit log: the name after
// "user:" or "username:" anywhere in the text, up to white space.
var nodeUserLabel = regexp.MustCompile(`\b([Uu][Ss][Ee][Rr](?:[Nn][Aa][Mm][Ee])?):\s*(\S+)`)

// auditDetails finds the details of audit rows as p.rows writes them
// (text without quotes or escapes in it).
var auditDetails = regexp.MustCompile(`"details":"([^"\\]*)"`)

// nodeLabels writes the details of audit rows the Go console stored as
// Node stored them: Node shortened the name after a user label anywhere in
// the text, so "Created user: frank" became "Created user: f***k"; the
// console keeps the names of the accounts the user management changed
// (BUGS.md AL1).
func nodeLabels(rows string) string {
	return auditDetails.ReplaceAllStringFunc(rows, func(m string) string {
		details := auditDetails.FindStringSubmatch(m)[1]
		return `"details":"` + nodeUserLabel.ReplaceAllStringFunc(details, func(label string) string {
			parts := nodeUserLabel.FindStringSubmatch(label)
			return parts[1] + ": " + auditlog.RedactUsername(parts[2])
		}) + `"`
	})
}
