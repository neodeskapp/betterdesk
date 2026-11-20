package webui

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	// check, when a bug fix set it, replaces the comparison with Node's
	// answer where the recording cannot say what the fix answers: the
	// status is compared, then check names what is wrong ("" for nothing).
	check func(header http.Header, body string) string
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
				failing := failingTargets(golden.Cases)
				for _, c := range golden.Cases {
					if bug := intended(config.base, &c, failing); bug != "" {
						fixed[bug]++
					}
					rec := httptest.NewRecorder()
					replay(h, rec, rawRequest(c.Method, c.Target, c.Headers))
					res := rec.Result()
					var problems []string

					if res.StatusCode != c.Status {
						problems = append(problems, "status "+strconv.Itoa(res.StatusCode)+", Node "+strconv.Itoa(c.Status))
					}
					if c.check != nil {
						if problem := c.check(res.Header, rec.Body.String()); problem != "" {
							problems = append(problems, problem)
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
// fix changed). failing holds the targets of failingTargets.
func intended(base string, c *goldenCase, failing map[string]bool) string {
	// S15: a file of the build replaces the Cache-Control the steps before
	// set (the recording's preset) with its own hour
	if preset := c.Headers["X-Test-Preset"]; preset != "" && c.Answer["cache-control"] == preset && c.Answer["last-modified"] != "" {
		c.Answer["cache-control"] = "public, max-age=3600"
		return "S15"
	}
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
	// W5: with a build for another base, the sign-in page someone signed
	// out is sent to is under that base
	if loc := c.Answer["location"]; base != "" && c.Status == http.StatusFound && strings.HasPrefix(loc, "/login") {
		c.Answer["location"] = base + loc
		if c.Body != nil {
			body := strings.Replace(*c.Body, "Redirecting to /login", "Redirecting to "+base+"/login", 1)
			c.Body = &body
		}
		// The body names the address once, also where HEAD leaves it out
		if n, err := strconv.Atoi(c.Answer["content-length"]); err == nil && n > 0 {
			c.Answer["content-length"] = strconv.Itoa(n + len(base))
		}
		return "W5"
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
		c.check = func(_ http.Header, body string) string {
			if strings.Contains(body, "CSRF token") {
				return "the CSRF refusal: " + strconv.Quote(clip(body))
			}
			return ""
		}
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
	// W4: a directory of hashed files is no file, with or without a slash
	// at the end. The request goes on to the pages, which answer the app
	// with 404, and keeps none of the hashed files' headers (Node's answer
	// carries their Vary where a build was there to find the directory)
	if (c.Method == http.MethodGet || c.Method == http.MethodHead) && c.Answer["vary"] != "" && hashedDirectory(base, c.Target) {
		head := c.Method == http.MethodHead
		c.Status = http.StatusNotFound
		c.check = func(header http.Header, body string) string {
			switch {
			case header.Get("Vary") != "" || header.Get("Content-Encoding") != "" || strings.Contains(header.Get("Cache-Control"), "immutable"):
				return "headers of a hashed file: " + fmt.Sprint(header)
			case !head && (!strings.Contains(body, "console app") || strings.Contains(body, "bd-error")):
				return "not the app's page: " + strconv.Quote(clip(body))
			}
			return ""
		}
		return "W4"
	}
	// S17: below 500 the error answer says what was wrong with the request
	// also in production, where Node said "Internal Server Error". The
	// recording has only that text, so the status is compared, then the
	// headers other than the length and the tag, then the message. An error
	// without a message still says the generic text
	op, message := c.Headers["X-Test-Op"], c.Headers["X-Test-Message"]
	head := c.Method == http.MethodHead
	failed := genericFailure(c)
	if head {
		failed = c.Status >= 400 && c.Status < http.StatusInternalServerError && (failing[c.Target] || failing["etag "+c.Answer["etag"]])
	}
	if failed && (op == "" || op == "error" && message != "") {
		answer, target := c.Answer, c.Target
		kind := "text"
		switch {
		case c.Page != nil:
			kind = "page"
		case c.Body != nil && strings.HasPrefix(*c.Body, "{"):
			kind = "json"
		}
		c.check = func(header http.Header, body string) string {
			for name, want := range answer {
				if name != "content-length" && name != "etag" && want != "{POLICY}" && header.Get(name) != want {
					return name + ": " + strconv.Quote(header.Get(name)) + ", Node " + strconv.Quote(want)
				}
			}
			if head {
				if body != "" {
					return "a body: " + strconv.Quote(clip(body))
				}
				return ""
			}
			shown, ok := shownMessage(kind, body)
			switch {
			case !ok:
				return "no error answer: " + strconv.Quote(clip(body))
			case op == "error" && shown != message,
				op == "" && !clientMessage(target, shown):
				return "message " + strconv.Quote(shown)
			}
			return ""
		}
		return "S17"
	}
	return ""
}

// genericFailure reports whether Node's answer is its error handler's
// in production: "Internal Server Error" as JSON or text, or the error
// page without a message.
func genericFailure(c *goldenCase) bool {
	if c.Status < 400 || c.Status >= http.StatusInternalServerError {
		return false
	}
	switch {
	case c.Body != nil:
		return *c.Body == `{"success":false,"error":"Internal Server Error"}` || *c.Body == "Internal Server Error"
	case c.Page != nil:
		return *c.Page == `<meta name="bd-error" content="500">`
	}
	return false
}

// failingTargets are the targets and the tags ("etag <tag>") of the
// recorded answers of genericFailure: a HEAD request for such a target, or
// with such a tag, got the same failure without a body.
func failingTargets(cases []goldenCase) map[string]bool {
	out := map[string]bool{}
	for i := range cases {
		if cases[i].Method != http.MethodHead && genericFailure(&cases[i]) {
			out[cases[i].Target] = true
			out["etag "+cases[i].Answer["etag"]] = true
		}
	}
	return out
}

// shownMessage is the message of an error answer of a kind ("json",
// "page" or "text"): the error of the JSON, the message of the error page,
// or the text.
func shownMessage(kind, body string) (string, bool) {
	switch kind {
	case "json":
		var answer struct {
			Success *bool  `json:"success"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &answer); err != nil || answer.Success == nil || *answer.Success {
			return "", false
		}
		return answer.Error, true
	case "page":
		_, rest, ok := strings.Cut(body, `<head><meta name="bd-error" content="500" data-message="`)
		if !ok {
			return "", false
		}
		message, _, _ := strings.Cut(rest, `">`)
		return html.UnescapeString(message), true
	}
	return body, true
}

// clientMessage reports whether a message is one the web app's own
// failures give below 500 for a request to target: a parameter of the
// target that does not decode, a failed precondition, a range outside
// the file, a file that is not there.
func clientMessage(target, message string) bool {
	if param, ok := strings.CutPrefix(message, "Failed to decode param '"); ok {
		param, ok = strings.CutSuffix(param, "'")
		return ok && param != "" && strings.Contains(target, param)
	}
	switch message {
	case "Precondition Failed", "Range Not Satisfiable", "Not Found":
		return true
	}
	return false
}

// hashedDirectory reports whether a request target names a directory of
// the test build's hashed files (<base>/_ui/immutable/...).
func hashedDirectory(base, target string) bool {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	rest, ok := strings.CutPrefix(target, base+"/")
	if !ok || strings.Contains(rest, "..") {
		return false
	}
	switch strings.Trim(path.Clean("/"+rest), "/") {
	case hashedDir, hashedDir + "/entry", hashedDir + "/chunks", hashedDir + "/nodes", hashedDir + "/assets", hashedDir + "/withindex":
		return true
	}
	return false
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
