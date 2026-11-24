package console

import (
	"encoding/json"
	"os"
	"path/filepath"
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
