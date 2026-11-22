package serverinfo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// Requests run side by side. Saves of the public endpoints must not lose
// one another's lines or leave half a file, and the reads beside them must
// see a whole file. Run with -race.
func TestConcurrentUse(t *testing.T) {
	root := t.TempDir()
	dataDir, envPath := filepath.Join(root, "data"), filepath.Join(root, ".env")
	unit := filepath.Join(root, "unit")
	if err := os.WriteFile(envPath, []byte("# console\nPORT=5000\nPUBLIC_SERVER_ID=start.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(sampleSystemd), 0o644); err != nil {
		t.Fatal(err)
	}
	api := &stubAPI{}
	key, _ := json.Marshal(map[string]string{"key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	api.set("GET", &apiAnswer{Data: key})
	s := New(Options{
		DataDir: dataDir, EnvPath: envPath, PubKeyPath: filepath.Join(root, "none.pub"), APIURL: "http://127.0.0.1:21114/api",
		SystemdUnit: unit, DockerEnvFile: filepath.Join(root, "none"), GoAPI: api.client(), Getenv: envMap(nil), Logger: &recordingLogger{},
	})

	ctx := context.Background()
	const workers, rounds = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				// Every other save clears the API address. A cleared setting
				// is read from the files, so a read in the middle of a save
				// would pair one save's host with another's address.
				host := "w" + strconv.Itoa(w) + "-" + strconv.Itoa(i) + ".example"
				api := "https://" + host
				if i%2 == 1 {
					api = ""
				}
				if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: host, PublicRelayServer: host, PublicAPIURL: api}); err != nil {
					t.Errorf("save: %v", err)
				}
				for range 20 {
					got := s.ReadPublicEndpointEnv()
					whole := got.PublicAPIURL == "" || got.PublicAPIURL == "https://"+got.PublicServerID
					if got.PublicServerID == "" || got.PublicServerID != got.PublicRelayServer || !whole {
						t.Errorf("read a mixed state: %+v", got)
					}
				}
				if s.ResolvePublicKey(ctx) == "" {
					t.Error("no key")
				}
				s.ResolveRustDeskEndpoints(&RequestHost{Host: "console.example.com"}, "")
				if _, err := s.SetConnectionMode(ctx, ConnectionInput{Mode: "relay_only", P2PFallbackMs: float64(1000 + w)}); err != nil {
					t.Errorf("set connection mode: %v", err)
				}
				if settings, err := s.GetConnectionMode(ctx); err != nil || settings.Mode != ModeRelayOnly {
					t.Errorf("connection mode %+v, %v", settings, err)
				}
			}
		}(w)
	}
	wg.Wait()

	for _, path := range []string{filepath.Join(dataDir, DurableBasename), envPath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		values := ParseEnvFile(string(raw))
		id, api := values[EnvPublicServerID], values[EnvPublicAPIURL]
		if id == "" || values[EnvPublicRelayServer] != id || (api != "" && api != "https://"+id) {
			t.Errorf("%s holds a mixed state: %q", filepath.Base(path), raw)
		}
	}
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if values := ParseEnvFile(string(env)); values["PORT"] != "5000" {
		t.Errorf("the console's env file lost a line: %q", env)
	}
	if names := listDir(dataDir); len(names) != 1 {
		t.Errorf("data folder holds %q, want only the settings file", names)
	}
}
