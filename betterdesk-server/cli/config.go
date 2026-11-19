package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/managedconfig"
	"github.com/unitronix/betterdesk-server/console/updater"
)

const configUsage = `Usage:
  betterdesk-server config check
  betterdesk-server config set KEY=VALUE [KEY=VALUE ...]`

// Config is "config check" and "config set", what management-cli.js did
// with "check" and "set".
//
// check prints the report of what this install can do from the panel,
// the one the management routes answer (updater.Capabilities), as JSON,
// and exits with 0 when every group is ready and with 1 otherwise.
//
// set checks KEY=VALUE pairs against the allowlist (the keys of the
// built-in .env.example) and the rules for paths, ports and switches,
// writes them into the env file with a backup (managedconfig.Apply) and
// prints what it did as JSON. A refused change prints its reason on
// standard error and exits with 1.
//
// The env file is BETTERDESK_ENV_FILE, else .env in BETTERDESK_CONSOLE_PATH,
// else in the console's folder. No action, -h and --help print the usage
// and exit with 0; another action exits with 2.
func Config(args []string, env Env) int {
	action := ""
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "", "-h", "--help":
		fmt.Fprintln(env.Stdout, configUsage)
		return 0
	case "check", "set":
	default:
		fmt.Fprintln(env.Stdout, configUsage)
		return 2
	}
	cfg, err := consoleconfig.Load(env.Getenv)
	if err != nil {
		fmt.Fprintln(env.Stderr, err)
		return 1
	}
	envFile := env.Getenv("BETTERDESK_ENV_FILE")
	if envFile == "" {
		envFile = filepath.Join(orElse(env.Getenv("BETTERDESK_CONSOLE_PATH"), cfg.ConsoleDir), ".env")
	}
	if action == "check" {
		svc := updater.New(updater.Options{
			DataDir: cfg.DataDir, ConsoleDir: cfg.ConsoleDir, EnvFile: envFile,
			Docker: cfg.Docker, Getenv: env.Getenv, GOOS: env.GOOS,
		})
		return configCheck(env, svc.Capabilities())
	}
	managed := managedconfig.New(managedconfig.Options{
		ConsoleDir: cfg.ConsoleDir, EnvFile: envFile, Getenv: env.Getenv, GOOS: env.GOOS, Now: env.Now,
	})
	return configSet(args[1:], env, managed)
}

func configCheck(env Env, report updater.Capabilities) int {
	text, err := jsonText(report, true)
	if err != nil {
		fmt.Fprintln(env.Stderr, err)
		return 1
	}
	fmt.Fprintln(env.Stdout, text)
	g := report.Groups
	for _, group := range []updater.Group{g.Update, g.Config, g.Restart, g.Permissions, g.Backup, g.Health} {
		if !group.Ready {
			return 1
		}
	}
	return 0
}

func configSet(args []string, env Env, managed *managedconfig.Config) int {
	fail := func(err error) int {
		fmt.Fprintln(env.Stderr, err.Error())
		return 1
	}
	// parseChanges: changes[key] = value, in a JavaScript object
	changes := newObject[string]()
	for _, item := range args {
		eq := strings.IndexByte(item, '=')
		if eq <= 0 {
			return fail(fmt.Errorf("Expected KEY=VALUE, got: %s", item))
		}
		changes.set(item[:eq], item[eq+1:])
	}
	var list []managedconfig.Change
	for _, key := range changes.entries() {
		list = append(list, managedconfig.Change{Key: key, Value: changes.values[key]})
	}
	settings, err := managed.Validate(list, true)
	if err != nil {
		return fail(err)
	}
	applied, err := managed.Apply(settings)
	if err != nil {
		var invalid *managedconfig.ValidationError
		if errors.As(err, &invalid) {
			return fail(invalid)
		}
		return fail(err)
	}
	values := orderedJSON{values: map[string]any{}}
	for _, s := range applied.Values {
		values.keys = append(values.keys, s.Key)
		values.values[s.Key] = s.Value
	}
	changed := applied.Changed
	if changed == nil {
		changed = []string{}
	}
	out := orderedJSON{
		keys:   []string{"envPath", "backupPath", "changed", "values"},
		values: map[string]any{"envPath": applied.EnvPath, "backupPath": applied.BackupPath, "changed": changed, "values": values},
	}
	text, err := jsonText(out, true)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(env.Stdout, text)
	return 0
}
