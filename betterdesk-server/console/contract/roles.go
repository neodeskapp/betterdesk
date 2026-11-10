package contract

import "fmt"

// Role is who sends a request: one of the console's roles, or nobody.
type Role string

// SignedOut is a browser without a session.
const SignedOut Role = "signed-out"

// AllRoles is every role the suite runs as, least privilege first. This is
// the order requests are sent in and the order roles are listed in a golden.
// Role pro cannot sign in to the console: its requests go out with the
// cookies its refused sign-in left.
var AllRoles = []Role{
	SignedOut,
	"pro",
	"guest_issuer",
	"viewer",
	"operator",
	"global_admin",
	"server_admin",
	"super_admin",
	"admin",
}

// Roles picks roles by name for a case. It panics on a name that is not in
// AllRoles, so a typo shows when the cases are registered.
func Roles(names ...string) []Role {
	out := make([]Role, 0, len(names))
	for _, name := range names {
		role := Role(name)
		if roleIndex(role) < 0 {
			panic(fmt.Sprintf("contract: unknown role %q", name))
		}
		out = append(out, role)
	}
	return out
}

// Account is the test account of a role: e2e_<role>. SignedOut has none.
func (r Role) Account() string {
	if r == SignedOut {
		return ""
	}
	return "e2e_" + string(r)
}

func roleIndex(r Role) int {
	for i, known := range AllRoles {
		if known == r {
			return i
		}
	}
	return -1
}

// sortRoles returns the roles in the order of AllRoles, each once.
func sortRoles(roles []Role) []Role {
	has := make(map[Role]bool, len(roles))
	for _, r := range roles {
		has[r] = true
	}
	out := make([]Role, 0, len(has))
	for _, r := range AllRoles {
		if has[r] {
			out = append(out, r)
		}
	}
	return out
}
