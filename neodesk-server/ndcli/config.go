package ndcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config is what `neodesk login` saves: the console's address, the token
// for it and how to reach it. It lives in the user's own config folder
// (never the working folder), readable by its owner only.
//
// --insecure is never saved: a console with a certificate of its own is
// trusted with --ca-cert, which is.
type Config struct {
	URL       string `json:"url"`
	Token     string `json:"token,omitempty"`
	CACert    string `json:"ca_cert,omitempty"`
	AllowHTTP bool   `json:"allow_http,omitempty"`
}

// ErrConfigOpen is the refusal of a config file others can read or that
// belongs to someone else.
var ErrConfigOpen = errors.New("config file is open to other users")

// configPath is where the config file is: --config, NEODESK_CONFIG, or
// neodesk/config.json in the user's config folder.
func (e *Env) configPath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if p := e.Getenv("NEODESK_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := e.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no config folder: %w (set NEODESK_CONFIG)", err)
	}
	return filepath.Join(dir, "neodesk", "config.json"), nil
}

// loadConfig reads the config file. A missing file is an empty Config. A
// file that others may read or write, or that another user owns, is
// refused: its token would not be yours alone, and its address could send
// your token elsewhere.
func loadConfig(path string) (*Config, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a file", path)
	}
	if err := checkPrivate(path, info); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s is not a valid config file: %w", path, err)
	}
	return &c, nil
}

// saveConfig writes the config file readable by its owner only, through a
// temporary file so a crash leaves the old one.
func saveConfig(path string, c *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// normalizeURL checks a console address: http or https, a host, no
// query, no user name or password; without a trailing slash.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("no console address: use --url, NEODESK_URL or neodesk login")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%q is no console address like https://desk.example.com", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q: give the address without a user, query or fragment", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

// loopback reports whether an address's host is this machine.
func loopback(address string) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
