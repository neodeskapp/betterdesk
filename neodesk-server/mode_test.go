package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestParseFlagsHelper is the start of the server's configuration in a
// child process of TestSplitModesEndTheStart; it does nothing in a normal
// run.
func TestParseFlagsHelper(t *testing.T) {
	if os.Getenv("BDGO_PARSE_FLAGS_HELPER") != "1" {
		t.Skip("runs only as the child of TestSplitModesEndTheStart")
	}
	cfg := parseFlags()
	fmt.Printf("configuration done, mode %s\n", cfg.Mode)
	os.Exit(0)
}

// MODE=signal and MODE=relay end the start with a message that names the
// combined mode; MODE=all goes on.
func TestSplitModesEndTheStart(t *testing.T) {
	run := func(mode string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestParseFlagsHelper$")
		cmd.Env = append(os.Environ(), "BDGO_PARSE_FLAGS_HELPER=1", "MODE="+mode)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, mode := range []string{"signal", "relay", "RELAY"} {
		out, err := run(mode)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() == 0 {
			t.Fatalf("MODE=%s: the start went on (%v): %s", mode, err, out)
		}
		lower := strings.ToLower(mode)
		if strings.Contains(out, "configuration done") ||
			!strings.Contains(out, "MODE="+lower+" (-mode "+lower+") is not supported") ||
			!strings.Contains(out, "Use MODE=all (-mode all), the combined mode") {
			t.Fatalf("MODE=%s: output %q does not refuse the mode and name the combined one", mode, out)
		}
	}
	out, err := run("all")
	if err != nil || !strings.Contains(out, "configuration done, mode all") {
		t.Fatalf("MODE=all: %v: %s", err, out)
	}
}
