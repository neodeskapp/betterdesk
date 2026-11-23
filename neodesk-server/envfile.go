package main

import (
	"fmt"
	"os"

	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// loadEnvFile fills the environment from the file NEODESK_ENV_FILE
// names: lines of KEY=value and "#" comments, the key trimmed and the
// value read as dotenv files are read (serverinfo.EnvValue: quotes, the
// escapes in double quotes, " #" comments). A variable keeps a value that
// is not empty, so the service's own settings win. The first line of a
// key that fills it wins. Without the variable nothing is read.
func loadEnvFile(getenv func(string) string, setenv func(key, value string) error, readFile func(string) ([]byte, error)) error {
	path := getenv("NEODESK_ENV_FILE")
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
		if err := setenv(line.Key, line.Value); err != nil {
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
