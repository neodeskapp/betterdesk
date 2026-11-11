package addressparser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// jsEntry is one object of a result as the Node module returns it: a mailbox
// has "address" and "name", a group has "name" and "group".
type jsEntry struct {
	Address *string    `json:"address"`
	Name    *string    `json:"name"`
	Group   *[]jsEntry `json:"group"`
}

// vector is one input with the list the Node module returns for it, and the
// flattened list where that differs.
type vector struct {
	In   string     `json:"in"`
	Out  []jsEntry  `json:"out"`
	Flat *[]jsEntry `json:"flat"`
}

// loadVectors reads a file the Node oracle wrote.
func loadVectors(t testing.TB, file string) []vector {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var vectors []vector
	if err := decoder.Decode(&vectors); err != nil {
		t.Fatalf("decode %s: %v", file, err)
	}
	return vectors
}

// toAddresses converts a list from the Node module, and fails when an object
// has another set of keys than a mailbox or a group has.
func toAddresses(list []jsEntry) ([]Address, error) {
	addresses := make([]Address, 0, len(list))
	for _, entry := range list {
		switch {
		case entry.Name == nil:
			return nil, fmt.Errorf("entry without a name")
		case entry.Group != nil && entry.Address != nil:
			return nil, fmt.Errorf("entry %q with both an address and a group", *entry.Name)
		case entry.Group != nil:
			members, err := toAddresses(*entry.Group)
			if err != nil {
				return nil, err
			}
			addresses = append(addresses, Address{Name: *entry.Name, IsGroup: true, Group: members})
		case entry.Address != nil:
			addresses = append(addresses, Address{Name: *entry.Name, Address: *entry.Address})
		default:
			return nil, fmt.Errorf("entry %q with neither an address nor a group", *entry.Name)
		}
	}
	return addresses, nil
}

// replay compares Parse and Flatten with every vector of a file and returns
// how many it compared.
func replay(t *testing.T, file string) int {
	vectors := loadVectors(t, file)
	failed := 0
	for i, v := range vectors {
		want, err := toAddresses(v.Out)
		if err != nil {
			t.Fatalf("vector %d (%q): %v", i, v.In, err)
		}
		wantFlat := want
		if v.Flat != nil {
			if wantFlat, err = toAddresses(*v.Flat); err != nil {
				t.Fatalf("vector %d (%q) flat: %v", i, v.In, err)
			}
		}
		got := Parse(v.In)
		gotFlat := Flatten(got)
		if !reflect.DeepEqual(got, want) {
			failed++
			t.Errorf("vector %d: Parse(%q)\n got  %s\n want %s", i, v.In, show(got), show(want))
		} else if !reflect.DeepEqual(gotFlat, wantFlat) {
			failed++
			t.Errorf("vector %d: Flatten(Parse(%q))\n got  %s\n want %s", i, v.In, show(gotFlat), show(wantFlat))
		}
		if failed >= 25 {
			t.Fatalf("stopping after %d failures", failed)
		}
	}
	return len(vectors)
}

// show prints a list with every string quoted, so control characters and
// whitespace are visible.
func show(list []Address) string {
	if list == nil {
		return "nil"
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for i, entry := range list {
		if i > 0 {
			out.WriteByte(' ')
		}
		if entry.IsGroup {
			fmt.Fprintf(&out, "{group %q %s}", entry.Name, show(entry.Group))
		} else {
			fmt.Fprintf(&out, "{%q %q}", entry.Name, entry.Address)
		}
	}
	out.WriteByte(']')
	return out.String()
}

// TestNodeVectors replays what nodemailer's address parser returned for the
// inputs of testdata/nodeoracle/addressparser.js.
func TestNodeVectors(t *testing.T) {
	count := replay(t, "testdata/addressparser.json")
	if count < 20000 {
		t.Fatalf("only %d vectors in testdata/addressparser.json, the file is incomplete", count)
	}
	t.Logf("%d vectors", count)
}

// TestExtraNodeVectors replays a second file of vectors, named by the
// environment variable ADDRESSPARSER_VECTORS. It is how inputs the fuzzer
// found are checked against the Node module (see testdata/nodeoracle/README.md).
func TestExtraNodeVectors(t *testing.T) {
	file := os.Getenv("ADDRESSPARSER_VECTORS")
	if file == "" {
		t.Skip("ADDRESSPARSER_VECTORS is not set")
	}
	t.Logf("%d vectors", replay(t, file))
}
