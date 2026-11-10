package contract

import (
	"fmt"
	"net/url"
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
	// Record is what to do with the answers (CONTRACT_RECORD).
	Record RecordMode
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
}

// LoadSettings reads the settings through getenv (os.Getenv in a run).
func LoadSettings(getenv func(string) string) (*Settings, error) {
	s := &Settings{
		PasswordFile: "/stack/secrets/e2e_password",
		GoldenDir:    "testdata/golden",
		RoutesFile:   "testdata/routes.json",
		Timeout:      20 * time.Second,
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
	s.RequireGo = strings.TrimSpace(getenv("CONTRACT_REQUIRE_GO")) == "1"
	if v := strings.TrimSpace(getenv("CONTRACT_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("CONTRACT_TIMEOUT %q is not a duration like 20s", v)
		}
		s.Timeout = d
	}
	return s, nil
}
