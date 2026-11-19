package console

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A port of the console that is taken stops the start with
// ErrDefaultPortTaken only when CONSOLE_PORT was not set: the server then
// runs without its console, next to an older console that still holds the
// port (DECISIONS A62). With CONSOLE_PORT set it is the ordinary error.
func TestATakenPortGivesWayOnlyWithoutConsolePort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	for _, unset := range []bool{true, false} {
		data := t.TempDir()
		cfg := &config.Config{
			Host: "127.0.0.1", Port: port, PortUnset: unset, NodeEnv: "development",
			DataDir: data, UploadsDir: filepath.Join(data, "uploads"),
			LangDir: filepath.Join("..", "..", "web-nodejs", "lang"), WebUIDir: filepath.Join(data, "no-build"),
			DefaultLanguage: "en", EnvPath: filepath.Join(data, ".env"),
			Getenv: func(key string) string { return map[string]string{"SESSION_SECRET": testSecret}[key] },
		}
		s, err := New(context.Background(), Options{Config: cfg, Store: storetest.Migrated(t), API: http.NotFoundHandler()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		err = s.Start(context.Background())
		s.Stop()
		if err == nil {
			t.Fatalf("PortUnset %v: the console started on a taken port", unset)
		}
		if errors.Is(err, ErrDefaultPortTaken) != unset {
			t.Errorf("PortUnset %v: error %v, ErrDefaultPortTaken %v", unset, err, errors.Is(err, ErrDefaultPortTaken))
		}
		if s.Addr() != "" {
			t.Errorf("PortUnset %v: still listening on %s", unset, s.Addr())
		}
	}
}
