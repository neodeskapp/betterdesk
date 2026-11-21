package generator

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// TestSigningSeedWrittenOnlyWhenItDiffers leaves the seed file alone
// when it holds the configured seed and writes it when the setting
// changes (BUGS RV58: every readiness check rewrote it).
func TestSigningSeedWrittenOnlyWhenItDiffers(t *testing.T) {
	seed := testSeed
	s := New(Options{DataDir: t.TempDir(), Env: func() []string { return []string{"BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED=" + seed} }})
	if _, err := s.EnsureSigningKey(); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	must(t, os.Chtimes(s.SigningSeedPath(), old, old))
	for i := 0; i < 3; i++ {
		if _, err := s.EnsureSigningKey(); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(s.SigningSeedPath()); err != nil || !info.ModTime().Equal(old) {
		t.Errorf("the seed file was written again: %v %v", info.ModTime(), err)
	}
	seed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if _, err := s.EnsureSigningKey(); err != nil {
		t.Fatal(err)
	}
	if got := s.SigningSeed(); got != seed {
		t.Errorf("after the setting changed the file holds %q", got)
	}
}

// TestSigningFileWritesOverlap lets two writers in the same millisecond
// replace a signing file while a reader reads it: no write fails, the
// reader always finds a valid seed, and no temporary file stays (BUGS
// RV58: the writers shared a temporary name, and the file was removed
// before the rename).
func TestSigningFileWritesOverlap(t *testing.T) {
	at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s := New(Options{DataDir: t.TempDir(), PID: 74, Now: func() time.Time { return at }, Env: func() []string { return nil }})
	must(t, s.writeSigningFile(s.SigningSeedPath(), testSeed, 0o600))
	other := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	var failures, misses atomic.Int64
	var writers, reader sync.WaitGroup
	done := make(chan struct{})
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if !s.SigningSeedValid() {
				misses.Add(1)
			}
		}
	}()
	for _, value := range []string{testSeed, other} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 300; i++ {
				if err := s.writeSigningFile(s.SigningSeedPath(), value, 0o600); err != nil {
					failures.Add(1)
				}
			}
		}()
	}
	writers.Wait()
	close(done)
	reader.Wait()
	if failures.Load() != 0 || misses.Load() != 0 {
		t.Errorf("%d writes failed, %d reads found no valid seed", failures.Load(), misses.Load())
	}
	if entries, _ := os.ReadDir(s.ModuleDir()); len(entries) != 1 {
		t.Errorf("files left beside the seed: %v", entries)
	}
}

// TestTemplateInstallChecksRepo refuses a repository that is no
// owner/name or not on the allow list before any request goes out, and
// asks GitHub for the default or an allowed one (BUGS RV54: download
// mode put any text into the API address and sent the token with it).
func TestTemplateInstallChecksRepo(t *testing.T) {
	for _, c := range []struct {
		name, repo, allowed string
		wantErr             string
		wantPath            string
	}{
		{"path in the name", "o/r/../../../user/repos?per_page=100&x=", "", "betterdesk_client_repo_invalid", ""},
		{"not on the list", "someorg/private-repo", "", "betterdesk_client_repo_not_allowed", ""},
		{"default", "", "", "", "/repos/UNITRONIX/BetterDesk-Client/releases/latest"},
		{"on the list", " someorg/private-repo ", "UNITRONIX/BetterDesk-Client, someorg/private-repo", "", "/repos/someorg/private-repo/releases/latest"},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := &fakeGitHub{answer: func(r *http.Request) *http.Response { return reply(500, []byte(`{}`)) }}
			env := []string{"BETTERDESK_GITHUB_TOKEN=made-up-token"}
			if c.allowed != "" {
				env = append(env, "BETTERDESK_CLIENT_ALLOWED_REPOS="+c.allowed)
			}
			s := New(Options{DataDir: t.TempDir(), HTTP: gh, Env: func() []string { return env }})
			must(t, os.WriteFile(filepath.Join(s.opts.DataDir, signingSeedName), []byte(testSeed), 0o600))
			if _, err := s.AcceptModuleTerms(); err != nil {
				t.Fatal(err)
			}
			_, err := s.InstallFromGitHub(context.Background(), c.repo, "")
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr || len(gh.requests) != 0 {
					t.Fatalf("got %v after %d requests, want %s and none", err, len(gh.requests), c.wantErr)
				}
				return
			}
			if len(gh.requests) == 0 || gh.requests[0].URL.Opaque != "//api.github.com"+c.wantPath {
				t.Fatalf("requests %v", gh.requests)
			}
		})
	}
}

// TestFinalizeTakesOnlyAnObject completes a profile that is no object as
// the empty profile, without a key per character or item (BUGS RV53).
func TestFinalizeTakesOnlyAnObject(t *testing.T) {
	s := New(Options{ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Env: func() []string { return nil }})
	want := stringify(s.FinalizeSupportBranding(context.Background(), goapi.NewObject()))
	for _, input := range []any{"abc", []any{"x", "y"}, float64(5), nil} {
		if got := stringify(s.FinalizeSupportBranding(context.Background(), input)); got != want {
			t.Errorf("%#v: %s, want %s", input, got, want)
		}
	}
	got := s.FinalizeSupportBranding(context.Background(), goapi.NewObject("company_name", "Acme", "server_host", "h.example"))
	if got.Get("company_name") != "Acme" || got.Get("server_host") != "h.example" {
		t.Errorf("an object's fields are lost: %s", stringify(got))
	}
}
