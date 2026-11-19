package ssrfguard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/ssrfguard/guardtest"
)

// nodeOracle is a running testdata/nodeoracle/server.js: the Node guard,
// asked one question per line.
type nodeOracle struct {
	mu  sync.Mutex
	in  io.WriteCloser
	out *bufio.Reader
}

type oracleQuestion struct {
	Fn           string   `json:"fn"`
	Input        string   `json:"input"`
	AllowPrivate bool     `json:"allowPrivate"`
	Records      []string `json:"records"`
	Error        string   `json:"error,omitempty"`
}

type oracleAnswer struct {
	OK      json.RawMessage `json:"ok"`
	Err     *string         `json:"err"`
	Lookups []string        `json:"lookups"`
}

var (
	oracleOnce sync.Once
	oracle     *nodeOracle
)

// startOracle starts the Node guard when node and web-nodejs are there.
// Without them it returns nil and the fuzz test checks the Go code against
// its own rules only.
func startOracle() *nodeOracle {
	oracleOnce.Do(func() {
		script := filepath.Join("testdata", "nodeoracle", "server.js")
		if _, err := os.Stat(filepath.Join("..", "..", "..", "web-nodejs", "lib", "ssrfGuard.js")); err != nil {
			return
		}
		node, err := exec.LookPath("node")
		if err != nil {
			return
		}
		cmd := exec.Command(node, script)
		cmd.Stderr = os.Stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			return
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return
		}
		if err := cmd.Start(); err != nil {
			return
		}
		oracle = &nodeOracle{in: in, out: bufio.NewReaderSize(out, 1<<20)}
	})
	return oracle
}

func (o *nodeOracle) ask(q oracleQuestion) (oracleAnswer, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if q.Records == nil {
		q.Records = []string{}
	}
	line, err := json.Marshal(q)
	if err != nil {
		return oracleAnswer{}, err
	}
	if _, err := o.in.Write(append(line, '\n')); err != nil {
		return oracleAnswer{}, err
	}
	reply, err := o.out.ReadBytes('\n')
	if err != nil {
		return oracleAnswer{}, err
	}
	var a oracleAnswer
	err = json.Unmarshal(reply, &a)
	return a, err
}

// jsonSafe reports whether text reaches Node unchanged through JSON: valid
// UTF-8 without the replacement character, which is what a lone surrogate
// or a broken byte would have become on the way in.
func jsonSafe(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, utf8.RuneError)
}

// plainlyDangerous is the rule the guard exists for, written without the
// guard's own code: an IPv4 address in its usual spelling that is
// loopback or link-local, or private while private addresses are not
// allowed.
func plainlyDangerous(address string, opts Options) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || ip.String() != address {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	if opts.AllowPrivate {
		return false
	}
	return ip.IsPrivate() || ip.As4()[0] == 0 ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}

// FuzzSSRFGuard feeds hosts, URLs and resolver answers to the checks.
//
// With Node at hand every input also goes to the Node guard, and the two
// must agree: apart from hosts that need the rules for international
// domain names (refused here) and the answers the fixed rows of BUGS.md
// change (guardtest), the answers and messages are the same.
//
// Without Node the checks are held to the plain rule: no check passes a
// host that is, or resolves to, an IPv4 address that is plainly dangerous.
func FuzzSSRFGuard(f *testing.F) {
	seeds := []struct {
		kind         uint8
		input        string
		records      string
		allowPrivate bool
	}{
		{0, "127.0.0.1", "", false}, {0, "::ffff:127.0.0.1", "", true}, {0, "fe80::1%eth0", "", false}, {0, "0:0:0:0:0:0:0:1", "", false},
		{1, "localhost", "", false}, {1, " [::1] ", "", true}, {1, "LOCALHOST.", "", false}, {1, "10.0.0.1", "", true},
		{2, "example.com", "93.184.216.34", false}, {2, "rebind.example", "8.8.8.8,127.0.0.1", true}, {2, "2130706433", "127.0.0.1", false},
		{2, "nas.lan", "192.168.1.5", true}, {2, "v6.example", "::1", false}, {2, "gone.example", "!getaddrinfo ENOTFOUND gone.example", false},
		{3, "http://example.com/", "93.184.216.34", false}, {3, "http://0x7f.1/", "", false}, {3, "http://[::ffff:127.0.0.1]/", "", false},
		{3, "http://user:pw@example.com/", "1.1.1.1", false}, {3, "http://example.com\\@127.0.0.1/", "1.1.1.1", false}, {3, "file:///etc/passwd", "", false},
		{3, "http://%31%32%37.0.0.1/", "", true}, {3, "https://EXAMPLE.com:443/a/../b?c#d", "10.0.0.1", true}, {3, "http://127.1:80/", "", false},
		{3, "ht\ttp://169.254.169.254/latest", "", true}, {3, "http://[fe80::1]/", "", true}, {3, "http://1.1.1.1.1/", "", false},
	}
	for _, s := range seeds {
		f.Add(s.kind, s.input, s.records, s.allowPrivate)
	}
	f.Fuzz(func(t *testing.T, kind uint8, input, recordList string, allowPrivate bool) {
		opts := Options{AllowPrivate: allowPrivate}
		var records []string
		lookupErr := ""
		if strings.HasPrefix(recordList, "!") {
			lookupErr = recordList[1:]
			if lookupErr == "" {
				lookupErr = "getaddrinfo ENOTFOUND host"
			}
		} else if recordList != "" {
			records = strings.Split(recordList, ",")
		}
		var asked []string
		g := &Guard{Lookup: func(_ context.Context, host string) ([]string, error) {
			asked = append(asked, host)
			if lookupErr != "" {
				return nil, errors.New(lookupErr)
			}
			return records, nil
		}}
		ctx := context.Background()

		q := oracleQuestion{Input: input, AllowPrivate: allowPrivate, Records: records, Error: lookupErr}
		var passed bool // the Go check let the input through
		var got string  // its answer, or its refusal
		var idna bool
		switch kind % 4 {
		case 0:
			q.Fn = "isBlockedIp"
			blockedHere := IsBlockedIP(input, opts)
			passed, got = !blockedHere, "false"
			if blockedHere {
				got = "true"
			}
			if !blockedHere && plainlyDangerous(input, opts) {
				t.Fatalf("IsBlockedIP(%q, %+v) lets a dangerous address through", input, opts)
			}
			if blockedHere != guardtest.Blocked(input, allowPrivate) {
				t.Fatalf("IsBlockedIP(%q, %+v) = %v, the ranges say %v", input, opts, blockedHere, !blockedHere)
			}
		case 1:
			q.Fn = "hostname"
			clean, err := SafeHostname(input, opts)
			passed, got = err == nil, clean
			if err != nil {
				got = err.Error()
			} else if plainlyDangerous(clean, opts) {
				t.Fatalf("SafeHostname(%q, %+v) = %q, a dangerous address", input, opts, clean)
			}
		case 2:
			q.Fn = "resolved"
			clean, err := g.SafeResolvedHost(ctx, input, opts)
			passed, got = err == nil, clean
			if err != nil {
				got = err.Error()
			} else {
				for _, address := range append([]string{clean}, recordsIfAsked(asked, records)...) {
					if plainlyDangerous(address, opts) {
						t.Fatalf("SafeResolvedHost(%q, %+v, records %q) = %q although %s is dangerous", input, opts, records, clean, address)
					}
				}
			}
		case 3:
			q.Fn = "httpUrl"
			parsed, err := g.SafeHTTPURL(ctx, input, opts)
			passed = err == nil
			if err != nil {
				got = err.Error()
				_, parseErr := parseURL(input)
				idna = errors.Is(parseErr, errNeedsIDNA)
			} else {
				got = parsed.Href + " " + parsed.Hostname
				host := strings.TrimSuffix(strings.TrimPrefix(parsed.Hostname, "["), "]")
				for _, address := range append([]string{host}, recordsIfAsked(asked, records)...) {
					if plainlyDangerous(address, opts) {
						t.Fatalf("SafeHTTPURL(%q, %+v, records %q) passes although %s is dangerous", input, opts, records, address)
					}
				}
				if parsed.Protocol != "http:" && parsed.Protocol != "https:" {
					t.Fatalf("SafeHTTPURL(%q) passes the scheme %s", input, parsed.Protocol)
				}
				if again, ok := ParseURL(parsed.Href); !ok || again.Href != parsed.Href || again.Hostname != parsed.Hostname {
					t.Fatalf("SafeHTTPURL(%q): the normal form %q does not parse to itself: %+v", input, parsed.Href, again)
				}
			}
		}

		node := startOracle()
		if node == nil || !jsonSafe(input) || !jsonSafe(recordList) {
			return
		}
		answer, err := node.ask(q)
		if err != nil {
			t.Fatalf("the Node oracle did not answer: %v", err)
		}
		nodePassed := answer.Err == nil
		if q.Fn == "isBlockedIp" {
			nodePassed = string(answer.OK) == "false"
		}
		if idna {
			// Refused here whatever Node says
			if passed && !nodePassed {
				t.Fatalf("%s(%q, allowPrivate %v) passes here as %q, Node refuses: %s", q.Fn, input, allowPrivate, got, string(answer.OK)+deref(answer.Err))
			}
			return
		}
		if strings.Join(asked, "\x00") != strings.Join(answer.Lookups, "\x00") {
			t.Fatalf("%s(%q): the resolver was asked for %q here and for %q by Node", q.Fn, input, asked, answer.Lookups)
		}
		// An answer the fixed rows of BUGS.md change is not Node's, but the
		// one guardtest gives
		if q.Fn == "isBlockedIp" {
			if guardtest.Change(input, allowPrivate) != "" {
				return
			}
		} else if goErr := map[bool]string{false: got, true: ""}[passed]; passed != nodePassed || !passed && goErr != deref(answer.Err) {
			if bug := guardtest.MessageChange(deref(answer.Err), goErr, allowPrivate); bug == "unexplained" {
				t.Fatalf("%s(%q, allowPrivate %v, records %q, error %q)\n here %q\n node %s%s", q.Fn, input, allowPrivate, records, lookupErr, got, answer.OK, deref(answer.Err))
			}
			return
		}
		var want string
		switch {
		case q.Fn == "isBlockedIp":
			want = string(answer.OK)
		case answer.Err != nil:
			want = *answer.Err
		case q.Fn == "httpUrl":
			var u vectorURL
			_ = json.Unmarshal(answer.OK, &u)
			want = u.Href + " " + u.Hostname
			// Node left some dot segments in a path (BUGS.md SC10); its URL,
			// read again here, has them resolved
			if again, ok := ParseURL(u.Href); ok && got != want && got == again.Href+" "+u.Hostname {
				return
			}
		default:
			_ = json.Unmarshal(answer.OK, &want)
		}
		if got != want {
			t.Fatalf("%s(%q, allowPrivate %v, records %q, error %q)\n here %q\n node %q", q.Fn, input, allowPrivate, records, lookupErr, got, want)
		}
	})
}

// The spellings of an IPv4 address the system resolver reads give an
// address in the usual form, and the usual form gives itself.
func FuzzLegacyIPv4(f *testing.F) {
	for _, seed := range []string{"127.1", "2130706433", "0x7f.0.0.1", "0177.0.0.1", "1.2.3.4", "256", "0x", "08.1.1.1", "1.2.3.4.5", "4294967296", "", ".", "1..2"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		address, ok := legacyIPv4(host)
		if !ok {
			if IsIPv4(host) {
				t.Fatalf("the address %q is not read", host)
			}
			return
		}
		if !IsIPv4(address) {
			t.Fatalf("%q gives %q, which is no address", host, address)
		}
		if IsIPv4(host) && address != host {
			t.Fatalf("the address %q gives %q", host, address)
		}
	})
}

// recordsIfAsked is the resolver's answer when the check used it.
func recordsIfAsked(asked, records []string) []string {
	if len(asked) == 0 {
		return nil
	}
	return records
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestNodeOracle checks the line to the Node guard that the fuzz test
// relies on. It is skipped where Node is not installed.
func TestNodeOracle(t *testing.T) {
	node := startOracle()
	if node == nil {
		t.Skip("node or web-nodejs is not here")
	}
	answer, err := node.ask(oracleQuestion{Fn: "resolved", Input: "Rebind.Example", Records: []string{"8.8.8.8", "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if deref(answer.Err) != "Hostname rebind.example resolves to blocked address 127.0.0.1" || len(answer.Lookups) != 1 || answer.Lookups[0] != "rebind.example" {
		t.Errorf("Node answered %+v, error %q", answer, deref(answer.Err))
	}
	answer, err = node.ask(oracleQuestion{Fn: "httpUrl", Input: "http://0x7f.1/"})
	if err != nil || deref(answer.Err) != "Blocked IP address: 127.0.0.1" {
		t.Errorf("Node answered %+v, error %q, %v", answer, deref(answer.Err), err)
	}
}
