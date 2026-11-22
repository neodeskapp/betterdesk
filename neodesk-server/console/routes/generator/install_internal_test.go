package generator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gen "github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// contextProbe answers every request to GitHub with 404 and records
// whether the request's context was alive and when it ends.
type contextProbe struct {
	mu        sync.Mutex
	calls     int
	cancelled bool
	deadline  time.Time
}

func (p *contextProbe) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if err := r.Context().Err(); err != nil {
		p.cancelled = true
		return nil, err
	}
	p.deadline, _ = r.Context().Deadline()
	return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(`{}`))), Request: r}, nil
}

// TestInstallOutlivesTheRequest runs the template install and the source
// sync on a context that the request's end does not cancel, bounded by a
// deadline of its own (BUGS RV57: a closed tab or a proxy's read timeout
// killed the downloads and git).
func TestInstallOutlivesTheRequest(t *testing.T) {
	seed := "BQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU="
	for _, embedded := range []bool{false, true} {
		probe := &contextProbe{}
		data := t.TempDir()
		env := []string{"BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED=" + seed}
		service := gen.New(gen.Options{DataDir: data, Embedded: embedded, HTTP: probe, Env: func() []string { return env }})
		if !embedded {
			if err := os.WriteFile(filepath.Join(data, "custom-client-signing.seed"), []byte(seed), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.AcceptModuleTerms(); err != nil {
				t.Fatal(err)
			}
		}
		h := &handlers{gen: service, dataDir: data, log: kit.NewLogger("error", false).Child("generator")}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest(http.MethodPost, "/api/generator/module/install", nil).WithContext(ctx)
		start := time.Now()
		h.install(httptest.NewRecorder(), r)
		if probe.calls == 0 || probe.cancelled || probe.deadline.Sub(start) < installDeadline-time.Minute || probe.deadline.Sub(start) > installDeadline+time.Minute {
			t.Errorf("embedded %v: %d requests, cancelled %v, deadline in %v", embedded, probe.calls, probe.cancelled, probe.deadline.Sub(start))
		}
	}
}
