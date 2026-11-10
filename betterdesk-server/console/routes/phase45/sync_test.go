package phase45

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
