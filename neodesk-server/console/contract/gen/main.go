// Command gen writes the contract suite's route list (testdata/routes.json)
// from the route inventory of the Node console.
//
//	go run ./console/contract/gen -inventory /notes/inventory.json -out console/contract/testdata/routes.json
//
// The inventory is made outside the repository. The route list keeps what
// the suite needs of each route and sorts the routes, so the file changes
// only where the routes change.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// inventory is the part of the inventory file this program reads.
type inventory struct {
	Routes []struct {
		ID         string  `json:"id"`
		Method     string  `json:"method"`
		Path       string  `json:"path"`
		App        string  `json:"app"`
		Group      string  `json:"group"`
		CSRF       string  `json:"csrf"`
		File       string  `json:"file"`
		ShadowedBy *string `json:"shadowedBy"`
		Reachable  *bool   `json:"reachable"`
	} `json:"routes"`
}

func main() {
	in := flag.String("inventory", "", "the inventory file (inventory.json)")
	out := flag.String("out", "testdata/routes.json", "the route list to write")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "gen: -inventory is required")
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return fmt.Errorf("read the inventory: %w", err)
	}
	var inv inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return fmt.Errorf("read the inventory %s: %w", in, err)
	}
	if len(inv.Routes) == 0 {
		return fmt.Errorf("the inventory %s has no routes", in)
	}

	list := contract.RouteList{Source: filepath.Base(in)}
	seen := map[string]bool{}
	for _, r := range inv.Routes {
		if r.ID == "" || r.Method == "" || r.Path == "" {
			return fmt.Errorf("an inventory route has no id, method or path: %+v", r)
		}
		if seen[r.ID] {
			return fmt.Errorf("the inventory lists %q twice", r.ID)
		}
		seen[r.ID] = true
		route := contract.Route{ID: r.ID, Method: r.Method, Path: r.Path, App: r.App, Group: r.Group, CSRF: r.CSRF, File: r.File}
		if r.ShadowedBy != nil {
			route.ShadowedBy = *r.ShadowedBy
		}
		route.Unreachable = r.Reachable != nil && !*r.Reachable
		list.Routes = append(list.Routes, route)
	}
	sort.SliceStable(list.Routes, func(i, j int) bool {
		a, b := list.Routes[i], list.Routes[j]
		if a.App != b.App {
			return a.App < b.App
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Method != b.Method {
			return methodOrder(a.Method) < methodOrder(b.Method)
		}
		return a.ID < b.ID
	})

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	buf.WriteString("{\n \"source\": ")
	if err := enc.Encode(list.Source); err != nil {
		return err
	}
	buf.Truncate(buf.Len() - 1)
	buf.WriteString(",\n \"routes\": [\n")
	for i, route := range list.Routes {
		buf.WriteString("  ")
		if err := enc.Encode(route); err != nil {
			return err
		}
		buf.Truncate(buf.Len() - 1)
		if i < len(list.Routes)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString(" ]\n}\n")

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("create the folder of %s: %w", out, err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write the route list: %w", err)
	}
	fmt.Printf("wrote %d routes to %s\n", len(list.Routes), out)
	return nil
}

// methodOrder sorts the methods of one path: reads before writes.
func methodOrder(method string) int {
	for i, m := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		if m == method {
			return i
		}
	}
	return 99
}
