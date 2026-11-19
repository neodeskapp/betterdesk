package users_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
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
	// not, after the step, so the steps after it start from the rows the
	// recording's did. nil writes nothing.
	node func(n *nodeWrites)
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

// guidList is "g<from>",...,"g<to>" as a JSON list writes it.
func guidList(from, to int) string {
	parts := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		parts = append(parts, `"g`+strconv.Itoa(i)+`"`)
	}
	return strings.Join(parts, ",")
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
)

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
	f, ok := fixes[name]
	return f, ok
}

// fixes are the steps the Go console answers otherwise, by the step's
// name. The names of the steps before the renames (section 9 of the
// oracle) are the same on every database shape; later ones carry the
// shape in front.
var fixes = map[string]fix{
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
	"199 POST /api/users": {bug: "US7", answer: replaced(`"user_groups":["ug-alpha","ug-beta","missing"]`, `"user_groups":["ug-alpha","ug-beta"]`)},
	"204 POST /api/users": {bug: "US7", answer: replaced(`"user_groups":["5"]`, `"user_groups":[]`)},
	"209 POST /api/users": {bug: "US7", answer: replaced(`"user_groups":["g0",`+guidList(1, 99)+`]`, `"user_groups":[]`)},
	"226 POST /api/users": {bug: "US7", answer: replaced(`"role":"viewer","user_groups"`, `"role":"viewer","email":"","user_groups"`)},

	// Checks after writes: Node created the account, then refused its
	// email or strategy. The strategy gave it its guid first.
	"227 POST /api/users": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) { n.account(n.id(40, 41), "mail_bad") }},
	"228 POST /api/users": {bug: "US4", answer: is(emailTooLong), node: func(n *nodeWrites) { n.account(n.id(41, 42), "mail_long") }},
	"229 POST /api/users": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) { n.account(n.id(42, 43), "mail_num") }},
	"230 POST /api/users": {bug: "US4", node: func(n *nodeWrites) { n.account(n.id(43, 44), "mail_tostr") }},
	"231 POST /api/users": {bug: "US4", answer: is(invalidEmail), node: func(n *nodeWrites) { n.account(n.id(44, 45), "mail_true") }},
	"239 POST /api/users": {bug: "US4", node: func(n *nodeWrites) {
		id := n.id(52, 53)
		n.account(id, "scope_missing")
		n.guid(id)
	}},
	// Node wrote the role before the weak password, the folders before the
	// unknown strategy, the user groups before the invalid email
	"366 PATCH /api/users/13": {bug: "US4", node: func(n *nodeWrites) { n.exec(`UPDATE users SET role = 'viewer' WHERE id = 13`) }},
	"378 PATCH /api/users/13": {bug: "US4", node: func(n *nodeWrites) {
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
