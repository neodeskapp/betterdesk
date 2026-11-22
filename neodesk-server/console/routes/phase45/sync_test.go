package phase45

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/agentreleases"
	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A failed import is 502 with its reason, and writes no audit entry.
func TestSyncFailure(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		for _, c := range []struct {
			name      string
			transport roundTrip
			want      string
		}{
			{"GitHub refuses", func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}, `{"success":false,"error":"generic_agent_release_list_failed:403"}`},
			{"no network", func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: connection reset")
			}, `{"success":false,"error":"dial tcp: connection reset"}`},
		} {
			h := &handlers{
				d:        &kit.Deps{Audit: auditlog.New(db), Store: db},
				releases: agentreleases.New(agentreleases.Options{DataDir: t.TempDir(), HTTP: c.transport}),
			}
			rec := httptest.NewRecorder()
			h.syncRelease(rec, httptest.NewRequest(http.MethodPost, "/api/agent-releases/sync", nil))
			if rec.Code != http.StatusBadGateway || rec.Body.String() != c.want {
				t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
			}
			var n int
			if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_log").Scan(&n); err != nil || n != 0 {
				t.Errorf("%s: %d audit entries (%v)", c.name, n, err)
			}
		}
	})
}

// RV57: the import goes on when the browser or a proxy gives up on the
// request: it runs on a context the request's end does not cancel, with a
// deadline of its own.
func TestSyncOutlivesTheRequest(t *testing.T) {
	db := storetest.Migrated(t)
	var seen error
	var left time.Duration
	called := false
	transport := roundTrip(func(r *http.Request) (*http.Response, error) {
		called, seen = true, r.Context().Err()
		if deadline, ok := r.Context().Deadline(); ok {
			left = time.Until(deadline)
		}
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})
	h := &handlers{
		d:        &kit.Deps{Audit: auditlog.New(db), Store: db},
		releases: agentreleases.New(agentreleases.Options{DataDir: t.TempDir(), HTTP: transport}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	h.syncRelease(rec, httptest.NewRequest(http.MethodPost, "/api/agent-releases/sync", nil).WithContext(ctx))
	if !called || seen != nil {
		t.Fatalf("the import ran %v with the context ended by %v; want it to run on a live context", called, seen)
	}
	if left < 29*time.Minute || left > 30*time.Minute {
		t.Errorf("the import has %v left; want a deadline of 30 minutes", left)
	}
	if rec.Body.String() != `{"success":false,"error":"generic_agent_release_list_failed:403"}` {
		t.Errorf("answer: %d %s", rec.Code, rec.Body.String())
	}
}

// RV51, RV55: a release the cache holds already, or one whose tag is not
// newer than the cache's, is no upstream failure: 409 with the error's
// code and tag, no audit entry, and nothing downloaded.
func TestSyncRefusalOfTheCacheIsAConflict(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		for _, c := range []struct {
			name, cached, want string
		}{
			{"already imported", "support-v1.2.0", `{"success":false,"error":"generic_agent_release_already_imported:support-v1.2.0"}`},
			{"not newer", "support-v2.0.0", `{"success":false,"error":"generic_agent_release_not_newer:support-v1.2.0"}`},
		} {
			dataDir := t.TempDir()
			folder := filepath.Join(dataDir, "modules", "betterdesk-support-releases", c.cached)
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, "manifest.json"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			var asked []string
			transport := roundTrip(func(r *http.Request) (*http.Response, error) {
				asked = append(asked, r.URL.String())
				body := `[{"tag_name":"support-v1.2.0","draft":false,"prerelease":false,"assets":[{"name":"manifest.json","browser_download_url":"https://github.com/x/manifest.json"}]}]`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			h := &handlers{
				d:        &kit.Deps{Audit: auditlog.New(db), Store: db},
				releases: agentreleases.New(agentreleases.Options{DataDir: dataDir, HTTP: transport}),
			}
			rec := httptest.NewRecorder()
			h.syncRelease(rec, httptest.NewRequest(http.MethodPost, "/api/agent-releases/sync", nil))
			if rec.Code != http.StatusConflict || rec.Body.String() != c.want {
				t.Errorf("%s: %d %s, want 409 %s", c.name, rec.Code, rec.Body.String(), c.want)
			}
			if len(asked) != 1 {
				t.Errorf("%s: asked %v, want the release list alone", c.name, asked)
			}
			var n int
			if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_log").Scan(&n); err != nil || n != 0 {
				t.Errorf("%s: %d audit entries (%v)", c.name, n, err)
			}
		}
	})
}
