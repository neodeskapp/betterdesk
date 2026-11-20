package meshcentral

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/events"
	"github.com/unitronix/betterdesk-server/peer"
)

// The agent-server certificate of a bare MESH_AGENT_CERT_FILE (the default
// mesh_agent_server.pem) lives next to the server's key file, which every
// installer keeps in KEYS_PATH, where the backup reads it: not in the
// folder the server happens to run in (BN11).
func TestAgentCertNextToTheKeyFile(t *testing.T) {
	keys, work := t.TempDir(), t.TempDir()
	t.Chdir(work)
	cfg := config.DefaultConfig()
	cfg.KeyFile = filepath.Join(keys, "id_ed25519")
	cfg.MeshAssetsDir = filepath.Join(work, "assets-empty")
	if cfg.MeshAgentCertFile != "mesh_agent_server.pem" {
		t.Fatalf("the default certificate file is %q", cfg.MeshAgentCertFile)
	}
	gw, err := NewGateway(cfg, nil, peer.NewMap(), events.NewBus(), "mesh-agent-cert-test-secret-32b")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(keys, "mesh_agent_server.pem")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no certificate next to the key file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "mesh_agent_server.pem")); err == nil {
		t.Error("a certificate was written into the working folder")
	}
	if path, present, _ := gw.AgentCertInfo(); path != want || !present {
		t.Errorf("AgentCertInfo: %q, present %v; want %q", path, present, want)
	}
	// The next start loads the same certificate
	again, err := NewGateway(cfg, nil, peer.NewMap(), events.NewBus(), "mesh-agent-cert-test-secret-32b")
	if err != nil {
		t.Fatal(err)
	}
	if again.ServerID() != gw.ServerID() {
		t.Errorf("another server id after a restart: %s, then %s", gw.ServerID(), again.ServerID())
	}

	// A path with a folder is taken as it is
	named := filepath.Join(t.TempDir(), "agent.pem")
	cfg.MeshAgentCertFile = named
	if _, err := NewGateway(cfg, nil, peer.NewMap(), events.NewBus(), "mesh-agent-cert-test-secret-32b"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(named); err != nil {
		t.Errorf("MESH_AGENT_CERT_FILE with a folder: %v", err)
	}
	// A bare key file name keeps the working folder
	cfg.MeshAgentCertFile, cfg.KeyFile = "mesh_agent_server.pem", "id_ed25519"
	if path := agentCertPath(cfg); path != "mesh_agent_server.pem" {
		t.Errorf("with a bare key file: %q", path)
	}
}
