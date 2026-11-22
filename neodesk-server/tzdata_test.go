package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestBinaryCarriesTheZoneDatabase: the server binary links time/tzdata, so
// a billing time zone loads on a host without a zone database of its own
// (BUGS CM2).
func TestBinaryCarriesTheZoneDatabase(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command to list the binary's packages")
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	if !slices.Contains(strings.Fields(string(out)), "time/tzdata") {
		t.Error("the server binary does not link time/tzdata")
	}
}
