package users_test

import (
	"context"
	"net/http"
	"strconv"

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

// is answers the same text on every database.
func is(answer string) func(string) string { return func(string) string { return answer } }

const (
	roleAboveOwn    = `{"success":false,"error":"Cannot assign a role higher than your own"}`
	invalidUsername = `{"success":false,"error":"A username has 3 to 32 characters: letters, numbers and underscores only."}`
)

// fixes are the steps the Go console answers otherwise, by the step's
// name. The names of the steps before the renames (section 9 of the
// oracle) are the same on every database shape.
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

// createdAccount stores the viewer a create made, with its id, as the
// username was stored and with the audit entry that names it as it was
// sent. PostgreSQL's id sequence moves past it.
func (n *nodeWrites) createdAccount(id int64, stored, sent string) {
	n.p.t.Helper()
	n.exec(`INSERT INTO users (id, username, password_hash, role, auth_provider) VALUES (?, ?, 'set by Node', 'viewer', 'local')`, id, stored)
	if n.p.db.IsPostgres() {
		n.exec(`SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))`)
	}
	n.audit("user_created", "Created user: "+sent+" (viewer)")
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
