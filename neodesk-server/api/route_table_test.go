package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// guard is what the route table puts in front of one route's handler.
type guard struct {
	permissions []string // requirePermission, requireAnyPermission
	roles       []string // requireRole
	wrappers    []string // the names of the other s.* wrappers
}

func (g guard) has(wrapper string) bool {
	for _, w := range g.wrappers {
		if w == wrapper {
			return true
		}
	}
	return false
}

// constants reads the string constants of the auth package by name.
func constants(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	files, err := filepath.Glob(filepath.Join("..", "auth", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							out[id.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
		}
	}
	return out
}

// routeTable parses the package's source: every mux.HandleFunc call, by
// its pattern, with the guard of its handler.
func routeTable(t *testing.T) map[string]guard {
	t.Helper()
	consts := constants(t)
	value := func(e ast.Expr) string {
		if sel, ok := e.(*ast.SelectorExpr); ok {
			if v, ok := consts[sel.Sel.Name]; ok {
				return v
			}
			return sel.Sel.Name
		}
		if lit, ok := e.(*ast.BasicLit); ok {
			v, _ := strconv.Unquote(lit.Value)
			return v
		}
		return "?"
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]guard{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "mux" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			pattern, _ := strconv.Unquote(lit.Value)
			var g guard
			ast.Inspect(call.Args[1], func(n ast.Node) bool {
				inner, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var fn string
				switch f := inner.Fun.(type) {
				case *ast.SelectorExpr:
					fn = f.Sel.Name
				case *ast.Ident:
					fn = f.Name
				}
				switch fn {
				case "requirePermission":
					g.permissions = append(g.permissions, value(inner.Args[0]))
				case "requireAnyPermission":
					if list, ok := inner.Args[0].(*ast.CompositeLit); ok {
						for _, e := range list.Elts {
							g.permissions = append(g.permissions, value(e))
						}
					}
				case "requireRole":
					g.roles = append(g.roles, value(inner.Args[0]))
				default:
					g.wrappers = append(g.wrappers, fn)
				}
				return true
			})
			out[pattern] = g
			return true
		})
	}
	return out
}

// noCheckHere are the routes with no permission in the table, each with
// where its caller is checked instead. A new route without a permission
// fails the test until it is named here.
var noCheckHere = map[string]string{
	"GET /api/health":                               "public: status and version",
	"GET /api/server/pubkey":                        "public: the server's public key",
	"GET /api/org":                                  "data-scoped in the handler",
	"POST /api/org/login":                           "public: an organization's sign-in",
	"POST /api/auth/login":                          "public: sign-in",
	"POST /api/auth/login/2fa":                      "public: sign-in",
	"GET /api/auth/me":                              "the signed-in account itself",
	"POST /api/login":                               "public: RustDesk client sign-in",
	"GET /api/login-options":                        "public: RustDesk client sign-in",
	"GET /api/oidc/auth-query":                      "public: RustDesk client sign-in",
	"GET /api/oidc/callback":                        "public: the provider's callback",
	"POST /api/logout":                              "the client's own session",
	"GET /api/currentUser":                          "the client's own session",
	"POST /api/currentUser":                         "the client's own session",
	"GET /api/ab":                                   "the client's own address book",
	"POST /api/ab":                                  "the client's own address book",
	"GET /api/ab/personal":                          "the client's own address book",
	"POST /api/ab/personal":                         "the client's own address book",
	"GET /api/ab/tags":                              "the client's own address book",
	"GET /api/group":                                "RustDesk client: data-scoped in the handler",
	"GET /api/group/get":                            "RustDesk client: data-scoped in the handler",
	"POST /api/group/get":                           "RustDesk client: data-scoped in the handler",
	"GET /api/device-group":                         "RustDesk client: data-scoped in the handler",
	"GET /api/device-group/accessible":              "RustDesk client: data-scoped in the handler",
	"GET /api/peers/list":                           "RustDesk client: data-scoped in the handler",
	"POST /api/peers/list":                          "RustDesk client: data-scoped in the handler",
	"POST /api/heartbeat":                           "device: the device's own token",
	"POST /api/sysinfo":                             "device: the device's own token",
	"POST /api/sysinfo_ver":                         "device: the device's own token",
	"GET /api/server-key":                           "public: the server's public key",
	"GET /api/server-key/fingerprint":               "public: the server's public key",
	"GET /api/software":                             "public: client downloads",
	"GET /api/software/client-download-link":        "public: client downloads",
	"GET /api/user/group":                           "RustDesk client: the caller's own group",
	"GET /api/peer-key/{id}":                        "RustDesk client: a peer's public key, checked in the handler",
	"GET /api/users":                                "client fallback, then user.view in the handler",
	"GET /api/auth/oidc/status":                     "public: sign-in page",
	"GET /api/auth/oidc/callback":                   "public: the provider's callback",
	"GET /api/auth/oidc/session":                    "public: sign-in",
	"POST /api/auth/oidc/exchange":                  "public: sign-in",
	"GET /api/auth/sso/status":                      "public: sign-in page",
	"GET /api/panel/catalog":                        "every signed-in account reads its own navigation",
	"GET /api/mesh/share/validate":                  "public: a share link's token",
	"GET /api/guest/access-links/validate":          "public: a guest link's token",
	"GET /api/guest/access-links/peers":             "public: a guest link's token",
	"GET /ws/nd-mgmt/{device_id}":                   "device: the device's own token",
	"/":                                             "the JSON 404",
	"GET /api/org/{id}":                             "organization members (requireOrgMembership)",
	"GET /api/org/{id}/address-book":                "organization members (requireOrgMembership)",
	"GET /api/org/{id}/users":                       "organization members (requireOrgMembership)",
	"GET /api/org/{id}/invitations":                 "organization managers (requireOrgManager)",
	"GET /api/org/{id}/devices":                     "organization members (requireOrgMembership)",
	"GET /api/org/{id}/settings":                    "organization members (requireOrgMembership)",
	"GET /api/org/{id}/peer-credentials":            "organization members (requireOrgMembership)",
	"GET /api/org/{id}/policy":                      "organization members (requireOrgMembership)",
	"GET /api/org/{id}/policy/effective/{deviceId}": "organization members (requireOrgMembership)",
	"GET /api/org/{id}/policy/audit":                "organization members (requireOrgMembership)",
}

// checkedElsewhere are the wrappers that stand for a check of their own.
var checkedElsewhere = []string{
	"requirePanelAdmin", "requireConsoleCaller", "inProcessOnly", "metricsGuard",
	"rateLimitPublic", "rateLimitOutside",
}

// agentClasses are the routes that run something on a device, by what the
// path says, with the permissions that may open them. requireRole with
// admin (super admins only) is stricter than any of them.
var agentClasses = []struct {
	match       func(pattern string) bool
	what        string
	permissions []string
}{
	{func(p string) bool { return strings.HasSuffix(p, "/exec") || strings.Contains(p, "/terminal") },
		"a terminal", []string{"mesh.terminal", "cdap.terminal", "agent.terminal"}},
	{func(p string) bool { return strings.Contains(p, "/devices/{id}/files") },
		"the files", []string{"mesh.files", "cdap.files", "agent.files"}},
	{func(p string) bool { return strings.Contains(p, "/nd/mgmt/") && strings.HasSuffix(p, "/send") },
		"any message to the agent", []string{"agent.terminal"}},
	{func(p string) bool { return strings.HasSuffix(p, "/power") },
		"power actions", []string{"mesh.power"}},
	{func(p string) bool {
		for _, s := range []string{"/desktop", "/tcp", "/udp", "/video", "/audio", "/share", "/telemetry/command", "/session-grant", "/wol"} {
			if strings.HasSuffix(p, s) {
				return true
			}
		}
		return false
	}, "remote control", []string{"device.connect"}},
	{func(p string) bool { return strings.HasSuffix(p, "/cdap/devices/{id}/command") },
		"a CDAP command", []string{"cdap.command"}},
}

// accountChanges are the routes that change accounts, role permissions,
// API keys and second factors: a person only (requirePerson, L387).
func accountChange(pattern string) bool {
	method, path, _ := strings.Cut(pattern, " ")
	if method == "GET" {
		return false
	}
	return path == "/api/users" || strings.HasPrefix(path, "/api/users/{id}") && !strings.HasSuffix(path, "/organizations") ||
		strings.HasPrefix(path, "/api/role-permissions") || strings.HasPrefix(path, "/api/keys")
}

// TestEveryRouteNamesItsPermission walks the API server's route table as
// the source registers it: every route names a permission, a role or a
// check of its own, or is listed with where its caller is checked; every
// route that runs something on a device asks a permission of its kind;
// and every route that changes accounts asks for a person (L387, L388).
func TestEveryRouteNamesItsPermission(t *testing.T) {
	table := routeTable(t)
	if len(table) < 250 {
		t.Fatalf("found %d routes, the table has more: the parser misses some", len(table))
	}
	patterns := make([]string, 0, len(table))
	for p := range table {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		g := table[pattern]
		named := len(g.permissions) > 0 || len(g.roles) > 0
		for _, w := range checkedElsewhere {
			named = named || g.has(w)
		}
		if _, listed := noCheckHere[pattern]; !named && !listed {
			t.Errorf("%s names no permission and is not listed in noCheckHere", pattern)
		}
		_, path, _ := strings.Cut(pattern, " ")
		for _, class := range agentClasses {
			if !class.match(path) {
				continue
			}
			ok := len(g.permissions) > 0
			for _, p := range g.permissions {
				fits := false
				for _, want := range class.permissions {
					fits = fits || p == want
				}
				ok = ok && fits
			}
			for _, role := range g.roles {
				ok = ok || role == "admin"
			}
			if !ok {
				t.Errorf("%s runs %s on a device but asks %v %v, want one of %v", pattern, class.what, g.permissions, g.roles, class.permissions)
			}
		}
		if accountChange(pattern) && !g.has("requirePerson") {
			t.Errorf("%s changes accounts, roles or keys without requirePerson", pattern)
		}
	}
}
