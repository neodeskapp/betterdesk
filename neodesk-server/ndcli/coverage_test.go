package ndcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contractRoutes are the console's routes in the contract suite's list
// (console/contract/testdata/routes.json): the ones a request reaches.
func contractRoutes(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "console", "contract", "testdata", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Routes []struct {
			ID, Method, Path, App, ShadowedBy string
			Unreachable                       bool
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range list.Routes {
		if r.App == "console" && !r.Unreachable && r.ShadowedBy == "" {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	if len(out) < 700 {
		t.Fatalf("only %d routes in the contract list", len(out))
	}
	return out
}

// Every console route of the contract suite's list has a command or is
// on the excluded list with its reason. A new route with neither fails
// here: add a command to commands.json, or the route with its reason to
// "excluded". console/cli_coverage_test.go checks the console's live
// route table the same way.
func TestEveryContractRouteHasACommandOrAReason(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	routes := contractRoutes(t)
	cov := tab.Cover(routes)
	t.Logf("%d console routes in the contract list: %d by a command, %d excluded with a reason, %d uncovered",
		len(routes), cov.Commands, cov.Excluded, len(cov.Uncovered))
	if len(cov.Uncovered) > 0 {
		t.Errorf("%d routes have neither a command nor a reason to leave them out:\n  %s\nAdd a command to ndcli/commands.json, or the route to its \"excluded\" list with the reason.",
			len(cov.Uncovered), strings.Join(cov.Uncovered, "\n  "))
	}
	if twice := tab.Twice(); len(twice) > 0 {
		t.Errorf("routes named more than once: %v", twice)
	}
	for _, e := range tab.Excluded {
		if len(strings.TrimSpace(e.Reason)) < 20 {
			t.Errorf("%s: the reason %q says too little", e.Route, e.Reason)
		}
	}
}

// The check sees a route that nothing covers.
func TestCoverageNamesANewRoute(t *testing.T) {
	tab, _ := Load()
	cov := tab.Cover([]string{"GET /api/devices", "POST /api/brand-new/:id(\\d+)", "GET /api/auth/oidc/callback"})
	if cov.Commands != 1 || cov.Excluded != 1 || len(cov.Uncovered) != 1 || cov.Uncovered[0] != "POST /api/brand-new/:id(\\d+)" {
		t.Errorf("coverage %+v", cov)
	}
	if k := RouteKey("GET /api/automation/rules/:id(\\d+)"); k != RouteKey("GET /api/automation/rules/{id}") || k != "GET /api/automation/rules/{}" {
		t.Errorf("RouteKey: %q", k)
	}
	if RouteKey("GET /*") != RouteKey("GET /{rest...}") {
		t.Error("wildcards differ")
	}
}
