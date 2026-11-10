package ssrfguard

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"unicode/utf8"
)

// testdata/vectors.jsonl.gz was recorded by testdata/nodeoracle/gen.js
// from web-nodejs/lib/ssrfGuard.js and Node's URL parser. Every line is
// one call and its answer; the tests replay them all.

type vectorScenario struct {
	Name    string   `json:"name"`
	Records []string `json:"records"`
	None    bool     `json:"none"`
	Error   string   `json:"error"`
}

type vectorLookup struct {
	Host     string `json:"host"`
	All      bool   `json:"all"`
	Verbatim bool   `json:"verbatim"`
}

type vectorURL struct {
	Href     string `json:"href"`
	Protocol string `json:"protocol"`
	Username string `json:"username"`
	Password string `json:"password"`
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
	Pathname string `json:"pathname"`
	Search   string `json:"search"`
	Hash     string `json:"hash"`
}

type vector struct {
	Fn           string          `json:"fn"`
	IP           string          `json:"ip"`
	Host         string          `json:"host"`
	URL          string          `json:"url"`
	AllowPrivate bool            `json:"allowPrivate"`
	Blocked      bool            `json:"blocked"`
	OK           json.RawMessage `json:"ok"`
	Err          *string         `json:"err"`
	Code         string          `json:"code"`
	Fail         bool            `json:"fail"`
	Scenario     vectorScenario  `json:"scenario"`
	Lookups      []vectorLookup  `json:"lookups"`
}

func loadVectors(t testing.TB) []vector {
	t.Helper()
	f, err := os.Open("testdata/vectors.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []vector
	lines := bufio.NewScanner(zr)
	lines.Buffer(make([]byte, 1<<20), 1<<24)
	for lines.Scan() {
		var v vector
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatalf("vector %d: %v", len(out)+1, err)
		}
		out = append(out, v)
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// scripted is a resolver that answers what the vector's scenario says and
// notes what it was asked.
func scripted(sc vectorScenario, asked *[]string) *Guard {
	return &Guard{Lookup: func(_ context.Context, host string) ([]string, error) {
		*asked = append(*asked, host)
		if sc.Error != "" {
			return nil, errors.New(sc.Error)
		}
		return sc.Records, nil
	}}
}

func sameLookups(asked []string, want []vectorLookup) bool {
	if len(asked) != len(want) {
		return false
	}
	for i := range want {
		if asked[i] != want[i].Host {
			return false
		}
	}
	return true
}

func refusal(err error) (string, string) {
	var refused *BlockedError
	if errors.As(err, &refused) {
		return refused.Message, refused.Code
	}
	return "<" + err.Error() + ">", ""
}

func TestNodeVectors(t *testing.T) {
	ctx := context.Background()
	counts := map[string]int{}
	idna := 0
	failures := 0
	fail := func(format string, args ...any) {
		failures++
		if failures <= 12 {
			t.Errorf(format, args...)
		}
	}
	for i, v := range loadVectors(t) {
		counts[v.Fn]++
		opts := Options{AllowPrivate: v.AllowPrivate}
		switch v.Fn {
		case "isBlockedIp":
			if got := IsBlockedIP(v.IP, opts); got != v.Blocked {
				fail("vector %d: IsBlockedIP(%q, %+v) = %v, Node says %v", i, v.IP, opts, got, v.Blocked)
			}

		case "hostname":
			got, err := SafeHostname(v.Host, opts)
			if v.Err != nil {
				if err == nil {
					fail("vector %d: SafeHostname(%q, %+v) = %q, Node refuses: %s", i, v.Host, opts, got, *v.Err)
				} else if msg, code := refusal(err); msg != *v.Err || code != v.Code {
					fail("vector %d: SafeHostname(%q, %+v) refuses with %q (%s), Node with %q (%s)", i, v.Host, opts, msg, code, *v.Err, v.Code)
				}
				continue
			}
			var want string
			_ = json.Unmarshal(v.OK, &want)
			if err != nil || got != want {
				fail("vector %d: SafeHostname(%q, %+v) = %q, %v, Node says %q", i, v.Host, opts, got, err, want)
			}

		case "resolved":
			var asked []string
			got, err := scripted(v.Scenario, &asked).SafeResolvedHost(ctx, v.Host, opts)
			if !sameLookups(asked, v.Lookups) {
				fail("vector %d: SafeResolvedHost(%q) asked the resolver for %q, Node for %+v", i, v.Host, asked, v.Lookups)
			}
			for _, l := range v.Lookups {
				if !l.All || !l.Verbatim {
					t.Fatalf("vector %d: Node's lookup did not ask for all records in the resolver's order", i)
				}
			}
			if v.Err != nil {
				if err == nil {
					fail("vector %d: SafeResolvedHost(%q, %+v, %s) = %q, Node refuses: %s", i, v.Host, opts, v.Scenario.Name, got, *v.Err)
				} else if msg, code := refusal(err); msg != *v.Err || code != v.Code {
					fail("vector %d: SafeResolvedHost(%q, %+v, %s) refuses with %q, Node with %q", i, v.Host, opts, v.Scenario.Name, msg, *v.Err)
				}
				continue
			}
			var want string
			_ = json.Unmarshal(v.OK, &want)
			if err != nil || got != want {
				fail("vector %d: SafeResolvedHost(%q, %+v, %s) = %q, %v, Node says %q", i, v.Host, opts, v.Scenario.Name, got, err, want)
			}

		case "parse":
			if !utf8.ValidString(v.URL) {
				continue
			}
			got, err := parseURL(v.URL)
			if errors.Is(err, errNeedsIDNA) {
				idna++
				continue
			}
			if v.Fail {
				if err == nil {
					fail("vector %d: ParseURL(%q) = %+v, Node throws", i, v.URL, got)
				}
				continue
			}
			var want vectorURL
			_ = json.Unmarshal(v.OK, &want)
			if err != nil {
				fail("vector %d: ParseURL(%q) fails, Node says %+v", i, v.URL, want)
				continue
			}
			have := vectorURL{Href: got.Href, Protocol: got.Protocol, Username: got.Username, Password: got.Password, Hostname: got.Hostname, Port: got.Port, Pathname: got.Pathname, Search: got.Search, Hash: got.Hash}
			if want.Protocol != "http:" && want.Protocol != "https:" {
				// Only what a check reads is kept for other schemes
				want.Href, want.Pathname, want.Search, want.Hash = "", "", "", ""
			}
			if !reflect.DeepEqual(have, want) {
				fail("vector %d: ParseURL(%q)\n got  %+v\n node %+v", i, v.URL, have, want)
			}

		case "httpUrl", "monitoringUrl":
			if !utf8.ValidString(v.URL) {
				continue
			}
			var asked []string
			g := scripted(v.Scenario, &asked)
			var got *URL
			var err error
			if v.Fn == "monitoringUrl" {
				got, err = g.SafeMonitoringURL(ctx, v.URL)
			} else {
				got, err = g.SafeHTTPURL(ctx, v.URL, opts)
			}
			if _, parseErr := parseURL(v.URL); errors.Is(parseErr, errNeedsIDNA) {
				// The one place the port is stricter: such a host is refused
				// as a URL that does not parse, whatever Node made of it
				idna++
				if msg, _ := refusal(errOr(err)); err == nil || msg != "Invalid URL" {
					fail("vector %d: %s(%q) with a host that needs IDNA: %v, want it refused as Invalid URL", i, v.Fn, v.URL, err)
				}
				continue
			}
			if !sameLookups(asked, v.Lookups) {
				fail("vector %d: %s(%q) asked the resolver for %q, Node for %+v", i, v.Fn, v.URL, asked, v.Lookups)
			}
			if v.Err != nil {
				if err == nil {
					fail("vector %d: %s(%q, %+v, %s) = %+v, Node refuses: %s", i, v.Fn, v.URL, opts, v.Scenario.Name, got, *v.Err)
				} else if msg, code := refusal(err); msg != *v.Err || code != v.Code {
					fail("vector %d: %s(%q, %+v, %s) refuses with %q, Node with %q", i, v.Fn, v.URL, opts, v.Scenario.Name, msg, *v.Err)
				}
				continue
			}
			var want vectorURL
			_ = json.Unmarshal(v.OK, &want)
			if err != nil || got.Href != want.Href || got.Hostname != want.Hostname {
				fail("vector %d: %s(%q, %+v, %s) = %+v, %v, Node says %+v", i, v.Fn, v.URL, opts, v.Scenario.Name, got, err, want)
			}

		default:
			t.Fatalf("vector %d: unknown fn %q", i, v.Fn)
		}
	}
	if failures > 12 {
		t.Errorf("%d more differences not shown", failures-12)
	}
	for fn, least := range map[string]int{"isBlockedIp": 10000, "hostname": 4000, "resolved": 1500, "parse": 9000, "httpUrl": 9000, "monitoringUrl": 1500} {
		if counts[fn] < least {
			t.Errorf("%d vectors for %s, want at least %d: is the recorded file complete?", counts[fn], fn, least)
		}
	}
	t.Logf("replayed %v; %d URLs have a host that needs IDNA and are refused here", counts, idna)
}

func errOr(err error) error {
	if err == nil {
		return errors.New("no error")
	}
	return err
}
