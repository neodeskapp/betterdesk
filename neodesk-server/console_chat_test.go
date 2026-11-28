package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/signal"
)

// TestAServerWithoutConsoleHasNoChat: while the console is expected, a
// computer's chat at the API port answers 503 to ask again soon; a server
// whose console is off (CONSOLE_PORT=0) answers 404, so the computer
// knows it has no chat (L536).
func TestAServerWithoutConsoleHasNoChat(t *testing.T) {
	t.Setenv("CONSOLE_PORT", "0")
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	apiSrv := api.New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	ask := func() int {
		rec := httptest.NewRecorder()
		apiSrv.ListenerHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/ws/chat/900100001?v=2", nil))
		return rec.Code
	}
	if status := ask(); status != http.StatusServiceUnavailable {
		t.Errorf("before the console starts: %d, want 503", status)
	}
	stop, err := startConsole(context.Background(), config.DefaultConfig(), database, apiSrv, nil, nil,
		func(signal.ControllerFeatures) {}, func(http.Handler) {}, func() {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if status := ask(); status != http.StatusNotFound {
		t.Errorf("with CONSOLE_PORT=0: %d, want 404", status)
	}
}
