package main

import (
	"context"
	"fmt"
	"os"

	"github.com/unitronix/betterdesk-server/console"
	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
)

// startConsole starts the web console on its own port when CONSOLE_PORT is
// set. The returned function stops it.
func startConsole(ctx context.Context) (func(), error) {
	cfg, err := consoleconfig.Load(os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("console configuration: %w", err)
	}
	if cfg.Port == 0 {
		return func() {}, nil
	}
	srv := console.New(cfg)
	if err := srv.Start(ctx); err != nil {
		return nil, err
	}
	return srv.Stop, nil
}
