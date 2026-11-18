package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Case is one hand-written request and what the suite does with its answer.
// Cases belong to a group (Group) and run in the order they are listed.
type Case struct {
	// Name names the case in its group. It is the name of the golden file:
	// lower-case letters, digits, dash, dot and underscore.
	Name string
	// Method is the HTTP method, GET when empty.
	Method string
	// Path is the path to request. {name} is replaced by the variable of
	// that name (see Capture); a variable nothing captured is MissingID.
	// {secret:name} is replaced by the secret of that name (secrets.go).
	Path string
	// Query is added to the path. Values may hold {name} and
	// {secret:name} too.
	Query url.Values
	// Headers are added to the request and replace the suite's own
	// (Accept, Accept-Language, Accept-Encoding, User-Agent) by name.
	// Values may hold {name} and {secret:name}:
	// "Authorization": "Bearer {secret:device_token}".
	Headers map[string]string

	// Body is sent as JSON. Strings in it may hold {name} and
	// {secret:name}; a string that is only "{name}" is replaced by the
	// captured value with its type, so an ID that came as a number is sent
	// as a number.
	Body any
	// RawBody is sent as it is, with ContentType. Use it for malformed
	// JSON, forms and other content types.
	RawBody     string
	ContentType string
	// Multipart is sent as multipart/form-data.
	Multipart []Part

	// NoCSRF leaves out the CSRF token of a write.
	NoCSRF bool
	// NoSession sends the request without the session cookie, NoCookies
	// without any cookie.
	NoSession bool
	NoCookies bool
	// FollowRedirects follows redirects and records the last answer, with
	// the list of redirects on the way. Without it the redirect itself is
	// the answer.
	FollowRedirects bool

	// Roles are the roles the case runs for, AllRoles when empty.
	Roles []Role
	// As sends the request as this one role, whichever role's turn it is.
	// It is for setup and cleanup: create as admin what every role then
	// reads. The step still runs once for each role in Roles.
	As Role

	// Capture takes values out of the answer into variables for the cases
	// after it: variable name to where the value is. That is a path of the
	// JSON answer ("ticket": "data.id"), "header:<Name>" for a header or
	// "cookie:<name>" for a cookie the answer sets. Headers and cookies are
	// taken as the console sent them, JSON values after the instance's
	// address was replaced (see Goldens in the README). A value that is not
	// in the answer leaves the variable as it was.
	Capture map[string]string
	// CaptureSecret is Capture for values that are secrets: a token, a
	// two-factor secret. Later cases of the turn send one as
	// {secret:name}, and from now on it is masked wherever it shows, in
	// this answer too. The value must be text (a number counts as its
	// digits) of at least eight characters.
	CaptureSecret map[string]string
	// Compute sets variables from functions of earlier values, right before
	// the request is sent: variable name to "function(variable)" or
	// "function(secret:name)" ("code": "totp(secret:totp)"). The functions
	// are listed in compute.go.
	Compute map[string]string
	// Volatile lists paths of the answer that change from run to run and
	// that two recordings cannot find, because they differ in more than a
	// value: "data.sessions" for a whole object, "data.items[#]" for the
	// number of elements. A string or number marked here still keeps the
	// format the recordings share; start the path with "~" to free it of
	// that too ("~data.uptime").
	Volatile []string
	// VolatileHeaders lists headers that only have to be present.
	VolatileHeaders []string
	// Covers names the routes this case stands for in the coverage check,
	// by route ID ("POST /api/auth/logout"). The route that matches Method
	// and Path is covered without being named.
	Covers []string
	// Socket makes the case a step on a websocket at Path (socketcase.go).
	Socket *Socket
}

// Part is one part of a multipart/form-data body.
type Part struct {
	// Field is the form field's name.
	Field string
	// FileName makes the part a file upload.
	FileName string
	// ContentType is the part's content type; it may be empty for a plain field.
	ContentType string
	Content     []byte
}

// CaseGroup is the cases of one route group.
type CaseGroup struct {
	Name  string
	Cases []Case
}

var caseGroups = map[string]*CaseGroup{}

var (
	groupName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	caseName     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	variable     = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
	variableName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	// reference is a variable or a secret in a case: {name}, {secret:name}.
	reference = regexp.MustCompile(`\{(secret:)?([A-Za-z0-9_]+)\}`)
)

// Group registers the cases of a route group. Call it from an init function
// in cases_<group>_test.go. The cases of a group run in the order given,
// once per role: first every case for the first role, then for the next.
// Variables captured by a case are for the cases after it in the same
// role's turn. Groups do not see each other's variables and run in the
// order of their names.
//
// Group panics on a case it cannot run (no name, an unknown method, a bad
// path), so a mistake shows before the first request.
func Group(name string, cases ...Case) {
	if !groupName.MatchString(name) {
		panic(fmt.Sprintf("contract: group name %q: use lower-case letters, digits and dashes", name))
	}
	if _, taken := caseGroups[name]; taken {
		panic(fmt.Sprintf("contract: group %q is registered twice", name))
	}
	seen := map[string]bool{}
	for i := range cases {
		c := &cases[i]
		if c.Method == "" {
			c.Method = http.MethodGet
		}
		if err := c.check(); err != nil {
			panic(fmt.Sprintf("contract: group %q, case %d (%q): %v", name, i+1, c.Name, err))
		}
		if seen[c.Name] {
			panic(fmt.Sprintf("contract: group %q has two cases named %q", name, c.Name))
		}
		seen[c.Name] = true
	}
	caseGroups[name] = &CaseGroup{Name: name, Cases: cases}
}

// Groups lists the registered groups in the order they run: by name.
func Groups() []*CaseGroup {
	names := make([]string, 0, len(caseGroups))
	for name := range caseGroups {
		names = append(names, name)
	}
	sort.Strings(names)
	groups := make([]*CaseGroup, len(names))
	for i, name := range names {
		groups[i] = caseGroups[name]
	}
	return groups
}

var caseMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

func (c *Case) check() error {
	if !caseName.MatchString(c.Name) {
		return fmt.Errorf("name %q: use lower-case letters, digits, dash, dot and underscore", c.Name)
	}
	if !caseMethods[c.Method] {
		return fmt.Errorf("unknown method %q", c.Method)
	}
	if !strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, "?#") {
		return fmt.Errorf("path %q: it must start with / and the query goes in Query", c.Path)
	}
	bodies := 0
	if c.Body != nil {
		bodies++
		if _, err := json.Marshal(c.Body); err != nil {
			return fmt.Errorf("body cannot be sent as JSON: %w", err)
		}
	}
	if c.RawBody != "" {
		bodies++
	}
	if len(c.Multipart) > 0 {
		bodies++
	}
	if bodies > 1 {
		return fmt.Errorf("give one of Body, RawBody and Multipart")
	}
	for _, role := range append(append([]Role{}, c.Roles...), c.As) {
		if role != "" && roleIndex(role) < 0 {
			return fmt.Errorf("unknown role %q", role)
		}
	}
	for name, text := range c.Capture {
		if _, err := parseCaptureSource(text); err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
	}
	for name, text := range c.CaptureSecret {
		if !variableName.MatchString(name) {
			return fmt.Errorf("capture secret %q: a secret's name has letters, digits and _", name)
		}
		if _, both := c.Capture[name]; both {
			return fmt.Errorf("capture secret %s: the name is in Capture too", name)
		}
		if _, err := parseCaptureSource(text); err != nil {
			return fmt.Errorf("capture secret %s: %w", name, err)
		}
	}
	if _, err := parseCompute(c.Compute); err != nil {
		return err
	}
	byHand, _ := handMarks(c.Volatile)
	if _, err := newVolatileSet(byHand); err != nil {
		return fmt.Errorf("volatile: %w", err)
	}
	for _, id := range c.Covers {
		if method, pattern, ok := strings.Cut(id, " "); !ok || method == "" || !strings.HasPrefix(pattern, "/") {
			return fmt.Errorf("covers %q: a route ID is method and path, like \"POST /api/auth/logout\"", id)
		}
	}
	if c.Socket != nil {
		if c.Method != http.MethodGet || bodies > 0 || c.FollowRedirects || c.NoCSRF {
			return fmt.Errorf("a socket case is a GET without body, redirects or CSRF settings")
		}
		if err := c.Socket.check(); err != nil {
			return err
		}
	}
	return nil
}

// roles are the roles the case runs for, in the order of AllRoles.
func (c *Case) roles() []Role {
	if len(c.Roles) == 0 {
		return AllRoles
	}
	return sortRoles(c.Roles)
}

func (c *Case) runsFor(role Role) bool {
	for _, r := range c.roles() {
		if r == role {
			return true
		}
	}
	return false
}

// variables are the values captured so far in one role's turn.
type variables map[string]any

// fill replaces every {name} in text by the variable's value.
func (v variables) fill(text string) string {
	return variable.ReplaceAllStringFunc(text, func(match string) string {
		return v.text(match[1 : len(match)-1])
	})
}

// text is the value of a variable as it goes into text: MissingID for a
// variable nothing captured.
func (v variables) text(name string) string {
	value, ok := v[name]
	if !ok {
		return MissingID
	}
	switch value := value.(type) {
	case string:
		return value
	case json.Number:
		return string(value)
	}
	return string(encodeJSON(value))
}

// turn is what one role's turn of a group has collected: the variables
// the cases captured and computed, the secrets they captured, and for
// each argument of a two-factor function the time step of its last code.
type turn struct {
	vars    variables
	secrets map[string]string
	steps   map[string]int64
	// run is the run's secrets: the files of the secrets folder, and every
	// captured secret, for masking.
	run   *secretSet
	clock clock
	// sockets are the websockets the turn opened, by name.
	sockets map[string]*turnSocket
}

// clock is the time as the two-factor functions see it.
type clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

func newTurn(run *secretSet) *turn {
	return &turn{vars: variables{}, secrets: map[string]string{}, steps: map[string]int64{}, run: run, clock: realClock{}, sockets: map[string]*turnSocket{}}
}

// secret is the value of {secret:name}: captured in this turn, or the file
// of the secrets folder.
func (tn *turn) secret(name string) (string, error) {
	if value, ok := tn.secrets[name]; ok {
		return value, nil
	}
	return tn.run.file(name)
}

// fill replaces every {name} and {secret:name} in text by its value. It
// fails for a secret that has no value.
func (tn *turn) fill(text string) (string, error) {
	var failed error
	out := reference.ReplaceAllStringFunc(text, func(match string) string {
		m := reference.FindStringSubmatch(match)
		if m[1] == "" {
			return tn.vars.text(m[2])
		}
		value, err := tn.secret(m[2])
		if err != nil && failed == nil {
			failed = err
		}
		return value
	})
	return out, failed
}

// fillJSON replaces the variables and secrets in every string of a JSON
// value. A string that is one variable and nothing else becomes the
// variable's value, with its type; a secret is always text.
func (tn *turn) fillJSON(value any) (any, error) {
	switch value := value.(type) {
	case string:
		if m := variable.FindStringSubmatch(value); m != nil && m[0] == value {
			if captured, ok := tn.vars[m[1]]; ok {
				return captured, nil
			}
		}
		return tn.fill(value)
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			filled, err := tn.fillJSON(child)
			if err != nil {
				return nil, err
			}
			out[key] = filled
		}
		return out, nil
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			filled, err := tn.fillJSON(child)
			if err != nil {
				return nil, err
			}
			out[i] = filled
		}
		return out, nil
	}
	return value, nil
}

// multipartBoundary is fixed, so the same case always sends the same bytes.
const multipartBoundary = "bdcontract7d2c9a41e5b34f0c"

// request builds the request of the case with the variables and secrets
// filled in.
func (c *Case) request(tn *turn) (Request, error) {
	target, err := tn.fill(c.Path)
	if err != nil {
		return Request{}, err
	}
	if len(c.Query) > 0 {
		query := url.Values{}
		for key, values := range c.Query {
			for _, value := range values {
				filled, err := tn.fill(value)
				if err != nil {
					return Request{}, err
				}
				query.Add(key, filled)
			}
		}
		target += "?" + query.Encode()
	}
	r := Request{
		Method: c.Method, Path: target,
		NoCSRF: c.NoCSRF, NoSession: c.NoSession, NoCookies: c.NoCookies, FollowRedirects: c.FollowRedirects,
	}
	if len(c.Headers) > 0 {
		r.Header = http.Header{}
		for name, value := range c.Headers {
			filled, err := tn.fill(value)
			if err != nil {
				return r, err
			}
			r.Header.Set(name, filled)
		}
	}
	switch {
	case c.Body != nil:
		// Through JSON and back, so structs and typed maps are walked like
		// plain maps.
		plain, err := json.Marshal(c.Body)
		if err != nil {
			return r, fmt.Errorf("encode body: %w", err)
		}
		value, err := decodeJSON(plain)
		if err != nil {
			return r, fmt.Errorf("encode body: %w", err)
		}
		filled, err := tn.fillJSON(value)
		if err != nil {
			return r, err
		}
		r.Body, r.ContentType = encodeJSON(filled), "application/json"
	case c.RawBody != "":
		filled, err := tn.fill(c.RawBody)
		if err != nil {
			return r, err
		}
		r.Body, r.ContentType = []byte(filled), c.ContentType
	case len(c.Multipart) > 0:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if err := w.SetBoundary(multipartBoundary); err != nil {
			return r, fmt.Errorf("multipart: %w", err)
		}
		for _, part := range c.Multipart {
			header := textproto.MIMEHeader{}
			disposition := fmt.Sprintf(`form-data; name=%q`, part.Field)
			if part.FileName != "" {
				disposition += fmt.Sprintf(`; filename=%q`, part.FileName)
			}
			header.Set("Content-Disposition", disposition)
			if part.ContentType != "" {
				header.Set("Content-Type", part.ContentType)
			}
			pw, err := w.CreatePart(header)
			if err != nil {
				return r, fmt.Errorf("multipart: %w", err)
			}
			if _, err := pw.Write(part.Content); err != nil {
				return r, fmt.Errorf("multipart: %w", err)
			}
		}
		if err := w.Close(); err != nil {
			return r, fmt.Errorf("multipart: %w", err)
		}
		r.Body, r.ContentType = buf.Bytes(), w.FormDataContentType()
	}
	return r, nil
}

// info describes the case's request for the golden, with the variables
// still as {name}.
func (c *Case) info() RequestInfo {
	info := RequestInfo{Method: c.Method, Path: c.Path}
	if len(c.Query) > 0 {
		info.Path += "?" + c.Query.Encode()
	}
	switch {
	case c.Body != nil:
		info.Body = c.Body
	case c.RawBody != "":
		info.Body = c.RawBody
		info.Notes = append(info.Notes, "raw body, content type "+quote(c.ContentType))
	case len(c.Multipart) > 0:
		var fields []string
		for _, part := range c.Multipart {
			field := part.Field
			if part.FileName != "" {
				field += fmt.Sprintf(" (file %s, %d bytes)", part.FileName, len(part.Content))
			}
			fields = append(fields, field)
		}
		info.Notes = append(info.Notes, "multipart form: "+strings.Join(fields, ", "))
	}
	headers := make([]string, 0, len(c.Headers))
	for name, value := range c.Headers {
		headers = append(headers, name+": "+value)
	}
	sort.Strings(headers)
	for _, header := range headers {
		info.Notes = append(info.Notes, "header "+header)
	}
	if c.NoCSRF {
		info.Notes = append(info.Notes, "no CSRF token")
	}
	if c.NoSession {
		info.Notes = append(info.Notes, "no session cookie")
	}
	if c.NoCookies {
		info.Notes = append(info.Notes, "no cookies")
	}
	if c.FollowRedirects {
		info.Notes = append(info.Notes, "redirects followed")
	}
	if c.As != "" {
		info.Notes = append(info.Notes, fmt.Sprintf("sent as %s in every role's turn", c.As))
	}
	computed := make([]string, 0, len(c.Compute))
	for name, expression := range c.Compute {
		computed = append(computed, "computed: "+name+" = "+expression)
	}
	sort.Strings(computed)
	info.Notes = append(info.Notes, computed...)
	if c.Socket != nil {
		info.Notes = append(info.Notes, c.Socket.notes()...)
	}
	return info
}

// captureSource is where Capture and CaptureSecret take a value from: a
// header, a cookie or a path of the JSON answer.
type captureSource struct {
	header, cookie string
	path           path
}

func parseCaptureSource(text string) (captureSource, error) {
	if name, ok := strings.CutPrefix(text, "header:"); ok {
		if name == "" || strings.ContainsAny(name, " :") {
			return captureSource{}, fmt.Errorf("%q: give the header's name after header:", text)
		}
		return captureSource{header: name}, nil
	}
	if name, ok := strings.CutPrefix(text, "cookie:"); ok {
		if name == "" || strings.ContainsAny(name, " ;=") {
			return captureSource{}, fmt.Errorf("%q: give the cookie's name after cookie:", text)
		}
		return captureSource{cookie: name}, nil
	}
	p, err := parsePath(text)
	if err != nil {
		return captureSource{}, err
	}
	for _, s := range p {
		if s.kind == stepEvery || s.kind == stepLength {
			return captureSource{}, fmt.Errorf("%q must name one value, without [*] or [#]", text)
		}
	}
	return captureSource{path: p}, nil
}

// from takes the value out of an answer. body is the decoded JSON answer
// the paths are looked up in, nil when there is none. ok is false when the
// answer does not have the value.
func (src captureSource) from(answer Answer, body any, hasBody bool) (any, bool) {
	switch {
	case src.header != "":
		if answer.raw == nil {
			return nil, false
		}
		values := answer.raw.header.Values(src.header)
		if len(values) == 0 {
			return nil, false
		}
		return strings.Join(values, ", "), true
	case src.cookie != "":
		if answer.raw == nil {
			return nil, false
		}
		var value string
		found := false
		for _, cookie := range (&http.Response{Header: answer.raw.header}).Cookies() {
			if cookie.Name == src.cookie {
				value, found = cookie.Value, true
			}
		}
		return value, found
	case hasBody:
		return lookup(body, src.path)
	}
	return nil, false
}

// capture takes the case's Capture and CaptureSecret values out of an
// answer. Capture reads JSON as the answer holds it, with the instance's
// address replaced; CaptureSecret reads it as the console sent it. A
// captured secret is made known to the run, so masking finds it from now
// on. It fails for a secret value that is not text or too short.
func (c *Case) capture(answer Answer, tn *turn) error {
	if len(c.Capture) > 0 {
		body, hasBody := answerJSON(answer, false)
		for name, text := range c.Capture {
			src, err := parseCaptureSource(text)
			if err != nil {
				continue
			}
			if found, ok := src.from(answer, body, hasBody); ok {
				tn.vars[name] = found
			}
		}
	}
	if len(c.CaptureSecret) == 0 {
		return nil
	}
	body, hasBody := answerJSON(answer, true)
	names := make([]string, 0, len(c.CaptureSecret))
	for name := range c.CaptureSecret {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		src, err := parseCaptureSource(c.CaptureSecret[name])
		if err != nil {
			continue
		}
		found, ok := src.from(answer, body, hasBody)
		if !ok {
			continue
		}
		var value string
		switch v := found.(type) {
		case string:
			value = v
		case json.Number:
			value = string(v)
		default:
			return fmt.Errorf("capture secret %s: the value at %s is a %s, not text", name, c.CaptureSecret[name], jsonType(found))
		}
		if err := tn.run.add(name, value); err != nil {
			return fmt.Errorf("capture %w", err)
		}
		tn.secrets[name] = value
	}
	return nil
}

// answerJSON is the decoded JSON body of an answer: as the suite keeps it
// (with the instance's address and the known secrets masked), or as the
// console sent it when raw is set and the answer has its raw form.
func answerJSON(answer Answer, raw bool) (any, bool) {
	text := answer.Body.JSON
	if raw && answer.raw != nil {
		if answer.Body.Kind != BodyJSON && !isJSONType(mediaTypeOf(answer.raw.header.Get("Content-Type"))) {
			return nil, false
		}
		text = answer.raw.body
	} else if answer.Body.Kind != BodyJSON {
		return nil, false
	}
	value, err := decodeJSON(text)
	if err != nil {
		return nil, false
	}
	return value, true
}

// RunCases is the cases test: every registered group, each as its own
// subtest (TestCases/<group>/<role>/<case>).
func RunCases(t *testing.T) {
	s := suiteFor(t)
	s.requireRouting(t)
	groups := Groups()
	if len(groups) == 0 {
		t.Fatal("no case groups are registered")
	}
	keep := map[string]bool{}
	for _, group := range groups {
		t.Run(group.Name, func(t *testing.T) { s.runGroup(t, group) })
		for _, c := range group.Cases {
			keep[filepath.Clean(goldenFile(s.Settings.GoldenDir, "cases", group.Name, c.Name))] = true
		}
	}
	s.prune(t, filepath.Join(s.Settings.GoldenDir, "cases"), keep)
}

func (s *Suite) runGroup(t *testing.T, group *CaseGroup) {
	slots := make([]*slot, len(group.Cases))
	turns := map[Role]bool{}
	for i := range group.Cases {
		c := &group.Cases[i]
		sl := newSlot("cases", group.Name, c.Name, group.Name+"/"+c.Name, c.info())
		sl.method, sl.target, sl.upgrade = c.Method, variables{}.fill(c.Path), c.Socket != nil
		sl.rec.Volatile, sl.rec.VolatileHeaders = c.Volatile, c.VolatileHeaders
		slots[i] = sl
		for _, role := range c.roles() {
			turns[role] = true
		}
	}
	// Each browser is checked once per group: a group may end a session on
	// purpose, and the cases after that are meant to see it gone.
	browsers := map[Role]*Session{}
	browser := func(t *testing.T, role Role) *Session {
		if browsers[role] == nil {
			browsers[role] = s.session(t, role)
		}
		return browsers[role]
	}
	for _, role := range sortRoles(roleKeys(turns)) {
		t.Run(string(role), func(t *testing.T) {
			tn := newTurn(s.Client.secrets)
			defer tn.closeSockets()
			for i := range group.Cases {
				c := &group.Cases[i]
				if !c.runsFor(role) {
					continue
				}
				t.Run(c.Name, func(t *testing.T) {
					actor := role
					if c.As != "" {
						actor = c.As
					}
					sess := browser(t, actor)
					if err := c.compute(tn); err != nil {
						t.Fatalf("%s", s.redact(err.Error()))
					}
					request, err := c.request(tn)
					if err != nil {
						t.Fatalf("%s", s.redact(err.Error()))
					}
					id := group.Name + "/" + c.Name
					s.note(role, id)
					start := time.Now()
					var answer Answer
					if c.Socket != nil {
						answer = s.socketStep(sess, c, tn, request)
					} else {
						answer = sess.Do(request)
					}
					s.Report.took(fmt.Sprintf("%s as %s", id, role), time.Since(start))
					if err := c.capture(answer, tn); err != nil {
						t.Errorf("%s", s.redact(err.Error()))
					}
					if len(c.CaptureSecret) > 0 {
						// The answer may hold the secret it gave
						answer = s.Client.remask(answer)
					}
					s.judge(t, slots[i], role, answer)
					if answer.Failed != "" && !s.consoleAnswers(90*time.Second) {
						t.Fatalf("the console stopped answering. The last requests were: %s", strings.Join(s.recent, "; "))
					}
				})
			}
		})
	}
	s.flush(t, slots)
}
