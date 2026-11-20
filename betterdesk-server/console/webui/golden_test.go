package webui

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The goldens are what the Node console answered for a list of requests
// against the build in testdata/site (recorded by
// .ui-rework/go/tools/webui/gen-webui-golden.js, which runs the real
// lib/webUi.js and lib/mountRoutes.js). Each file is one configuration,
// compressed with gzip.

type goldenFile struct {
	Policy *string      `json:"policy"`
	Cases  []goldenCase `json:"cases"`
}

type goldenCase struct {
	Method  string            `json:"method"`
	Target  string            `json:"target"`
	Headers map[string]string `json:"headers"`
	Status  int               `json:"status"`
	Answer  map[string]string `json:"answer"`
	// Page: the answer is the app's page with this text put after <head>.
	Page       *string `json:"page"`
	Body       *string `json:"body"`
	BodyBase64 *string `json:"bodyBase64"`
	// NamesDir: the answer names the build directory ({DIR} in the body),
	// so its length and its tag depend on where the build is.
	NamesDir bool `json:"namesDir"`
	// notCSRF: a bug fix replaced Node's CSRF refusal (W2); only the status
	// is compared, and that the answer is no CSRF refusal.
	notCSRF bool
}

func loadGolden(t *testing.T, name string) goldenFile {
	t.Helper()
	file, err := os.Open("testdata/golden_" + name + ".json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unpacked, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var golden goldenFile
	if err := json.NewDecoder(unpacked).Decode(&golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// replay answers a test request the way the recorded Node app was wired:
// the routes (which fail on request of the X-Test-Op header), then the web
// app, then the 404 handler.
func replay(h *Host, w http.ResponseWriter, r *http.Request) {
	if preset := r.Header.Get("X-Test-Preset"); preset != "" {
		w.Header().Set("Cache-Control", preset)
	}
	number := func(name string) int {
		n, _ := strconv.Atoi(r.Header.Get(name))
		return n
	}
	switch r.Header.Get("X-Test-Op") {
	case "csrf":
		h.CSRFError(w, r)
	case "error":
		h.ServerError(w, r, errors.New(r.Header.Get("X-Test-Message")), number("X-Test-Status"))
	case "errorpage":
		kind, status := number("X-Test-Kind"), number("X-Test-Status")
		if !h.ErrorPage(w, r, kind, ErrorOptions{Status: status, Title: r.Header.Get("X-Test-Title"), Message: r.Header.Get("X-Test-Message")}) {
			if status == 0 {
				status = kind
			}
			httpx.WriteBody(w, r, status, plainText, []byte("declined"))
		}
	default:
		if !h.Handle(w, r) {
			h.NotFound(w, r)
		}
	}
}

func TestAnswersMatchNode(t *testing.T) {
	configs := []struct {
		name    string
		base    string
		options Options
		nobuild bool
		atLeast int
	}{
		{name: "root", options: Options{Production: true}, atLeast: 2000},
		{name: "dev", options: Options{}, atLeast: 700},
		{name: "https", options: Options{Production: true, HTTPS: true}, atLeast: 700},
		{name: "app2", base: "/app2", options: Options{Production: true}, atLeast: 2000},
		{name: "nobuild", options: Options{Production: true}, nobuild: true, atLeast: 700},
	}
	for _, config := range configs {
		for _, inBinary := range []bool{false, true} {
			name := config.name
			if inBinary {
				name += "/embedded"
			}
			t.Run(name, func(t *testing.T) {
				golden := loadGolden(t, config.name)
				if len(golden.Cases) < config.atLeast {
					t.Fatalf("only %d recorded cases", len(golden.Cases))
				}
				dir := filepath.Join(t.TempDir(), "missing")
				index := ""
				if !config.nobuild {
					dir = copySite(t, config.base)
					index = readSite(t, dir, "index.html")
				}
				options := config.options
				options.Dir = dir
				options.Session = sessionFromHeader
				h, named := New(options), dir
				if inBinary {
					h, named = embeddedHost(t, options, config.nobuild), embeddedDir
				}
				if h.Base() != config.base {
					t.Fatalf("base %q, want %q", h.Base(), config.base)
				}
				if golden.Policy != nil && h.page.csp != *golden.Policy {
					t.Fatalf("policy\n got %s\nwant %s", h.page.csp, *golden.Policy)
				}

				differences := 0
				fixed := map[string]int{}
				for _, c := range golden.Cases {
					if bug := intended(config.base, &c); bug != "" {
						fixed[bug]++
					}
					rec := httptest.NewRecorder()
					replay(h, rec, rawRequest(c.Method, c.Target, c.Headers))
					res := rec.Result()
					var problems []string

					if res.StatusCode != c.Status {
						problems = append(problems, "status "+strconv.Itoa(res.StatusCode)+", Node "+strconv.Itoa(c.Status))
					}
					if c.notCSRF {
						if strings.Contains(rec.Body.String(), "CSRF token") {
							problems = append(problems, "the CSRF refusal: "+strconv.Quote(clip(rec.Body.String())))
						}
						if len(problems) > 0 {
							differences++
							t.Errorf("%s %q %v: %s", c.Method, c.Target, c.Headers, strings.Join(problems, "; "))
						}
						continue
					}
					got := map[string]string{}
					for name, values := range res.Header {
						got[strings.ToLower(name)] = strings.Join(values, ", ")
					}
					names := map[string]bool{}
					for name := range got {
						names[name] = true
					}
					for name := range c.Answer {
						names[name] = true
					}
					sorted := make([]string, 0, len(names))
					for name := range names {
						sorted = append(sorted, name)
					}
					sort.Strings(sorted)
					for _, name := range sorted {
						if c.NamesDir && (name == "content-length" || name == "etag") {
							continue
						}
						want, wanted := c.Answer[name]
						if want == "{POLICY}" {
							want = *golden.Policy
						}
						have, has := got[name]
						if has != wanted || have != want {
							problems = append(problems, name+": "+strconv.Quote(have)+", Node "+strconv.Quote(want))
						}
					}

					want := ""
					switch {
					case c.Page != nil:
						at := strings.Index(index, "<head>") + len("<head>")
						want = index[:at] + strings.ReplaceAll(*c.Page, "{DIR}", named) + index[at:]
					case c.Body != nil:
						want = strings.ReplaceAll(*c.Body, "{DIR}", named)
					case c.BodyBase64 != nil:
						raw, err := base64.StdEncoding.DecodeString(*c.BodyBase64)
						if err != nil {
							t.Fatal(err)
						}
						want = string(raw)
					}
					if body := rec.Body.String(); body != want {
						problems = append(problems, "body "+strconv.Quote(clip(body))+", Node "+strconv.Quote(clip(want)))
					}

					if len(problems) > 0 {
						differences++
						if differences <= 12 {
							t.Errorf("%s %q %v\n    %s", c.Method, c.Target, c.Headers, strings.Join(problems, "\n    "))
						} else if differences <= 150 {
							t.Errorf("%s %q %v: %s", c.Method, c.Target, c.Headers, clip(problems[0]))
						}
					}
				}
				if differences > 0 {
					t.Errorf("%d of %d answers differ from Node", differences, len(golden.Cases))
				}
				for bug, n := range fixed {
					t.Logf("%s: %d answers compared with the fix's answer in place of Node's", bug, n)
				}
			})
		}
	}
}

// intended turns a recorded answer into what the console answers since a
// bug fix changed it on purpose, and names the bug ("" for an answer no
// fix changed).
func intended(base string, c *goldenCase) string {
	// W1: the way back after sign-in is checked with its dot segments
	// resolved; one the check refuses sends to the start
	if loc := c.Answer["location"]; c.Status == http.StatusFound && strings.Contains(loc, "/.") {
		if _, ok := httpx.SafeReturnPath(loc); !ok && c.Body != nil {
			c.Answer["location"] = "/"
			body := strings.ReplaceAll(*c.Body, loc, "/")
			c.Body = &body
			c.Answer["content-length"] = strconv.Itoa(len(body))
			return "W1"
		}
	}
	// W2: an error whose message holds "csrf" keeps its own status; the
	// answer is no CSRF refusal. Node's recording has only the refusal, so
	// the status is what is compared
	refused := answerText(c, "CSRF token") || c.Method == http.MethodHead && strings.Contains(c.Target, "csrf%zz")
	if c.Status == http.StatusForbidden && c.Headers["X-Test-Op"] != "csrf" && refused {
		switch {
		case c.Headers["X-Test-Op"] == "error":
			c.Status = http.StatusInternalServerError
			if s, _ := strconv.Atoi(c.Headers["X-Test-Status"]); s != 0 {
				c.Status = s
			}
		case strings.Contains(c.Target, "%zz"):
			c.Status = http.StatusBadRequest
		default:
			return ""
		}
		c.notCSRF = true
		return "W2"
	}
	// W3: a hashed-file path whose dot segments lead out of the hashed
	// files gets the empty 404 of a path that leaves the build
	if (c.Method == http.MethodGet || c.Method == http.MethodHead) && leavesHashedFiles(base, c.Target) {
		c.Status, c.Answer, c.Page = http.StatusNotFound, map[string]string{}, nil
		if c.Method == http.MethodGet {
			c.Answer["content-length"] = "0"
		}
		empty := ""
		c.Body = &empty
		return "W3"
	}
	return ""
}

// leavesHashedFiles reports whether a request target is a path under the
// hashed files (<base>/_ui/immutable/) whose dot segments lead out of them.
func leavesHashedFiles(base, target string) bool {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	prefix := base + "/" + hashedDir + "/"
	if !hasPrefixFold(target, prefix) || !strings.Contains(target, "..") {
		return false
	}
	cleaned := path.Clean(target) + "/"
	return !hasPrefixFold(cleaned, prefix)
}

// answerText reports whether the recorded body or page holds a text.
func answerText(c *goldenCase, text string) bool {
	return c.Body != nil && strings.Contains(*c.Body, text) || c.Page != nil && strings.Contains(*c.Page, text)
}

func clip(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}
