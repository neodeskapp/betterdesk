package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/unitronix/betterdesk-server/api"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/console"
	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/events"
)

// startConsole starts the web console on its own port: CONSOLE_PORT, else
// 5000, and not at all with CONSOLE_PORT=0. The console works on the
// server's database: PostgreSQL through the server's pool, SQLite through
// its own connections to the same file. The returned function stops the
// console and everything it started. requestRestart ends the process to
// be started again; the console calls it only under a supervisor.
func startConsole(ctx context.Context, cfg *config.Config, database db.Database, apiSrv *api.Server, bus *events.Bus, requestRestart func()) (func(), error) {
	ccfg, err := consoleconfig.LoadEnv()
	if err != nil {
		return nil, fmt.Errorf("console configuration: %w", err)
	}
	if ccfg.Port == 0 {
		return func() {}, nil
	}
	if os.Getenv("BETTERDESK_API_URL") == "" && os.Getenv("HBBS_API_URL") == "" {
		ccfg.APIHost = net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.APIPort))
	}

	sdb, err := openConsoleStore(cfg, database)
	if err != nil {
		return nil, err
	}
	if err := sdb.Migrate(ctx); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("console schema: %w", err)
	}
	srv, err := console.New(ctx, console.Options{
		Config:      ccfg,
		Store:       sdb,
		API:         apiSrv.InternalHandler(),
		APIListener: apiSrv.ListenerHandler(),
		Events:      bus,
		Roles:       database,
		Version:     Version,
		Commit:      Commit,
		Exit:        requestRestart,
	})
	if err != nil {
		_ = sdb.Close()
		return nil, err
	}
	if err := srv.Start(ctx); err != nil {
		if errors.Is(err, console.ErrDefaultPortTaken) {
			// An older console may hold the port (DECISIONS A62): signal,
			// relay and API keep running
			srv.Stop()
			_ = sdb.Close()
			log.Printf("ERROR: %v", err)
			log.Printf("ERROR: the server runs without its console. Set CONSOLE_PORT to a free port, or CONSOLE_PORT=0 to turn the console off")
			return func() {}, nil
		}
		_ = sdb.Close()
		return nil, err
	}
	return func() {
		srv.Stop()
		_ = sdb.Close()
	}, nil
}

// openConsoleStore opens the console's access to the server's database.
func openConsoleStore(cfg *config.Config, database db.Database) (*store.DB, error) {
	if pg, ok := database.(*db.PostgresDB); ok {
		return store.OpenPostgres(pg.Pool()), nil
	}
	sdb, err := store.OpenSQLite(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("console database: %w", err)
	}
	return sdb, nil
}
