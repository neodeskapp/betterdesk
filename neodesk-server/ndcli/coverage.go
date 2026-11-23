package ndcli

import "sort"

// Coverage is how the command table covers a list of console routes.
type Coverage struct {
	// Commands and Excluded count the routes a command or an exclusion
	// covers.
	Commands, Excluded int
	// Uncovered are the routes with neither, as given.
	Uncovered []string
}

// Cover works out which routes the table covers: each route ("GET
// /api/devices/:id" or "GET /api/devices/{id}") by a command or by an
// entry of the excluded list, compared by RouteKey.
func (t *Table) Cover(routes []string) Coverage {
	commands, excluded := map[string]bool{}, map[string]bool{}
	for _, c := range t.Commands {
		commands[RouteKey(c.Route)] = true
	}
	for _, e := range t.Excluded {
		excluded[RouteKey(e.Route)] = true
	}
	var cov Coverage
	seen := map[string]bool{}
	for _, r := range routes {
		k := RouteKey(r)
		if seen[k] {
			continue
		}
		seen[k] = true
		switch {
		case commands[k]:
			cov.Commands++
		case excluded[k]:
			cov.Excluded++
		default:
			cov.Uncovered = append(cov.Uncovered, r)
		}
	}
	sort.Strings(cov.Uncovered)
	return cov
}

// Stale lists the table's routes (commands and exclusions) that none of
// the routes is: a command for a route that went away.
func (t *Table) Stale(routes []string) []string {
	have := map[string]bool{}
	for _, r := range routes {
		have[RouteKey(r)] = true
	}
	var out []string
	for _, c := range t.Commands {
		if !have[RouteKey(c.Route)] {
			out = append(out, c.Name+" ("+c.Route+")")
		}
	}
	for _, e := range t.Excluded {
		if !have[RouteKey(e.Route)] {
			out = append(out, "excluded "+e.Route)
		}
	}
	sort.Strings(out)
	return out
}

// Twice lists route keys the table names more than once.
func (t *Table) Twice() []string {
	count := map[string]int{}
	for _, c := range t.Commands {
		count[RouteKey(c.Route)]++
	}
	for _, e := range t.Excluded {
		count[RouteKey(e.Route)]++
	}
	var out []string
	for k, n := range count {
		if n > 1 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
