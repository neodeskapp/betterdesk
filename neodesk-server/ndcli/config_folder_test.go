package ndcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A command with the address and the token given (flags, the environment
// or a token file) needs no config file, so a config folder that can't be
// found or read doesn't stop it: a user whose XDG_CONFIG_HOME or HOME
// still points at another account's folder (sudo, runuser) can use the
// command line with a token. When the address or the token has to come
// from the config file, the error still says why.
func TestAnAddressAndATokenGivenNeedNoConfigFolder(t *testing.T) {
	f := newFake(t)
	unreadable := filepath.Join(t.TempDir(), "neodesk", "config.json")
	if err := os.MkdirAll(unreadable, 0o700); err != nil { // a folder where the file should be
		t.Fatal(err)
	}
	folders := map[string]func() (string, error){
		"no config folder":           func() (string, error) { return "", errors.New("neither $XDG_CONFIG_HOME nor $HOME are defined") },
		"a config file it can't use": func() (string, error) { return filepath.Dir(filepath.Dir(unreadable)), nil },
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, folder := range folders {
		for how, args := range map[string][]string{
			"the environment": {"devices", "list"},
			"a token file":    {"--url", f.srv.URL, "--token-file", tokenFile, "devices", "list"},
		} {
			var out, errOut bytes.Buffer
			env := map[string]string{}
			if how == "the environment" {
				env = map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
			}
			e := &Env{
				Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
				Getenv:        func(k string) string { return env[k] },
				UserConfigDir: folder,
				Terminal:      func(any) bool { return false },
				Version:       "test",
			}
			if code := Main(args, e); code != 0 {
				t.Errorf("%s, address and token from %s: exit %d: %s", name, how, code, errOut.String())
			}
		}
		// Without a token the config file is needed, and its problem is named
		var errOut bytes.Buffer
		e := &Env{
			Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &errOut,
			Getenv:        func(string) string { return "" },
			UserConfigDir: folder,
			Terminal:      func(any) bool { return false },
			Version:       "test",
		}
		if code := Main([]string{"--url", f.srv.URL, "devices", "list"}, e); code != ExitConfig {
			t.Errorf("%s, no token: exit %d, want %d: %s", name, code, ExitConfig, errOut.String())
		}
	}
}
