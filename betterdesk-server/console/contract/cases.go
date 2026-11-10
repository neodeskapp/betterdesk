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
	Path string
	// Query is added to the path. Values may hold {name} too.
	Query url.Values
	// Headers are added to the request and replace the suite's own
	// (Accept, Accept-Language, Accept-Encoding, User-Agent) by name.
	Headers map[string]string

	// Body is sent as JSON. Strings in it may hold {name}; a string that is
	// only "{name}" is replaced by the captured value with its type, so an
	// ID that came as a number is sent as a number.
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

	// Capture takes values out of the JSON answer into variables for the
	// cases after it: variable name to path ("ticket": "data.id"). A path
	// that is not in the answer leaves the variable as it was.
	Capture map[string]string
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
	groupName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	caseName  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	variable  = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
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
		p, err := parsePath(text)
		if err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
		for _, s := range p {
			if s.kind == stepEvery || s.kind == stepLength {
				return fmt.Errorf("capture %s: %q must name one value, without [*] or [#]", name, text)
			}
		}
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
		value, ok := v[match[1:len(match)-1]]
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
	})
}

// fillJSON replaces the variables in every string of a JSON value. A string
// that is one variable and nothing else becomes the variable's value, with
// its type.
func (v variables) fillJSON(value any) any {
	switch value := value.(type) {
	case string:
		if m := variable.FindStringSubmatch(value); m != nil && m[0] == value {
			if captured, ok := v[m[1]]; ok {
				return captured
			}
		}
		return v.fill(value)
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			out[key] = v.fillJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = v.fillJSON(child)
		}
		return out
	}
	return value
}

// multipartBoundary is fixed, so the same case always sends the same bytes.
const multipartBoundary = "bdcontract7d2c9a41e5b34f0c"

// request builds the request of the case with the variables filled in.
func (c *Case) request(vars variables) (Request, error) {
	target := vars.fill(c.Path)
	if len(c.Query) > 0 {
		query := url.Values{}
		for key, values := range c.Query {
			for _, value := range values {
				query.Add(key, vars.fill(value))
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
			r.Header.Set(name, vars.fill(value))
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
		r.Body, r.ContentType = encodeJSON(vars.fillJSON(value)), "application/json"
	case c.RawBody != "":
		r.Body, r.ContentType = []byte(vars.fill(c.RawBody)), c.ContentType
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
	return info
}

// capture takes the case's Capture values out of an answer.
func (c *Case) capture(answer Answer, vars variables) {
	if len(c.Capture) == 0 || answer.Body.Kind != BodyJSON {
		return
	}
	value, err := decodeJSON(answer.Body.JSON)
	if err != nil {
		return
	}
	for name, text := range c.Capture {
		p, err := parsePath(text)
		if err != nil {
			continue
		}
		if found, ok := lookup(value, p); ok {
			vars[name] = found
		}
	}
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
		sl.method, sl.target = c.Method, variables{}.fill(c.Path)
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
			vars := variables{}
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
					request, err := c.request(vars)
					if err != nil {
						t.Fatalf("%v", err)
					}
					id := group.Name + "/" + c.Name
					s.note(role, id)
					start := time.Now()
					answer := sess.Do(request)
					s.Report.took(fmt.Sprintf("%s as %s", id, role), time.Since(start))
					c.capture(answer, vars)
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
