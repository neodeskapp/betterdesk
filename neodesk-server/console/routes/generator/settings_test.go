package generator_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
)

// TestSettings reads the generator's settings with the old console's
// names, defaults and number rules.
func TestSettings(t *testing.T) {
	for _, c := range []struct {
		env      map[string]string
		embedded bool
		worker   bool
		poll     time.Duration
		cooldown time.Duration
		dir      string
	}{
		{map[string]string{}, false, true, 5 * time.Second, time.Second, filepath.Join("/data", "agent-builds")},
		{map[string]string{"BETTERDESK_CLIENT_GENERATOR_MODE": " Embedded ", "AGENT_BUILD_WORKER": "off", "AGENT_ARTIFACT_DIR": "/srv/a"}, true, false, 5 * time.Second, time.Second, "/srv/a"},
		// AGENT_BUILD_WORKER takes every on/off value in any case (BUGS L84),
		// where the old console stayed on for "OFF"
		{map[string]string{"BETTERDESK_CLIENT_GENERATOR_MODE": "embeddedx", "AGENT_BUILD_WORKER": "OFF", "AGENT_BUILD_POLL_MS": "250ms", "AGENT_BUILD_COOLDOWN_MS": "0"}, false, false, 250 * time.Millisecond, 0, filepath.Join("/data", "agent-builds")},
		{map[string]string{"AGENT_BUILD_POLL_MS": "soon", "AGENT_BUILD_COOLDOWN_MS": "later"}, false, true, time.Millisecond, 0, filepath.Join("/data", "agent-builds")},
		{map[string]string{"AGENT_BUILD_POLL_MS": "-5", "AGENT_BUILD_COOLDOWN_MS": " 30"}, false, true, time.Millisecond, 30 * time.Millisecond, filepath.Join("/data", "agent-builds")},
	} {
		c.env["DATA_DIR"] = "/data"
		cfg, err := config.Load(func(k string) string { return c.env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GeneratorEmbedded != c.embedded || cfg.AgentBuildWorker != c.worker || cfg.AgentBuildPoll != c.poll ||
			cfg.AgentBuildCooldown != c.cooldown || cfg.AgentArtifactDir != c.dir {
			t.Errorf("%v: %v %v %v %v %q", c.env, cfg.GeneratorEmbedded, cfg.AgentBuildWorker, cfg.AgentBuildPoll, cfg.AgentBuildCooldown, cfg.AgentArtifactDir)
		}
	}
}
