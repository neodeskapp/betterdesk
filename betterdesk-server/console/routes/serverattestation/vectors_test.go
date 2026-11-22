package serverattestation

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/attestation"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// The records of testdata/routes.json.gz: what the Node routes
// answered for stored settings, bodies, brand names and translations.

type answer struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

type routeVectors struct {
	Translations map[string]map[string]string `json:"translations"`
	Put          []struct {
		Body   string          `json:"body"`
		Brand  json.RawMessage `json:"brand"`
		Texts  string          `json:"texts"`
		Mode   string          `json:"mode"`
		Put    answer          `json:"put"`
		Writes [][2]string     `json:"writes"`
		Logs   [][3]any        `json:"logs"`
		Get    answer          `json:"get"`
	} `json:"put"`
	Get []struct {
		Config  *string         `json:"config"`
		Failing bool            `json:"failing"`
		Brand   json.RawMessage `json:"brand"`
		Texts   string          `json:"texts"`
		First   answer          `json:"first"`
		Second  answer          `json:"second"`
	} `json:"get"`
	Public []struct {
		Result  *string         `json:"result"`
		Config  *string         `json:"config"`
		Failing string          `json:"failing"`
		Brand   json.RawMessage `json:"brand"`
		Texts   string          `json:"texts"`
		Answer  answer          `json:"answer"`
	} `json:"public"`
	Status []struct {
		Result       *string `json:"result"`
		Failing      bool    `json:"failing"`
		Status       answer  `json:"status"`
		ResultAnswer answer  `json:"result_answer"`
	} `json:"status"`
	Abort []answer `json:"abort"`
}

// fakeSettings is a settings table in memory with failures on demand.
type fakeSettings struct {
	rows      map[string]*string
	failing   map[string]bool
	failWrite bool
	writes    [][2]string
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{rows: map[string]*string{}, failing: map[string]bool{}}
}

func (f *fakeSettings) Get(_ context.Context, key string) (string, bool, error) {
	if f.failing[key] {
		return "", false, errors.New("database is locked")
	}
	if v := f.rows[key]; v != nil {
		return *v, true, nil
	}
	return "", false, nil
}

func (f *fakeSettings) Set(_ context.Context, key, value string) error {
	if f.failWrite {
		return errors.New("SQLITE_READONLY: attempt to write a readonly database")
	}
	f.writes = append(f.writes, [2]string{key, value})
	f.rows[key] = &value
	return nil
}

// brandOf reads a recorded brand name: a JSON value, or {"undefined":true}
// for a branding without one.
func brandOf(t *testing.T, raw json.RawMessage) any {
	v, err := branding.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if branding.Field(v, "undefined") == true {
		return branding.Undefined
	}
	return v
}

type logged struct {
	action, details string
}

// testHandlers are the group's handlers on settings in memory, with a
// fixed brand and translations and an audit log that may fail.
func testHandlers(settings attestation.Settings, brand any, texts map[string]string, failLog bool, logs *[]logged) *handlers {
	return &handlers{
		svc: attestation.New(attestation.Options{Settings: settings}),
		log: kit.NewLogger("error", false).Child("ServerAttestation"),
		translate: func(*http.Request) attestation.Translate {
			return func(key string) string {
				if text, ok := texts[key]; ok {
					return text
				}
				return key
			}
		},
		brand: func(*http.Request) any { return brand },
		logAction: func(_ *http.Request, action, details string) error {
			if failLog {
				return errors.New("no such table: audit_log")
			}
			*logs = append(*logs, logged{action, details})
			return nil
		},
	}
}

// call sends a request to one handler, a body through the JSON parser.
func call(h http.HandlerFunc, method, body string) answer {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	r := httptest.NewRequest(method, "/api/server-attestation/x", reader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	middleware.Bodies(middleware.BodyOptions{})(h).ServeHTTP(w, r)
	return answer{w.Code, w.Body.String()}
}

func loadRouteVectors(t *testing.T) routeVectors {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "routes.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var v routeVectors
	if err := json.NewDecoder(zr).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Every route answers, byte for byte, what the Node route answered for
// the same stored settings, body, brand and translations, and writes the
// same rows and audit entries.
func TestRoutesAsNodeAnswered(t *testing.T) {
	v := loadRouteVectors(t)
	failed := 0
	differ := func(format string, args ...any) {
		if failed++; failed <= 20 {
			t.Errorf(format, args...)
		}
	}

	refusedEmpty := 0
	for i, c := range v.Put {
		settings := newFakeSettings()
		settings.failWrite = c.Mode == "failWrite"
		var logs []logged
		h := testHandlers(settings, brandOf(t, c.Brand), v.Translations[c.Texts], c.Mode == "failLog", &logs)
		if withoutTexts(c.Body) {
			// C6: a body with none of the texts is refused and stores
			// nothing, where Node stored the default texts
			refusedEmpty++
			c.Put = answer{Status: http.StatusBadRequest, Body: `{"success":false,"error":"Invalid badge configuration"}`}
			c.Writes, c.Logs = nil, nil
			c.Get = call(testHandlers(newFakeSettings(), brandOf(t, c.Brand), v.Translations[c.Texts], false, &[]logged{}).badgeConfig, http.MethodGet, "")
		}
		if got := call(h.saveBadgeConfig, http.MethodPut, c.Body); got != c.Put {
			differ("put %d %s (%s, %s):\n  node %d %s\n    go %d %s", i, c.Body, c.Texts, c.Mode, c.Put.Status, c.Put.Body, got.Status, got.Body)
		}
		if !reflect.DeepEqual(settings.writes, c.Writes) && (len(settings.writes) != 0 || len(c.Writes) != 0) {
			differ("put %d %s wrote %q, node %q", i, c.Body, settings.writes, c.Writes)
		}
		if len(logs) != len(c.Logs) {
			differ("put %d %s: %d audit entries, node %d", i, c.Body, len(logs), len(c.Logs))
		}
		for j := range logs {
			if j < len(c.Logs) && (logs[j].action != c.Logs[j][1] || logs[j].details != c.Logs[j][2]) {
				differ("put %d audit entry %v, node %v", i, logs[j], c.Logs[j])
			}
		}
		settings.failWrite = false
		if got := call(h.badgeConfig, http.MethodGet, ""); got != c.Get {
			differ("get after put %d %s:\n  node %s\n    go %s", i, c.Body, c.Get.Body, got.Body)
		}
	}

	rereads := 0
	for i, c := range v.Get {
		settings := newFakeSettings()
		settings.rows[attestation.BadgeConfigKey] = c.Config
		settings.failing[attestation.BadgeConfigKey] = c.Failing
		var logs []logged
		h := testHandlers(settings, brandOf(t, c.Brand), v.Translations[c.Texts], false, &logs)
		if got := call(h.badgeConfig, http.MethodGet, ""); got != c.First {
			differ("get %d %s (failing %v, brand %s, %s):\n  node %d %s\n    go %d %s", i, show(c.Config), c.Failing, c.Brand, c.Texts, c.First.Status, c.First.Body, got.Status, got.Body)
		}
		changed := `{"badgeTitle":"changed behind its back"}`
		settings.rows[attestation.BadgeConfigKey] = &changed
		settings.failing = map[string]bool{}
		second := c.Second
		if c.Failing {
			// SA5: the defaults of a failed read are not kept, the second
			// request reads the row as a console that starts with it does
			rereads++
			fresh := newFakeSettings()
			fresh.rows[attestation.BadgeConfigKey] = &changed
			second = call(testHandlers(fresh, brandOf(t, c.Brand), v.Translations[c.Texts], false, &logs).badgeConfig, http.MethodGet, "")
			if second == c.Second {
				differ("second get %d after a failed read answers as Node did", i)
			}
		}
		if got := call(h.badgeConfig, http.MethodGet, ""); got != second {
			differ("second get %d %s:\n  node %s\n    go %s", i, show(c.Config), second.Body, got.Body)
		}
	}
	if rereads == 0 {
		differ("no get with a failed read: the fix of SA5 changes no vector")
	}
	if refusedEmpty == 0 {
		differ("no put without texts: the fix of C6 changes no vector")
	}

	for i, c := range v.Public {
		settings := newFakeSettings()
		settings.rows[attestation.ResultKey] = c.Result
		settings.rows[attestation.BadgeConfigKey] = c.Config
		if c.Failing != "" {
			settings.failing[c.Failing] = true
		}
		var logs []logged
		h := testHandlers(settings, brandOf(t, c.Brand), v.Translations[c.Texts], false, &logs)
		if got := call(h.public, http.MethodGet, ""); got != c.Answer {
			differ("public %d: result %s, config %s, brand %s, %s:\n  node %s\n    go %s", i, show(c.Result), show(c.Config), c.Brand, c.Texts, c.Answer.Body, got.Body)
		}
	}

	for i, c := range v.Status {
		settings := newFakeSettings()
		settings.rows[attestation.ResultKey] = c.Result
		settings.failing[attestation.ResultKey] = c.Failing
		var logs []logged
		h := testHandlers(settings, "BetterDesk", nil, false, &logs)
		if got := call(h.status, http.MethodGet, ""); got != c.Status {
			differ("status %d %s (failing %v):\n  node %s\n    go %s", i, show(c.Result), c.Failing, c.Status.Body, got.Body)
		}
		if got := call(h.result, http.MethodGet, ""); got != c.ResultAnswer {
			differ("result %d %s (failing %v):\n  node %s\n    go %s", i, show(c.Result), c.Failing, c.ResultAnswer.Body, got.Body)
		}
	}

	var logs []logged
	h := testHandlers(newFakeSettings(), "BetterDesk", nil, false, &logs)
	if got := call(h.abort, http.MethodPost, "{}"); got != v.Abort[0] {
		differ("abort: node %s, go %s", v.Abort[0].Body, got.Body)
	}

	if failed > 20 {
		t.Errorf("%d more vectors differ", failed-20)
	}
	n := len(v.Put) + len(v.Get) + len(v.Public) + len(v.Status) + len(v.Abort)
	if n < 10000 {
		t.Fatalf("only %d vectors", n)
	}
	t.Logf("%d vectors: put %d, get %d, public %d, status %d", n, len(v.Put), len(v.Get), len(v.Public), len(v.Status))
}

func show(s *string) string {
	if s == nil {
		return "(no row)"
	}
	b, _ := json.Marshal(*s)
	return string(b)
}

// withoutTexts reports whether a request body is a JSON object, or none,
// with neither badgeTitle nor tierLabels.
func withoutTexts(body string) bool {
	if strings.TrimSpace(body) == "" {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &fields) != nil {
		return false
	}
	_, title := fields["badgeTitle"]
	_, labels := fields["tierLabels"]
	return !title && !labels
}
