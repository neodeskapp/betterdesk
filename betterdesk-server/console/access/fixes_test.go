package access

import (
	"context"
	"testing"
)

// The vectors in testdata were recorded from the console's Node modules
// with Node's guard rules and catalog. The bug fixes changed both tables
// on purpose. The oracle tests therefore run on Node's tables
// (withNodeTables): they pin what the code does with a table. The changes
// of the tables are pinned here against Node's, each with the bug it
// fixes. Where a fix changed the code itself, the oracle test lists the
// vectors that fix changes.

// ruleFix is a change of the guard's rule table against Node's: a rule
// that goes (of two equal ones the later), a rule that comes, or both for
// a rule replaced in its place. Rules that come without replacing one are
// at the end of the table, in the order listed.
type ruleFix struct {
	bug         string
	remove, add Rule
}

var ruleFixes = []ruleFix{
	// The routes for people of the file transfer group answer at /api/bd
	// too.
	{bug: "FT8", add: Rule{Prefix: "/api/bd/transfer", Section: "files"}},
	{bug: "FT8", add: Rule{Prefix: "/api/bd/transfers", Section: "files"}},
}

// sectionFix is a catalog section whose permissions differ from Node's.
type sectionFix struct {
	bug         string
	key         string
	read, write Permission
}

var sectionFixes = []sectionFix{}

// nodeTables returns the guard's rules and the catalog's sections as the
// Node console had them.
func nodeTables(t *testing.T) ([]Rule, []Section) {
	t.Helper()
	var rules struct {
		Rules []Rule `json:"rules"`
	}
	loadVectors(t, "find_rule", &rules)
	var normalize struct {
		Catalog struct {
			Sections []struct {
				Key   string  `json:"key"`
				Label string  `json:"label"`
				Group string  `json:"group"`
				Read  *string `json:"readPermission"`
				Write *string `json:"writePermission"`
			} `json:"sections"`
		} `json:"catalog"`
	}
	loadVectors(t, "normalize", &normalize)
	permission := func(p *string) Permission {
		if p == nil {
			return ""
		}
		return Permission(*p)
	}
	catalog := make([]Section, 0, len(normalize.Catalog.Sections))
	for _, s := range normalize.Catalog.Sections {
		catalog = append(catalog, Section{s.Key, s.Label, s.Group, permission(s.Read), permission(s.Write)})
	}
	if len(rules.Rules) < 60 || len(catalog) < 30 {
		t.Fatalf("Node's tables look empty: %d rules, %d sections", len(rules.Rules), len(catalog))
	}
	return rules.Rules, catalog
}

// withNodeTables puts Node's rule table and catalog in place of the Go
// ones until the test ends. The tests of this package do not run in
// parallel, so nothing else reads the tables meanwhile.
func withNodeTables(t *testing.T) {
	t.Helper()
	rules, catalog := nodeTables(t)
	goRules, goCatalog := RouteRules, Catalog
	RouteRules, Catalog = rules, catalog
	t.Cleanup(func() { RouteRules, Catalog = goRules, goCatalog })
}

func TestTablesAreNodesWithTheFixes(t *testing.T) {
	rules, catalog := nodeTables(t)
	want := append([]Rule(nil), rules...)
	for _, fix := range ruleFixes {
		if fix.remove == (Rule{}) {
			want = append(want, fix.add)
			continue
		}
		found := false
		for i := len(want) - 1; i >= 0 && !found; i-- {
			if want[i] != fix.remove {
				continue
			}
			found = true
			if fix.add == (Rule{}) {
				want = append(want[:i:i], want[i+1:]...)
			} else {
				want[i] = fix.add
			}
		}
		if !found {
			t.Fatalf("%s: Node has no rule %+v", fix.bug, fix.remove)
		}
	}
	for i := 0; i < len(RouteRules) || i < len(want); i++ {
		var got, wanted Rule
		if i < len(RouteRules) {
			got = RouteRules[i]
		}
		if i < len(want) {
			wanted = want[i]
		}
		if got != wanted {
			t.Errorf("rule %d is %+v, Node's table with the fixes has %+v", i, got, wanted)
		}
	}

	wantCatalog := append([]Section(nil), catalog...)
	for _, fix := range sectionFixes {
		found := false
		for i := range wantCatalog {
			if wantCatalog[i].Key == fix.key {
				wantCatalog[i].ReadPermission, wantCatalog[i].WritePermission = fix.read, fix.write
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: Node has no section %q", fix.bug, fix.key)
		}
	}
	if len(Catalog) != len(wantCatalog) {
		t.Fatalf("%d catalog sections, Node's catalog with the fixes has %d", len(Catalog), len(wantCatalog))
	}
	for i := range Catalog {
		if Catalog[i] != wantCatalog[i] {
			t.Errorf("section %d is %+v, Node's catalog with the fixes has %+v", i, Catalog[i], wantCatalog[i])
		}
	}
}

// defaultRoles are the roles below the super admins, whose answers the
// panel access fixes change.
var defaultRoles = []string{"viewer", "operator", "guest_issuer", "global_admin", "server_admin"}

// roleContext is the access context of a signed-in account with a default
// role and no policy rows: its built-in permissions and its role's ceiling
// in every section.
func roleContext(t *testing.T, role string) *Context {
	t.Helper()
	permissions := defaultPermissions(role)
	s := fakeService(&fakeData{}, &fakeRoles{permissions: map[string][]string{role: permissions}})
	policy, err := s.ResolveForUser(context.Background(), PolicyUser{ID: 7, Role: role})
	if err != nil {
		t.Fatalf("policy of %s: %v", role, err)
	}
	return &Context{UserID: 7, HasUser: true, Role: role, Loaded: true, Permissions: newPermissionSet(permissions), Policy: policy}
}

// routeAnswer is what a default role gets from the guard and a route's own
// permission check: "ok", "guard" (the guard refuses) or "route" (the
// guard lets it through and the route refuses).
func routeAnswer(t *testing.T, role, method, path, permission string) string {
	t.Helper()
	c := roleContext(t, role)
	if got := c.Guard(method, path); !got.Decision.Passed() {
		return "guard"
	}
	if permission != "" && !c.HasPermission(permission) {
		return "route"
	}
	return "ok"
}

// routeCase is a route with its own check and the answer every default
// role gets, in the order of defaultRoles.
type routeCase struct {
	method, path, permission string
	answers                  [5]string
}

func checkRouteCases(t *testing.T, cases []routeCase) {
	t.Helper()
	for _, c := range cases {
		for i, role := range defaultRoles {
			if got := routeAnswer(t, role, c.method, c.path, c.permission); got != c.answers[i] {
				t.Errorf("%s %s (%s) as %s: %s, want %s", c.method, c.path, c.permission, role, got, c.answers[i])
			}
		}
	}
}
