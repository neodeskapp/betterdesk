package remote

import (
	"bytes"
	_ "embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// mockSession is the dev mock of the viewer (lib/devMockRemote/mock-session.js
// of the old console): it stands in for the remote client in the browser
// and draws a made-up desktop, so the viewer can be reviewed without a
// device. It is served only with BD_DEV_MOCK_REMOTE=1.
//
//go:embed mock-session.js
var mockSession []byte

// mockScenarios are the scenarios the mock knows (SCENARIOS).
var mockScenarios = []string{"ok", "slow", "password", "2fa", "fail-relay", "refused", "offline", "timeout", "ended", "dropped"}

// seededDevice is a fake device of the dev seed scripts: IDs 900100001 to
// 900100099 and from 950000000 (isSeededDevice).
var seededDevice = regexp.MustCompile(`^(9001000\d\d|950\d{6})$`)

// mockScript serves the mock as res.sendFile served the file: with the
// file's own ETag and Last-Modified, conditional requests and ranges.
type mockScript struct {
	files *httpx.Static
}

// mockFile is the name the script is served under.
const mockFile = "mock-session.js"

func newMockScript(modified time.Time) *mockScript {
	return &mockScript{files: httpx.NewStatic("/", httpx.StaticOptions{
		NoIndex: true, NoRedirect: true, NoFallthrough: true,
		FS: embeddedFile{data: mockSession, modified: modified},
	})}
}

// mockScript answers GET /dev/mock-remote.js: the mock with no-store and
// the JavaScript type when it is on, else the request goes on as if this
// route were not there.
func (h *handlers) mockScript(w http.ResponseWriter, r *http.Request) {
	if h.src.mock == nil {
		kit.PassOn(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	h.src.mock.files.ServeName(w, r, mockFile, http.NotFoundHandler())
}

// mockFor is devMockRemote.forRequest: the mock's settings for a viewer
// request, nil when the mock is off. With the mock on, ?mock=1 asks for
// it and a seeded demo device gets it unless ?mock=0; ?scenario= picks a
// known scenario in any letter case, else "ok". bad names a query value
// that is no text.
func (h *handlers) mockFor(r *http.Request, deviceID string) (settings any, bad string) {
	if h.src.mock == nil {
		return nil, ""
	}
	q := reqquery.Of(r)
	mock, ok := queryText(q, "mock", "")
	if !ok {
		return nil, "mock"
	}
	if mock != "1" && (mock == "0" || !seededDevice.MatchString(deviceID)) {
		return nil, ""
	}
	wanted, ok := queryText(q, "scenario", "ok")
	if !ok {
		return nil, "scenario"
	}
	wanted = jsbind.ToLower(wanted)
	for _, scenario := range mockScenarios {
		if scenario == wanted {
			return mockSettings{Scenario: wanted}, ""
		}
	}
	return mockSettings{Scenario: "ok"}, ""
}

type mockSettings struct {
	Scenario string `json:"scenario"`
}

// queryText is String(req.query[name] || fallback). ok is false where
// JavaScript throws: a value that is an object with a member toString.
func queryText(q *reqquery.Query, name, fallback string) (string, bool) {
	value := q.Get(name)
	if !value.Truthy() {
		return fallback, true
	}
	return value.Text()
}

// embeddedFile is a file server's view of one file held in memory.
type embeddedFile struct {
	data     []byte
	modified time.Time
}

func (f embeddedFile) Stat(name string) (fs.FileInfo, error) {
	if path.Base(filepath.ToSlash(name)) != mockFile {
		return nil, fs.ErrNotExist
	}
	return fileInfo(f), nil
}

func (f embeddedFile) Open(name string) (io.ReadSeekCloser, error) {
	if path.Base(filepath.ToSlash(name)) != mockFile {
		return nil, fs.ErrNotExist
	}
	return nopCloser{bytes.NewReader(f.data)}, nil
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

type fileInfo embeddedFile

func (i fileInfo) Name() string       { return mockFile }
func (i fileInfo) Size() int64        { return int64(len(i.data)) }
func (i fileInfo) Mode() fs.FileMode  { return 0o444 }
func (i fileInfo) ModTime() time.Time { return i.modified }
func (i fileInfo) IsDir() bool        { return false }
func (i fileInfo) Sys() any           { return nil }
