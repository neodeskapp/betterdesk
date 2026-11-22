package cli

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readVectors reads testdata/<name>.json.gz, a recording of the old Node
// console's scripts (testdata/README.md).
func readVectors(t testing.TB, name string, into any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	unpacked, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(unpacked).Decode(into); err != nil {
		t.Fatal(err)
	}
}
