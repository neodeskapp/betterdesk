package console

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/ndcli"
)

// liveRoutes are the routes of the console's two route tables, as
// "METHOD /pattern" ("ALL" for a route of every method).
func liveRoutes(w *world) []string {
	var out []string
	for _, table := range []*kit.Router{w.s.Early(), w.s.Router()} {
		for _, r := range table.Routes() {
			if strings.HasPrefix(r.Path, "/__test/") {
				continue
			}
			method := r.Method
			if method == "" {
				method = "ALL"
			}
			out = append(out, method+" "+r.Path)
		}
	}
	return out
}

// Every route the console serves, the ones that came with the Go console
// included (the contract suite's list lacks some), has a command of the
// neodesk command line or is on its excluded list with a reason; and
// every route the table names is one the console serves or the contract
// list holds. A new route with neither fails here: add a command to
// ndcli/commands.json, or the route to its "excluded" list.
func TestEveryLiveRouteHasACommandOrAReason(t *testing.T) {
	w := newWorld(t, setup{})
	tab, err := ndcli.Load()
	if err != nil {
		t.Fatal(err)
	}
	live := liveRoutes(w)
	if len(live) < 700 {
		t.Fatalf("only %d live routes", len(live))
	}
	cov := tab.Cover(live)
	t.Logf("%d live console routes: %d by a command, %d excluded with a reason, %d uncovered", len(live), cov.Commands, cov.Excluded, len(cov.Uncovered))
	if len(cov.Uncovered) > 0 {
		t.Errorf("%d routes have neither a command nor a reason to leave them out:\n  %s\nAdd a command to ndcli/commands.json, or the route to its \"excluded\" list with the reason.",
			len(cov.Uncovered), strings.Join(cov.Uncovered, "\n  "))
	}
	// Routes of the table that the console does not serve: only the
	// contract list's routes that are gone may be there, as exclusions
	for _, stale := range tab.Stale(live) {
		if !strings.HasPrefix(stale, "excluded ") {
			t.Errorf("a command for a route the console does not serve: %s", stale)
		}
	}

	// A route added without a command is found
	w.s.Router().Post("/api/zz-new-route/{id}", probe)
	if cov := tab.Cover(liveRoutes(w)); len(cov.Uncovered) != 1 || cov.Uncovered[0] != "POST /api/zz-new-route/{id}" {
		t.Errorf("a new route: %v", cov.Uncovered)
	}
}

// Every console route of the contract suite's list is one the console
// serves: a route that went away leaves the list with its goldens, so no
// check or command stands for an address that answers 404 (L431). The
// web console's pages and files are the exception: the web host answers
// them outside the route tables.
func TestTheContractListHoldsOnlyServedRoutes(t *testing.T) {
	w := newWorld(t, setup{})
	data, err := os.ReadFile(filepath.Join("contract", "testdata", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Routes []struct {
			Method, Path, App, Group, ShadowedBy string
			Unreachable                          bool
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	for _, r := range liveRoutes(w) {
		live[ndcli.RouteKey(r)] = true
	}
	for _, r := range list.Routes {
		if r.App != "console" || r.Unreachable || r.ShadowedBy != "" || r.Group == "web-ui" {
			continue
		}
		if route := r.Method + " " + r.Path; !live[ndcli.RouteKey(route)] && !live[ndcli.RouteKey("ALL "+r.Path)] {
			t.Errorf("the contract list holds %s, which the console does not serve", route)
		}
	}
}

// permissionGaps lists the commands whose route's guards ask for a
// permission the command's permission text does not name. routes are the
// live routes; where several fit a command's route (the RustDesk client's
// GET /api/devices passes the console's requests on to the console's),
// every one's guards count.
func permissionGaps(tab *ndcli.Table, routes []kit.Route) []string {
	needs := map[string][]string{}
	for _, r := range routes {
		method := r.Method
		if method == "" {
			method = "ALL"
		}
		key := ndcli.RouteKey(method + " " + r.Path)
		for _, need := range r.Permissions {
			if !slices.Contains(needs[key], need) {
				needs[key] = append(needs[key], need)
			}
		}
	}
	names := func(text, permission string) bool {
		for _, word := range strings.FieldsFunc(text, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.')
		}) {
			if strings.TrimSuffix(word, ".") == permission {
				return true
			}
		}
		return false
	}
	var gaps []string
	for _, c := range tab.Commands {
		_, path, _ := strings.Cut(c.Route, " ")
		for _, need := range append(needs[ndcli.RouteKey(c.Route)], needs[ndcli.RouteKey("ALL "+path)]...) {
			for _, permission := range strings.Split(need, " or ") {
				if !names(c.Permission, permission) {
					gaps = append(gaps, fmt.Sprintf("%s (%s) needs %s; the table says %q", c.Name, c.Route, need, c.Permission))
					break
				}
			}
		}
	}
	sort.Strings(gaps)
	return gaps
}

// Every command names the permission its route's guards ask for, so its
// help, the reference and `neodesk describe` say what an account and a
// token need: a command whose route needs a permission can't be in the
// table without it (L447: devices list said a signed-in account was
// enough).
func TestEveryCommandNamesThePermissionItsRouteNeeds(t *testing.T) {
	w := newWorld(t, setup{})
	tab, err := ndcli.Load()
	if err != nil {
		t.Fatal(err)
	}
	var routes []kit.Route
	for _, table := range []*kit.Router{w.s.Early(), w.s.Router()} {
		routes = append(routes, table.Routes()...)
	}
	guarded := 0
	for _, r := range routes {
		if len(r.Permissions) > 0 {
			guarded++
		}
	}
	if guarded < 300 {
		t.Fatalf("only %d routes name a permission: the route table no longer records its guards", guarded)
	}
	if gaps := permissionGaps(tab, routes); len(gaps) > 0 {
		t.Errorf("%d commands don't name the permission their route needs (ndcli/commands.json \"permission\"):\n  %s", len(gaps), strings.Join(gaps, "\n  "))
	}

	// A guarded route whose command names no permission, or another one,
	// is found; one that names it, alone or among others, is not
	d := w.s.Deps()
	w.s.Router().Get("/api/zz-guarded", probe)
	w.s.Router().Get("/api/zz-guarded", probe, d.RequireAuth, d.RequirePermission("audit.view"))
	w.s.Router().Post("/api/zz-either", probe, d.RequireAuth, d.RequireAnyPermission("device.edit", "device.view"))
	probes := &ndcli.Table{Commands: []*ndcli.Command{
		{Name: "zz none", Route: "GET /api/zz-guarded"},
		{Name: "zz other", Route: "GET /api/zz-guarded", Permission: "audit.export"},
		{Name: "zz named", Route: "GET /api/zz-guarded", Permission: "audit.view"},
		{Name: "zz half", Route: "POST /api/zz-either", Permission: "device.edit"},
		{Name: "zz both", Route: "POST /api/zz-either", Permission: "device.edit or device.view"},
	}}
	gaps := permissionGaps(probes, w.s.Router().Routes())
	want := []string{
		`zz half (POST /api/zz-either) needs device.edit or device.view; the table says "device.edit"`,
		`zz none (GET /api/zz-guarded) needs audit.view; the table says ""`,
		`zz other (GET /api/zz-guarded) needs audit.view; the table says "audit.export"`,
	}
	if strings.Join(gaps, "\n") != strings.Join(want, "\n") {
		t.Errorf("the probes:\n  %s\nwant\n  %s", strings.Join(gaps, "\n  "), strings.Join(want, "\n  "))
	}
}
