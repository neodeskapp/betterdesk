package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"

	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/updater"
)

// subcommand is a command of betterdesk-server besides running the
// server. run takes the arguments after the command's name and returns
// the exit status.
type subcommand struct {
	usage string
	run   func(args []string, stdin io.Reader, stdout, stderr io.Writer) int
}

// subcommands are the commands a first argument that does not start with
// "-" names (spec updater-installers.md 4.9). Add a command here.
var subcommands = map[string]subcommand{
	"update": {
		usage: "update [--check] [--yes] [--no-backup]   install the newest release of the update channel",
		run:   runUpdate,
	},
	"rollback": {
		usage: "rollback                                 put the binary before the last update back",
		run:   runRollback,
	},
}

// runSubcommand runs the command args[0] names. handled is false when
// args start with a flag or are empty: then the server runs.
func runSubcommand(args []string) (code int, handled bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return 0, false
	}
	cmd, ok := subcommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "betterdesk-server: unknown command %q\n\nCommands:\n", args[0])
		names := make([]string, 0, len(subcommands))
		for name := range subcommands {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(os.Stderr, "  betterdesk-server %s\n", subcommands[name].usage)
		}
		return 2, true
	}
	return cmd.run(args[1:], os.Stdin, os.Stdout, os.Stderr), true
}

// updaterForCommand builds the updater for a command from the console's
// configuration, the same environment the service reads. A command makes
// no data backup: that needs the database, which it does not open.
func updaterForCommand(stderr io.Writer) (*updater.Service, bool) {
	cfg, err := consoleconfig.Load(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "betterdesk-server: configuration: %v\n", err)
		return nil, false
	}
	return updater.New(updater.Options{
		DataDir:    cfg.DataDir,
		ConsoleDir: cfg.ConsoleDir,
		EnvFile:    os.Getenv("BETTERDESK_ENV_FILE"),
		Version:    Version,
		Commit:     Commit,
		Docker:     cfg.Docker,
		Getenv:     os.Getenv,
	}), true
}

func runUpdate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	svc, ok := updaterForCommand(stderr)
	if !ok {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return updater.CLI{Service: svc, In: stdin, Out: stdout}.Update(ctx, args)
}

func runRollback(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	svc, ok := updaterForCommand(stderr)
	if !ok {
		return 1
	}
	return updater.CLI{Service: svc, In: stdin, Out: stdout}.Rollback(args)
}
