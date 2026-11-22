package contract

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RecordMode says what a run does with the answers it gets.
type RecordMode int

const (
	// Compare checks the answers against the goldens.
	Compare RecordMode = iota
	// RecordFirst writes the answers as the goldens (CONTRACT_RECORD=1).
	RecordFirst
	// RecordMerge reads the goldens of a first recording, marks what
	// differs in this run as volatile and writes them back
	// (CONTRACT_RECORD=merge).
	RecordMerge
)

// Settings is how a run is set up, read from the environment.
type Settings struct {
	// BaseURL is the console under test (BD_URL).
	BaseURL *url.URL
	// PasswordFile holds the password of the e2e_<role> accounts
	// (BD_PASSWORD_FILE).
	PasswordFile string
	// SecretsDir is the folder whose files are the secrets cases send as
	// {secret:<file name>} (BD_SECRETS_DIR). A folder that does not exist
	// holds none.
	SecretsDir string
	// Record is what to do with the answers (CONTRACT_RECORD).
	Record RecordMode
	// RecordNew lets a recording write the golden of a request the Go
	// console answers when that golden is new: there is none yet, or it
	// holds the first of the two recordings only (CONTRACT_RECORD_NEW=1).
	// A golden that holds both recordings is never written.
	RecordNew bool
	// Rewrite names goldens a recording writes although the Go console
	// answers their requests (CONTRACT_REWRITE): a regular expression
	// that must match the golden's whole name, "<layer>/<group>/<name>" as
	// under GoldenDir without ".json" ("cases/devices/list",
	// "matrix/devices/GET_api_devices"). It is for a bug fix that changes
	// what Go answers on purpose; every other golden of a Go route stays
	// frozen. nil rewrites nothing.
	Rewrite *regexp.Regexp
	// GoldenDir is where the goldens are (CONTRACT_GOLDEN).
	GoldenDir string
	// RoutesFile is the route list the matrix is generated from
	// (CONTRACT_ROUTES).
	RoutesFile string
	// RequireGo fails every route and case the Go console did not answer
	// itself (CONTRACT_REQUIRE_GO=1).
	RequireGo bool
	// Timeout is the limit for one request.
	Timeout time.Duration
	// NodeOnly says the console is the Node console alone, without a Go
	// console in front and so without the routing report
	// (CONTRACT_NODE_ONLY=1). Every answer is Node's: a recording writes all
	// of them and freezes none. It is for spec recordings of a Node tree
	// (STACK_MODE=node), so it needs a GoldenDir other than the default:
	// the committed goldens are never written this way.
	NodeOnly bool
}

// defaultGoldenDir is where the committed goldens are.
const defaultGoldenDir = "testdata/golden"

// LoadSettings reads the settings through getenv (os.Getenv in a run).
func LoadSettings(getenv func(string) string) (*Settings, error) {
	s := &Settings{
		PasswordFile: "/stack/secrets/e2e_password",
		SecretsDir:   "/stack/secrets",
		GoldenDir:    defaultGoldenDir,
		RoutesFile:   "testdata/routes.json",
		Timeout:      60 * time.Second,
	}
	raw := strings.TrimSpace(getenv("BD_URL"))
	if raw == "" {
		return nil, fmt.Errorf("BD_URL is not set: the suite needs the address of a console")
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("BD_URL %q is not an http(s) URL", raw)
	}
	s.BaseURL = u

	if v := strings.TrimSpace(getenv("BD_PASSWORD_FILE")); v != "" {
		s.PasswordFile = v
	}
	if v := strings.TrimSpace(getenv("BD_SECRETS_DIR")); v != "" {
		s.SecretsDir = v
	}
	if v := strings.TrimSpace(getenv("CONTRACT_GOLDEN")); v != "" {
		s.GoldenDir = v
	}
	if v := strings.TrimSpace(getenv("CONTRACT_ROUTES")); v != "" {
		s.RoutesFile = v
	}
	switch v := strings.TrimSpace(getenv("CONTRACT_RECORD")); v {
	case "", "0":
		s.Record = Compare
	case "1":
		s.Record = RecordFirst
	case "merge":
		s.Record = RecordMerge
	default:
		return nil, fmt.Errorf("CONTRACT_RECORD %q: use 1 (write goldens) or merge (mark what differs as volatile)", v)
	}
	switch v := strings.TrimSpace(getenv("CONTRACT_RECORD_NEW")); v {
	case "", "0":
	case "1":
		if s.Record == Compare {
			return nil, fmt.Errorf("CONTRACT_RECORD_NEW=1 is for recording: set CONTRACT_RECORD too")
		}
		s.RecordNew = true
	default:
		return nil, fmt.Errorf("CONTRACT_RECORD_NEW %q: use 1 (also record new goldens of requests the Go console answers)", v)
	}
	if v := strings.TrimSpace(getenv("CONTRACT_REWRITE")); v != "" {
		if s.Record == Compare {
			return nil, fmt.Errorf("CONTRACT_REWRITE is for recording: set CONTRACT_RECORD too")
		}
		re, err := regexp.Compile("^(?:" + v + ")$")
		if err != nil {
			return nil, fmt.Errorf("CONTRACT_REWRITE %q is not a regular expression: %w", v, err)
		}
		s.Rewrite = re
	}
	s.RequireGo = strings.TrimSpace(getenv("CONTRACT_REQUIRE_GO")) == "1"
	switch v := strings.TrimSpace(getenv("CONTRACT_NODE_ONLY")); v {
	case "", "0":
	case "1":
		if filepath.Clean(s.GoldenDir) == filepath.Clean(defaultGoldenDir) {
			return nil, fmt.Errorf("CONTRACT_NODE_ONLY=1 needs CONTRACT_GOLDEN set to a folder of its own: the goldens in %s are the specification of the moved routes", defaultGoldenDir)
		}
		if s.RequireGo {
			return nil, fmt.Errorf("CONTRACT_NODE_ONLY=1 and CONTRACT_REQUIRE_GO=1 exclude each other: a Node console alone has no Go answers")
		}
		s.NodeOnly = true
	default:
		return nil, fmt.Errorf("CONTRACT_NODE_ONLY %q: use 1 (the console is Node alone)", v)
	}
	if v := strings.TrimSpace(getenv("CONTRACT_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("CONTRACT_TIMEOUT %q is not a duration like 20s", v)
		}
		s.Timeout = d
	}
	return s, nil
}
