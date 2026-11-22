package logging

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A line of Fatalf reaches the log in both formats at every LOG_LEVEL, and
// the process ends with status 1. Before, the server ended with log.Fatalf,
// whose line ("Failed to initialize keypair: ...") the level filter took
// for info and dropped at LOG_LEVEL=warn or error, so it exited without a
// word (L223).
func TestFatalfReachesTheLogAtEveryLevel(t *testing.T) {
	if child := os.Getenv("BD_LOGGING_FATAL_CHILD"); child != "" {
		format, level, _ := strings.Cut(child, "/")
		Setup(format, level)
		Fatalf("Failed to initialize keypair: %v", errors.New("bad key"))
		return
	}
	for _, format := range []string{"text", "json"} {
		for _, level := range []string{"error", "warn", "info"} {
			cmd := exec.Command(os.Args[0], "-test.run=^TestFatalfReachesTheLogAtEveryLevel$")
			cmd.Env = append(os.Environ(), "BD_LOGGING_FATAL_CHILD="+format+"/"+level)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Errorf("%s/%s: exit %v, want status 1", format, level, err)
			}
			if !strings.Contains(string(out), "Failed to initialize keypair: bad key") {
				t.Errorf("%s/%s: the fatal line is not in the log: %q", format, level, out)
			}
			if format == "json" && !strings.Contains(string(out), `"level":"fatal"`) {
				t.Errorf("json/%s: the line is not marked fatal: %q", level, out)
			}
		}
	}
}

// In JSON the level filter reads the line the server wrote, not the JSON
// made of it: an ERROR line passes at LOG_LEVEL=warn, an info line does
// not.
func TestJSONKeepsErrorsAtWarn(t *testing.T) {
	var out strings.Builder
	setupTo(&out, "json", "warn")
	t.Cleanup(func() { Setup("text", "info") })
	Logf("api", "ERROR: listen: address in use")
	Logf("api", "Starting server")
	got := out.String()
	if !strings.Contains(got, "address in use") {
		t.Errorf("the error line was dropped: %q", got)
	}
	if strings.Contains(got, "Starting server") {
		t.Errorf("the info line passed at warn: %q", got)
	}
}
