package contract

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
)

// Coverage is which console routes the suite sends a request to, and which
// it does not.
type Coverage struct {
	// Matrix and Cases count the console routes the matrix sends to and the
	// routes on the skip list that a case covers.
	Matrix, Cases int
	// Uncovered are the routes on the skip list no case covers.
	Uncovered []Skip
	// Problems are mistakes in the tables: a skip or a case that names a
	// route the route list does not have.
	Problems []string
	// Unreachable counts the console routes no request gets to; there is
	// nothing to send for them.
	Unreachable int
	// Other counts the routes of other apps, which are not checked.
	Other int
}

// CoverageOf works out the coverage of a route list by the matrix and the
// given case groups. A case covers the routes it names in Covers and the
// route its method and path match; when several routes match, the one with
// the most literal path segments, as the console would pick.
func CoverageOf(routes []Route, groups []*CaseGroup) Coverage {
	var cov Coverage
	known := map[string]bool{}
	var console []Route
	for _, route := range routes {
		known[route.ID] = true
		if route.App == ConsoleApp {
			console = append(console, route)
		} else {
			cov.Other++
		}
	}

	byCase := map[string]bool{}
	for _, group := range groups {
		for _, c := range group.Cases {
			for _, id := range c.Covers {
				if !known[id] {
					cov.Problems = append(cov.Problems, fmt.Sprintf("case %s/%s covers %q, which is not in the route list", group.Name, c.Name, id))
				}
				byCase[id] = true
			}
			if id := matchingRoute(console, c.Method, c.Path); id != "" {
				byCase[id] = true
			}
		}
	}

	skips := skipIndex()
	for _, skip := range matrixSkips {
		if !known[skip.ID] {
			cov.Problems = append(cov.Problems, fmt.Sprintf("the skip list names %q, which is not in the route list", skip.ID))
		}
	}
	for _, route := range console {
		skip, skipped := skips[route.ID]
		switch {
		case route.Unreachable || route.ShadowedBy != "":
			cov.Unreachable++
		case !skipped:
			cov.Matrix++
		case byCase[route.ID]:
			cov.Cases++
		default:
			cov.Uncovered = append(cov.Uncovered, skip)
		}
	}
	sort.Strings(cov.Problems)
	return cov
}

// matchingRoute finds the route a request goes to, or "".
func matchingRoute(routes []Route, method, requestPath string) string {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	best, bestLiterals := "", -1
	for _, route := range routes {
		if route.Method != method && route.Method != AllMethods {
			continue
		}
		if literals, ok := matchRoute(route.Path, requestPath); ok && literals > bestLiterals {
			best, bestLiterals = route.ID, literals
		}
	}
	return best
}

// CheckCoverage is the coverage test: it fails when a console route in the
// route list gets no request, neither from the matrix nor from a case, and
// lists those routes. With CONTRACT_COVERAGE=report it lists them without
// failing. It needs no console.
func CheckCoverage(t *testing.T) {
	file := strings.TrimSpace(os.Getenv("CONTRACT_ROUTES"))
	if file == "" {
		file = "testdata/routes.json"
	}
	list, err := LoadRoutes(file)
	if err != nil {
		t.Fatalf("%v", err)
	}
	cov := CoverageOf(list.Routes, Groups())
	for _, problem := range cov.Problems {
		t.Errorf("%s", problem)
	}
	t.Logf("%d console routes: %d in the matrix, %d left out of it and covered by cases, %d not covered, %d that no request reaches; %d routes of other apps are not checked",
		cov.Matrix+cov.Cases+len(cov.Uncovered)+cov.Unreachable, cov.Matrix, cov.Cases, len(cov.Uncovered), cov.Unreachable, cov.Other)
	if len(cov.Uncovered) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d routes are left out of the matrix and no case covers them:", len(cov.Uncovered))
	for _, skip := range cov.Uncovered {
		fmt.Fprintf(&b, "\n    %-58s %s", skip.ID, skip.Reason)
	}
	if strings.TrimSpace(os.Getenv("CONTRACT_COVERAGE")) == "report" {
		t.Log(b.String())
		return
	}
	t.Error(b.String() + "\n    (CONTRACT_COVERAGE=report lists them without failing)")
}
