package config

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// L85: PORT no longer moves the ID server; a start that has PORT and no
// SIGNAL_PORT says which variable it reads.
func TestLoadEnv_PortDoesNotMoveTheIDServer(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	t.Setenv("PORT", "5000")
	cfg := DefaultConfig()
	cfg.SignalPort = 21200 // the -port flag
	cfg.LoadEnv()
	if cfg.SignalPort != 21200 {
		t.Fatalf("SignalPort = %d with PORT=5000, want the flag's 21200", cfg.SignalPort)
	}
	if !strings.Contains(logs.String(), "SIGNAL_PORT") || !strings.Contains(logs.String(), "PORT=5000") {
		t.Errorf("log %q does not say that SIGNAL_PORT is read instead of PORT", logs.String())
	}

	logs.Reset()
	t.Setenv("SIGNAL_PORT", "21300")
	cfg = DefaultConfig()
	cfg.LoadEnv()
	if cfg.SignalPort != 21300 || strings.Contains(logs.String(), "PORT=5000") {
		t.Errorf("with SIGNAL_PORT: port %d, log %q; want 21300 and no word about PORT", cfg.SignalPort, logs.String())
	}
}
