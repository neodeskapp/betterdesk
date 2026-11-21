package generator

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

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
