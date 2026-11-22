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
	"github.com/unitronix/betterdesk-server/console/serverinfo"
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
// signalProof is the panel proof of the signal server in this process
// (signal.Server.PanelProof), which the web client's bridge presents.
func startConsole(ctx context.Context, cfg *config.Config, database db.Database, apiSrv *api.Server, bus *events.Bus, signalProof []byte, requestRestart func()) (func(), error) {
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
		ServerDB:    cfg.DBPath,
		SignalProof: signalProof,
		PublicRelay: cfg.SetPublicRelayServer,
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

// applySavedPublicRelay gives the signal server the relay the console's
// settings name ("Public relay server": saved in the console, else
// PUBLIC_RELAY_SERVER), before any device asks for a relay. The console
// tells it again after each save (Options.PublicRelay). A console
// configuration that does not load leaves RELAY_SERVERS.
func applySavedPublicRelay(cfg *config.Config, getenv func(string) string) {
	ccfg, err := consoleconfig.Load(getenv)
	if err != nil {
		return
	}
	info := serverinfo.New(serverinfo.Options{DataDir: ccfg.DataDir, EnvPath: ccfg.EnvPath, Getenv: getenv})
	if relay := info.ReadPublicEndpointEnv().PublicRelayServer; relay != "" {
		cfg.SetPublicRelayServer(relay)
		log.Printf("Relay for devices: %s, the public relay server of the console's settings", relay)
	}
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
