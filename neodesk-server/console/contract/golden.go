package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Golden is one golden file: what a console answers to one request, for
// each role that sent it.
type Golden struct {
	// ID is the matrix route ("GET /api/devices/:id") or the case
	// ("devices/list").
	ID string `json:"id"`
	// Request describes what was sent, for the reader. It is not compared.
	Request RequestInfo `json:"request"`
	// Recordings counts the recordings the golden was made from: 1 after a
	// first recording, which holds every value as it came, 2 once a second
	// recording was merged in and the volatile values are marked and masked.
	Recordings int `json:"recordings"`
	// Answers holds one entry per distinct answer, with the roles that got it.
	Answers []GoldenAnswer `json:"answers"`
}

// RequestInfo describes the request of a golden for the reader.
type RequestInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Body is the JSON body, or a description of another kind of body.
	Body any `json:"body,omitempty"`
	// Notes say what is special about the request: no CSRF token, no
	// session cookie, sent as one fixed role.
	Notes []string `json:"notes,omitempty"`
}

// GoldenAnswer is an answer and the roles that got it.
type GoldenAnswer struct {
	Roles     []Role              `json:"roles"`
	Status    int                 `json:"status"`
	Failed    string              `json:"failed,omitempty"`
	Redirects []string            `json:"redirects,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	// VolatileHeaders are headers that only have to be present.
	VolatileHeaders []string `json:"volatileHeaders,omitempty"`
	// Volatile are the paths of the body whose values change from run to
	// run: there the type is compared and the format the mark holds.
	Volatile []VolatileMark `json:"volatile,omitempty"`
	Body     Body           `json:"body"`
}

// Answer is the answer part of a golden entry.
func (g GoldenAnswer) Answer() Answer {
	return Answer{Status: g.Status, Failed: g.Failed, Redirects: g.Redirects, Headers: g.Headers, Body: g.Body}
}

func newGoldenAnswer(a Answer, volatile []VolatileMark, volatileHeaders []string) GoldenAnswer {
	return GoldenAnswer{
		Status: a.Status, Failed: a.Failed, Redirects: a.Redirects, Headers: a.Headers, Body: a.Body,
		Volatile: volatile, VolatileHeaders: volatileHeaders,
	}
}

// For returns the answer a role is expected to get.
func (g *Golden) For(role Role) (GoldenAnswer, bool) {
	for _, answer := range g.Answers {
		for _, r := range answer.Roles {
			if r == role {
				return answer, true
			}
		}
	}
	return GoldenAnswer{}, false
}

// LoadGolden reads a golden file. A file that does not exist gives an error
// that matches fs.ErrNotExist.
func LoadGolden(file string) (*Golden, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var g Golden
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("read golden %s: %w", file, err)
	}
	return &g, nil
}

// Save writes the golden file, creating its folder. The same golden always
// gives the same bytes.
func (g *Golden) Save(file string) error {
	data, err := g.encode()
	if err != nil {
		return fmt.Errorf("encode golden %s: %w", file, err)
	}
	return writeGolden(file, data)
}

// encode is the golden as its file holds it.
func (g *Golden) encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeGolden(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("create folder for golden: %w", err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return fmt.Errorf("write golden: %w", err)
	}
	return nil
}

// recording is what one run got for one golden: the answer of each role
// that sent the request.
type recording struct {
	ID      string
	Request RequestInfo
	Answers map[Role]Answer
	// Volatile and VolatileHeaders are marks set by hand, on a case or in
	// the matrix tables. A path that starts with "~" is free in format too.
	Volatile        []string
	VolatileHeaders []string
}

// FreeForm starts a path set by hand whose values are free in format as
// well: only their type is checked. It is for values no two recordings pin
// down, like a duration that is "51.3s" now and "1m3.2s" a little later.
const FreeForm = "~"

// AnyOrder starts a path set by hand of an array whose elements come in
// no fixed order, like the permissions of a role. The array is not
// volatile: it must hold the same elements as the golden, and both are
// sorted before they are compared. The golden keeps the elements sorted.
const AnyOrder = "@"

// Optional starts a path set by hand of a key that some objects have and
// others do not, like the details of only some audit events in a list
// whose elements are compared with one sample. The key may be missing on
// either side; where it is there, its value is volatile.
const Optional = "?"

// handMarks splits the marks set by hand into their paths and the set of
// those that are free in format.
func handMarks(marks []string) (paths []string, free map[string]bool) {
	free = map[string]bool{}
	for _, mark := range marks {
		text := strings.TrimPrefix(mark, FreeForm)
		if text != mark {
			free[text] = true
		}
		paths = append(paths, text)
	}
	return sortedCopy(paths), free
}

// build turns a first recording into a golden: roles with the same answer
// share an entry. The marks set by hand are on every answer, without
// formats; those come with the second recording.
func (rec *recording) build() *Golden {
	perRole := map[Role]GoldenAnswer{}
	byHand, _ := handMarks(rec.Volatile)
	for role, answer := range rec.Answers {
		perRole[role] = newGoldenAnswer(answer, plainMarks(byHand), sortedCopy(rec.VolatileHeaders))
	}
	return &Golden{ID: rec.ID, Request: rec.Request, Recordings: 1, Answers: groupByAnswer(perRole)}
}

// merge turns a second recording and the golden of the first into the final
// golden. What differs in value between the two is marked volatile, gets
// the format the recordings share (deriveMark) and is masked; what must
// not differ is returned as problems, each starting with the role. A role
// that only one of the two recordings has is a problem too.
//
// The format of a path is worked out from every role that got the same
// status, in both recordings. Merging into a golden that already went
// through a merge keeps its marks as they are: its volatile values are
// masked, so there is nothing to derive a format from.
func (rec *recording) merge(first *Golden, now time.Time) (golden *Golden, problems []string, timeBound int) {
	perRole := map[Role]GoldenAnswer{}
	for _, answer := range first.Answers {
		for _, role := range answer.Roles {
			perRole[role] = answer
		}
	}
	for _, role := range sortRoles(roleKeys(perRole)) {
		if _, again := rec.Answers[role]; !again {
			problems = append(problems, fmt.Sprintf("[%s] the second recording has no answer for this role", role))
		}
	}

	// What each role's two recordings say: the volatile paths, and the
	// bodies as samples for the formats.
	type pair struct {
		before       GoldenAnswer
		volatile     []string
		knownHeaders []string
	}
	pairs := map[Role]pair{}
	samples := map[int][]any{}
	byHand, free := handMarks(rec.Volatile)
	roles := sortRoles(roleKeys(rec.Answers))
	for _, role := range roles {
		second := rec.Answers[role]
		before, ok := perRole[role]
		if !ok {
			problems = append(problems, fmt.Sprintf("[%s] the first recording has no answer for this role", role))
			continue
		}
		known := union(markPaths(before.Volatile), byHand)
		knownHeaders := union(before.VolatileHeaders, rec.VolatileHeaders)
		found := detectVolatile(before.Answer(), second, known, knownHeaders, now)
		for _, problem := range found.Problems {
			problems = append(problems, fmt.Sprintf("[%s] %s", role, problem))
		}
		timeBound += found.TimeBound
		pairs[role] = pair{before: before, volatile: union(known, found.Volatile), knownHeaders: knownHeaders}
		if first.Recordings < 2 && before.Status == second.Status {
			for _, body := range []Body{before.Body, second.Body} {
				if body.Kind != BodyJSON {
					continue
				}
				if value, err := decodeJSON(body.JSON); err == nil {
					samples[before.Status] = append(samples[before.Status], value)
				}
			}
		}
	}

	derived := map[string]VolatileMark{}
	for _, role := range roles {
		p, ok := pairs[role]
		if !ok {
			continue
		}
		settled := map[string]VolatileMark{}
		if first.Recordings >= 2 {
			for _, mark := range p.before.Volatile {
				settled[mark.text()] = mark
			}
		}
		marks := make([]VolatileMark, len(p.volatile))
		for i, text := range p.volatile {
			mark, isSettled := settled[text]
			if strings.HasPrefix(text, AnyOrder) || strings.HasPrefix(text, Optional) {
				mark = plainMarks([]string{text})[0]
			} else if free[text] {
				mark = VolatileMark{Path: text}
			} else if !isSettled {
				key := fmt.Sprintf("%d %s", p.before.Status, text)
				if _, done := derived[key]; !done {
					derived[key] = deriveMark(text, samples[p.before.Status]...)
				}
				mark = derived[key]
				// The golden keeps the first element as the sample
				mark.Elements = strings.HasSuffix(text, "[#]")
			}
			marks[i] = mark
		}
		if len(marks) == 0 {
			marks = nil
		}
		masked, err := maskVolatile(p.before.Answer(), p.volatile)
		if err != nil {
			problems = append(problems, fmt.Sprintf("[%s] %v", role, err))
			continue
		}
		perRole[role] = newGoldenAnswer(masked, marks, p.knownHeaders)
	}
	recordings := first.Recordings + 1
	if recordings < 2 {
		recordings = 2
	}
	return &Golden{ID: rec.ID, Request: rec.Request, Recordings: recordings, Answers: groupByAnswer(perRole)}, problems, timeBound
}

// groupByAnswer puts roles with the same answer and the same volatile marks
// into one entry. Entries are in the order of their first role.
func groupByAnswer(perRole map[Role]GoldenAnswer) []GoldenAnswer {
	var groups []GoldenAnswer
	index := map[string]int{}
	for _, role := range sortRoles(roleKeys(perRole)) {
		answer := perRole[role]
		answer.Roles = nil
		key, err := json.Marshal(answer)
		if err != nil {
			panic("contract: encode answer: " + err.Error())
		}
		i, seen := index[string(key)]
		if !seen {
			i = len(groups)
			index[string(key)] = i
			groups = append(groups, answer)
		}
		groups[i].Roles = append(groups[i].Roles, role)
	}
	return groups
}

func roleKeys[V any](m map[Role]V) []Role {
	roles := make([]Role, 0, len(m))
	for role := range m {
		roles = append(roles, role)
	}
	return roles
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortedCopy(list []string) []string {
	return union(list, nil)
}

// goldenFile is the file of a golden below dir: layer is "matrix" or
// "cases", name the file name without ".json".
func goldenFile(dir, layer, group, name string) string {
	return filepath.Join(dir, layer, safeName(group), safeName(name)+".json")
}

// safeName makes a string usable as a file name on every system: letters,
// digits, dot, dash and underscore stay, everything else becomes a dash.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		return "_"
	}
	return name
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// goldenAnswerFile is how a GoldenAnswer is written in a golden file: the
// same fields, with a header that has one value written as a string and
// not as a list of one.
type goldenAnswerFile struct {
	Roles           []Role                  `json:"roles"`
	Status          int                     `json:"status"`
	Failed          string                  `json:"failed,omitempty"`
	Redirects       []string                `json:"redirects,omitempty"`
	Headers         map[string]headerValues `json:"headers,omitempty"`
	VolatileHeaders []string                `json:"volatileHeaders,omitempty"`
	Volatile        []VolatileMark          `json:"volatile,omitempty"`
	Body            Body                    `json:"body"`
}

type headerValues []string

func (h headerValues) MarshalJSON() ([]byte, error) {
	if len(h) == 1 {
		return marshalPlain(h[0])
	}
	return marshalPlain([]string(h))
}

func (h *headerValues) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var one string
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		*h = headerValues{one}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("header values: %w", err)
	}
	*h = list
	return nil
}

// MarshalJSON writes the answer as a golden file holds it.
func (g GoldenAnswer) MarshalJSON() ([]byte, error) {
	file := goldenAnswerFile{
		Roles: g.Roles, Status: g.Status, Failed: g.Failed, Redirects: g.Redirects,
		VolatileHeaders: g.VolatileHeaders, Volatile: g.Volatile, Body: g.Body,
	}
	if len(g.Headers) > 0 {
		file.Headers = make(map[string]headerValues, len(g.Headers))
		for name, values := range g.Headers {
			file.Headers[name] = values
		}
	}
	return marshalPlain(file)
}

// UnmarshalJSON reads an answer from a golden file and refuses fields it
// does not know, so a misspelt "volatile" does not go unnoticed.
func (g *GoldenAnswer) UnmarshalJSON(data []byte) error {
	var file goldenAnswerFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return err
	}
	*g = GoldenAnswer{
		Roles: file.Roles, Status: file.Status, Failed: file.Failed, Redirects: file.Redirects,
		VolatileHeaders: file.VolatileHeaders, Volatile: file.Volatile, Body: file.Body,
	}
	if len(file.Headers) > 0 {
		g.Headers = make(map[string][]string, len(file.Headers))
		for name, values := range file.Headers {
			g.Headers[name] = values
		}
	}
	return nil
}

// marshalPlain encodes a value as JSON without escaping <, > and &, which
// json.Marshal does and which makes formats and HTML hard to read.
func marshalPlain(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// withHandMarks adds the marks set by hand in the code to those a golden
// holds, so a mark added after a golden was recorded counts without
// recording again. A mark that is free in format takes the format off a
// recorded mark for the same path.
func withHandMarks(want GoldenAnswer, volatile, volatileHeaders []string) GoldenAnswer {
	paths, free := handMarks(volatile)
	if len(paths) > 0 {
		marks := append([]VolatileMark(nil), want.Volatile...)
		known := map[string]int{}
		for i, mark := range marks {
			known[mark.text()] = i
		}
		for _, p := range paths {
			i, ok := known[p]
			switch {
			case !ok:
				marks = append(marks, plainMarks([]string{p})[0])
			case free[p]:
				marks[i] = VolatileMark{Path: p}
			}
		}
		want.Volatile = marks
	}
	if len(volatileHeaders) > 0 {
		headers := append([]string(nil), want.VolatileHeaders...)
		for _, name := range volatileHeaders {
			if !containsFold(headers, name) {
				headers = append(headers, name)
			}
		}
		want.VolatileHeaders = headers
	}
	return want
}

func containsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}
