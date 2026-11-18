package main

import (
	"fmt"
	"os"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// loadEnvFile fills the environment from the file BETTERDESK_ENV_FILE
// names, with the rule the old console applied to its .env
// (web-nodejs/config/config.js): lines of KEY=value and "#" comments, key
// and value trimmed, and a variable keeps a value that is not empty, so
// the service's own settings win. The first line of a key that fills it
// wins. Without the variable nothing is read.
func loadEnvFile(getenv func(string) string, setenv func(key, value string) error, readFile func(string) ([]byte, error)) error {
	path := getenv("BETTERDESK_ENV_FILE")
	if path == "" {
		return nil
	}
	text, err := readFile(path)
	if err != nil {
		return fmt.Errorf("env file %s: %w", path, err)
	}
	for _, line := range serverinfo.ParseEnvLines(string(text)).Entries {
		if line.Type != serverinfo.EnvPair || getenv(line.Key) != "" {
			continue
		}
		if err := setenv(line.Key, jsbind.Trim(line.Value)); err != nil {
			return fmt.Errorf("env file %s: %s: %w", path, line.Key, err)
		}
	}
	return nil
}

// loadEnvFileFromOS is loadEnvFile on the process environment. A file
// that cannot be read is reported on stderr and the start goes on.
func loadEnvFileFromOS() {
	if err := loadEnvFile(os.Getenv, os.Setenv, os.ReadFile); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: %v\n", err)
	}
}
