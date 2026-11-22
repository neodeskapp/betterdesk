package contract

import (
	"fmt"
	"io/fs"
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
		suite.Report.redact = client.secrets.mask
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
	// method and target are what the routing report is asked about;
	// upgrade asks about a websocket upgrade to the target.
	method, target string
	upgrade        bool
}

func (sl *slot) file(dir string) string {
	return goldenFile(dir, sl.layer, sl.group, sl.name)
}

// goldenName is the golden's name under the golden folder without ".json",
// as CONTRACT_REWRITE matches it: "cases/devices/list".
func (sl *slot) goldenName() string {
	return sl.layer + "/" + safeName(sl.group) + "/" + safeName(sl.name)
}

func newSlot(layer, group, name, id string, request RequestInfo) *slot {
	return &slot{layer: layer, group: group, name: name, rec: recording{ID: id, Request: request, Answers: map[Role]Answer{}}}
}

// judge takes the answer a role got for a slot. Comparing, it fails the
// test with the differences from the golden. Recording, it keeps the answer
// for flush when the Node console gave it (see recordable).
func (s *Suite) judge(t *testing.T, sl *slot, role Role, answer Answer) {
	t.Helper()
	tally := sl.layer + "/" + sl.group
	handler := s.handler("", "", false)
	if sl.method != "" {
		handler = s.handler(sl.method, sl.target, sl.upgrade)
		s.Report.answeredBy(sl.layer, sl.rec.ID, handler)
	}
	if answer.Failed != "" {
		s.Report.finding("%s as %s: no answer (%s)", sl.rec.ID, role, answer.Failed)
	}

	if s.Settings.Record != Compare {
		record, problem := recordable(handler)
		if !record && problem == "" && s.Settings.RecordNew && s.isNewGolden(sl) {
			record = true
			s.Report.recordedNew[sl.file(s.Settings.GoldenDir)] = true
		}
		if !record && handler == HandlerGo && s.Settings.Rewrite != nil && s.Settings.Rewrite.MatchString(sl.goldenName()) {
			record = true
			s.Report.rewritten[sl.file(s.Settings.GoldenDir)] = true
		}
		switch {
		case record:
			sl.rec.Answers[role] = answer
			s.Report.count(tally, true)
		case problem == "":
			s.Report.froze(sl.file(s.Settings.GoldenDir))
		default:
			s.Report.count(tally, false)
			t.Errorf("%s as %s is not recorded: %s", sl.rec.ID, role, problem)
		}
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
		want = withHandMarks(want, sl.rec.Volatile, sl.rec.VolatileHeaders)
		for _, diff := range CompareAnswers(want, answer) {
			lines = append(lines, diff.String())
		}
	}
	s.Report.count(tally, len(lines) == 0)
	if len(lines) > 0 {
		t.Errorf("%s", s.redact(formatMismatch(sl.rec.ID, role, file, lines)))
	}
}

// redact puts the placeholders of the run's secrets in place of their
// values in a message.
func (s *Suite) redact(text string) string {
	return s.Client.secrets.mask(text)
}

// isNewGolden reports whether CONTRACT_RECORD_NEW may record a slot the Go
// console answers: its golden does not exist yet, or holds the first of
// the two recordings only, which is what the first half of the same
// recording left. A golden that holds both recordings stays as it is.
func (s *Suite) isNewGolden(sl *slot) bool {
	golden, err := LoadGolden(sl.file(s.Settings.GoldenDir))
	if err != nil {
		return isNotExist(err)
	}
	return golden.Recordings < 2
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

// recordable says what a recording does with an answer, by who gave it.
//
// Only an answer of the Node console is recorded: a spec recording of a
// tree that still has it (CONTRACT_NODE_ONLY, contract.sh spec). A request
// the Go console answers has a frozen golden: it was recorded from the
// Node console before the route moved and is the specification, so the
// file stays as it is (record false, no problem); a fix rewrites the
// goldens it names with CONTRACT_REWRITE. The request is still sent, because later
// steps of a case group may need what it creates. An answer nobody stands
// for, or one the routing report says nothing about, is a problem and is
// not written either.
func recordable(handler string) (record bool, problem string) {
	switch handler {
	case HandlerNode:
		return true, ""
	case HandlerGo:
		return false, ""
	case HandlerNone:
		return false, "nobody answers it. Goldens come from the Node console of a spec tree, or from Go with CONTRACT_REWRITE"
	}
	return false, "the routing report does not say who answers it, so its golden may be frozen and stays as it is"
}

// recordingBlocked says why the run must not record, "" when it may. A
// recording needs the routing report: without it the suite cannot tell the
// frozen goldens from the others and would overwrite them.
func (s *Suite) recordingBlocked() string {
	if s.Settings.Record == Compare || s.Settings.NodeOnly || s.Client.HasRouting() {
		return ""
	}
	return "recording needs the Go console's routing report (CONSOLE_DEBUG_ROUTING=1): " +
		"it says which routes the Go console answers, and their goldens are frozen"
}

// handler is who answers a request: the routing report's word, or the Node
// console for every request when the console is Node alone (NodeOnly). An
// empty method asks about a slot that names no request.
func (s *Suite) handler(method, target string, upgrade bool) string {
	if s.Settings.NodeOnly {
		return HandlerNode
	}
	if method == "" {
		return HandlerUnknown
	}
	return s.Client.handlerOf(method, target, upgrade)
}

// requireRouting stops a run that must not record.
func (s *Suite) requireRouting(t *testing.T) {
	t.Helper()
	if problem := s.recordingBlocked(); problem != "" {
		t.Fatalf("%s", problem)
	}
}

// flush writes the goldens of the slots when the run records. A first
// recording writes what it got. A merging recording marks what differs from
// the first as volatile; a difference that is not a volatile value fails
// the test and names the request. A slot without recorded answers, as a
// frozen one, is neither read nor written.
func (s *Suite) flush(t testing.TB, slots []*slot) {
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
		data, err := golden.encode()
		if err != nil {
			t.Errorf("encode golden %s: %v", file, err)
			continue
		}
		if names := s.Client.secrets.foundIn(data); len(names) > 0 {
			s.Report.problem("%s: not written, the golden would hold the value of the secret %s", sl.rec.ID, strings.Join(names, ", "))
			t.Errorf("%s is not recorded: the golden would hold the value of the secret %s (the masking did not reach it); %s is left as it was",
				sl.rec.ID, strings.Join(names, ", "), filepath.ToSlash(file))
			continue
		}
		if err := writeGolden(file, data); err != nil {
			t.Errorf("%v", err)
			continue
		}
		s.Report.files++
		for _, answer := range golden.Answers {
			s.Report.volatile += len(answer.Volatile)
		}
	}
}

// prune ends a first recording of a layer: it removes the golden files
// below dir that no route or case of the suite stands for any more, and the
// folders that leaves empty. keep holds the files of the routes and cases
// that exist. A leftover golden stays when the Node console does not answer
// its request: the Go console does, so it is frozen like the others.
func (s *Suite) prune(t *testing.T, dir string, keep map[string]bool) {
	t.Helper()
	if s.Settings.Record != RecordFirst {
		return
	}
	var folders []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil && isNotExist(err):
			return nil
		case err != nil:
			return err
		case entry.IsDir():
			if path != dir {
				folders = append(folders, path)
			}
			return nil
		case !strings.HasSuffix(path, ".json") || keep[filepath.Clean(path)]:
			return nil
		}
		golden, err := LoadGolden(path)
		if err != nil {
			return err
		}
		target, _, _ := strings.Cut(variables{}.fill(golden.Request.Path), "?")
		if s.handler(golden.Request.Method, target, false) != HandlerNode {
			s.Report.froze(path)
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		s.Report.removed++
		return nil
	})
	if err != nil {
		t.Errorf("tidy the goldens below %s: %v", filepath.ToSlash(dir), err)
	}
	for i := len(folders) - 1; i >= 0; i-- {
		// Remove fails on a folder that still holds a file, and that is the point
		_ = os.Remove(folders[i])
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
