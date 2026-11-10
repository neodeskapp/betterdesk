package contract

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Suite is the state of one run: the console it talks to, the browsers of
// the roles, the goldens and the report.
type Suite struct {
	Settings *Settings
	Client   *Client
	Report   *Report
	// Now is when the run started; recordings use it to spot values bound
	// to the day.
	Now time.Time

	sessions map[Role]*Session
	goldens  map[string]*Golden
	// recent holds the last requests, to name suspects when the console
	// stops answering.
	recent []string
}

var (
	suiteOnce sync.Once
	suite     *Suite
	suiteErr  error
)

// Main runs the tests of a test binary and prints the suite's summary after
// them. Call it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(contract.Main(m)) }
func Main(m *testing.M) int {
	code := m.Run()
	if suite != nil {
		suite.Report.Print(os.Stdout)
	}
	return code
}

// suiteFor returns the run's suite, set up from the environment on first
// use. A test that needs a console stops here when the settings are wrong.
func suiteFor(t *testing.T) *Suite {
	t.Helper()
	suiteOnce.Do(func() {
		settings, err := LoadSettings(os.Getenv)
		if err != nil {
			suiteErr = err
			return
		}
		client, err := NewClient(settings)
		if err != nil {
			suiteErr = err
			return
		}
		suite = &Suite{
			Settings: settings,
			Client:   client,
			Report:   newReport(settings),
			Now:      time.Now(),
			sessions: map[Role]*Session{},
			goldens:  map[string]*Golden{},
		}
		suite.Report.hasRouting = client.HasRouting()
	})
	if suiteErr != nil {
		t.Fatalf("contract suite: %v", suiteErr)
	}
	return suite
}

// slot is one golden in the making or under comparison: a matrix route or
// a case, with the answers of the roles of this run.
type slot struct {
	// layer is "matrix" or "cases"; group and name give the file.
	layer, group, name string
	rec                recording
	// method and target are what the routing report is asked about.
	method, target string
}

func (sl *slot) file(dir string) string {
	return goldenFile(dir, sl.layer, sl.group, sl.name)
}

func newSlot(layer, group, name, id string, request RequestInfo) *slot {
	return &slot{layer: layer, group: group, name: name, rec: recording{ID: id, Request: request, Answers: map[Role]Answer{}}}
}

// judge takes the answer a role got for a slot. Comparing, it fails the
// test with the differences from the golden. Recording, it keeps the answer
// for flush.
func (s *Suite) judge(t *testing.T, sl *slot, role Role, answer Answer) {
	t.Helper()
	tally := sl.layer + "/" + sl.group
	handler := HandlerUnknown
	if sl.method != "" {
		handler = s.Client.Handler(sl.method, sl.target)
		s.Report.answeredBy(sl.layer, sl.rec.ID, handler)
	}
	if answer.Failed != "" {
		s.Report.finding("%s as %s: no answer (%s)", sl.rec.ID, role, answer.Failed)
	}

	if s.Settings.Record != Compare {
		sl.rec.Answers[role] = answer
		s.Report.count(tally, true)
		return
	}

	var lines []string
	if s.Settings.RequireGo && handler != HandlerGo {
		switch handler {
		case HandlerUnknown:
			lines = append(lines, "who answered is not known: the console has no routing report (a Go console started with CONSOLE_DEBUG_ROUTING=1 has one)")
		default:
			lines = append(lines, fmt.Sprintf("answered by %s, not by the Go console", handler))
		}
	}
	file := sl.file(s.Settings.GoldenDir)
	golden, err := s.golden(file)
	switch {
	case err != nil && isNotExist(err):
		lines = append(lines, "there is no golden for it: record it (contract.sh record)")
	case err != nil:
		lines = append(lines, err.Error())
	default:
		want, ok := golden.For(role)
		if !ok {
			lines = append(lines, fmt.Sprintf("the golden has no answer for role %s: record it again", role))
			break
		}
		for _, diff := range CompareAnswers(want, answer) {
			lines = append(lines, diff.String())
		}
	}
	s.Report.count(tally, len(lines) == 0)
	if len(lines) > 0 {
		t.Errorf("%s", formatMismatch(sl.rec.ID, role, file, lines))
	}
}

// maxDiffLines is how many differences one answer prints.
const maxDiffLines = 30

// formatMismatch is the text of a failed comparison: what was asked, as
// whom, where the golden is, and each difference on its own line.
func formatMismatch(id string, role Role, file string, lines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s as %s differs from %s", id, role, filepath.ToSlash(file))
	for i, line := range lines {
		if i == maxDiffLines {
			fmt.Fprintf(&b, "\n    ... and %d more", len(lines)-i)
			break
		}
		b.WriteString("\n    " + line)
	}
	return b.String()
}

func (s *Suite) golden(file string) (*Golden, error) {
	if g, ok := s.goldens[file]; ok {
		return g, nil
	}
	g, err := LoadGolden(file)
	if err != nil {
		return nil, err
	}
	s.goldens[file] = g
	return g, nil
}

// flush writes the goldens of the slots when the run records. A first
// recording writes what it got. A merging recording marks what differs from
// the first as volatile; a difference that is not a volatile value fails
// the test and names the request.
func (s *Suite) flush(t *testing.T, slots []*slot) {
	t.Helper()
	if s.Settings.Record == Compare {
		return
	}
	for _, sl := range slots {
		if len(sl.rec.Answers) == 0 {
			continue
		}
		file := sl.file(s.Settings.GoldenDir)
		var golden *Golden
		switch s.Settings.Record {
		case RecordFirst:
			golden = sl.rec.build()
		case RecordMerge:
			first, err := LoadGolden(file)
			if err != nil {
				s.Report.problem("%s: no first recording to merge with (%v)", sl.rec.ID, err)
				t.Errorf("%s: no first recording to merge with: %v", sl.rec.ID, err)
				continue
			}
			var problems []string
			var timeBound int
			golden, problems, timeBound = sl.rec.merge(first, s.Now)
			s.Report.timeBound += timeBound
			if len(problems) > 0 {
				for _, problem := range problems {
					s.Report.problem("%s %s", sl.rec.ID, problem)
				}
				t.Errorf("%s answers differently on two fresh instances, and not just in a value:\n    %s",
					sl.rec.ID, strings.Join(problems, "\n    "))
			}
		}
		if err := golden.Save(file); err != nil {
			t.Errorf("%v", err)
			continue
		}
		s.Report.files++
		for _, answer := range golden.Answers {
			s.Report.volatile += len(answer.Volatile)
		}
	}
}

// session returns the browser of a role, signed in. A browser from earlier
// in the run is used again while the console still accepts its session.
func (s *Suite) session(t *testing.T, role Role) *Session {
	t.Helper()
	if sess, ok := s.sessions[role]; ok {
		if !sess.SignedIn() || sess.Alive() {
			return sess
		}
	}
	sess, _ := s.freshSession(t, role)
	return sess
}

// freshSession signs a role in again, replacing its browser, and returns
// the browser and the answer of the sign-in request (nil for SignedOut).
func (s *Suite) freshSession(t *testing.T, role Role) (*Session, *Answer) {
	t.Helper()
	sess := s.Client.NewSession(role)
	answer, err := sess.SignIn()
	if err != nil {
		t.Fatalf("%v", err)
	}
	s.sessions[role] = sess
	return sess, answer
}

// note remembers a request that is about to be sent.
func (s *Suite) note(role Role, id string) {
	s.recent = append(s.recent, fmt.Sprintf("%s as %s", id, role))
	if len(s.recent) > 8 {
		s.recent = s.recent[len(s.recent)-8:]
	}
}

// watch looks after the console and the session once a request is done.
// When the request got no answer, it waits for the console to answer again
// and stops the run if it does not. When the request ended the role's
// session, it reports that and signs in again, so the requests after it are
// sent as the role they are recorded for. checkSession asks the console
// even when the answer does not look like a lost session.
func (s *Suite) watch(t *testing.T, sess *Session, id string, answer Answer, checkSession bool) {
	t.Helper()
	if answer.Failed != "" && !s.consoleAnswers(90*time.Second) {
		t.Fatalf("the console stopped answering. The last requests were: %s", strings.Join(s.recent, "; "))
	}
	if !sess.SignedIn() {
		return
	}
	lost := sess.cookie(sessionCookies) == ""
	if !lost && (checkSession || answer.Status == http.StatusUnauthorized || answer.Failed != "") {
		lost = !sess.Alive()
	}
	if !lost {
		return
	}
	s.Report.finding("%s as %s ended the session (or a request shortly before it did): it must not be sent with placeholders", id, sess.Role)
	t.Errorf("%s as %s: the session is gone after this request", id, sess.Role)
	if _, err := sess.SignIn(); err != nil {
		t.Fatalf("sign in again as %s: %v", sess.Role, err)
	}
}

// consoleAnswers waits until the console answers /health, at most limit.
func (s *Suite) consoleAnswers(limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		if _, status, err := s.Client.fetchJSON(nil, "/health"); err == nil && status == http.StatusOK {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}
