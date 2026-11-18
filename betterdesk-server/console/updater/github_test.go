package updater

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testClient(f *fakeGitHub, token string) *githubClient {
	return &githubClient{transport: f, userAgent: "ua", token: token, cacheTTL: time.Minute, now: time.Now, cache: map[string]cached{}}
}

// The token goes to the API host only, never to github.com or a file
// host.
func TestTokenOnlyToTheAPI(t *testing.T) {
	in := newInstall(t)
	in.env["UPDATE_GITHUB_TOKEN"] = "secret-token"
	in.svc = New(in.opts)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	for i, req := range in.gh.requests {
		auth := in.gh.headers[i].Get("Authorization")
		api := strings.HasPrefix(req, "GET https://api.github.com/")
		if api && auth != "Bearer secret-token" || !api && auth != "" {
			t.Errorf("%s: Authorization %q", req, auth)
		}
	}
}

func TestAPIRedirects(t *testing.T) {
	f := newFakeGitHub()
	f.on("GET https://api.github.com/a", func(*http.Request) *http.Response { return redirect("/b") })
	f.on("GET https://api.github.com/b", func(*http.Request) *http.Response { return respond(200, `{"ok":1}`, nil) })
	f.on("GET https://api.github.com/evil", func(*http.Request) *http.Response { return redirect("https://evil.example/x") })
	f.on("GET https://api.github.com/loop", func(*http.Request) *http.Response { return redirect("/loop") })
	f.on("GET https://api.github.com/port", func(*http.Request) *http.Response { return redirect("https://api.github.com:8443/x") })
	g := testClient(f, "")
	if body, err := g.apiGet(context.Background(), "/a", false); err != nil || string(body) != `{"ok":1}` {
		t.Errorf("same host: %q %v", body, err)
	}
	for path, want := range map[string]string{
		"/evil": "GitHub URL host is not allowed", "/loop": "redirect limit exceeded", "/port": "GitHub URL host is not allowed",
		"/missing": "GitHub API 404",
	} {
		if _, err := g.apiGet(context.Background(), path, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if n := f.count("GET https://api.github.com/loop"); n != maxRedirects+1 {
		t.Errorf("the loop was followed %d times", n)
	}
}

// A release file must be one of the release found, on github.com.
func TestDownloadAddress(t *testing.T) {
	f := newFakeGitHub()
	f.on("GET https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json", func(*http.Request) *http.Response {
		return respond(200, "ok", nil)
	})
	g := testClient(f, "")
	want := releaseFile{owner: "UNITRONIX", repo: "BetterDesk", tag: "v1.0.0", name: "manifest.json"}
	for _, address := range []string{
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json",
		"https://GITHUB.com/unitronix/betterdesk/releases/download/v1.0.0/manifest.json",
	} {
		var buf bytes.Buffer
		if _, err := g.download(context.Background(), address, want, 100, &buf); err != nil && !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{
		"http://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json",
		"https://github.com:444/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json",
		"https://user@github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json",
		"https://evil.example/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json",
		"https://github.com/other/BetterDesk/releases/download/v1.0.0/manifest.json",
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v0.9.0/manifest.json",
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.sig",
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/x/../manifest.json",
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json?x=1",
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json#x",
		"https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0%2Fmanifest.json",
		"",
	} {
		var buf bytes.Buffer
		if _, err := g.download(context.Background(), address, want, 100, &buf); err == nil || !strings.Contains(err.Error(), "not a file of the release") {
			t.Errorf("%q: %v", address, err)
		}
	}
	if len(f.requests) != 2 {
		t.Errorf("requests sent for refused addresses: %v", f.requests)
	}
	var big bytes.Buffer
	f.on("GET https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json", func(*http.Request) *http.Response {
		return respond(200, strings.Repeat("x", 101), nil)
	})
	if _, err := g.download(context.Background(), "https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json", want, 100, &big); err == nil {
		t.Error("a file over the limit was taken")
	}
}

func TestCacheTTL(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"": 120 * time.Second, "0": 120 * time.Second, "abc": 120 * time.Second, "5000": 5 * time.Second, "-1": 0, " 300 ": 300 * time.Millisecond,
	} {
		if got := cacheTTL(raw); got != want {
			t.Errorf("cacheTTL(%q) = %v", raw, got)
		}
	}
}

func TestVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"3.6.0", "3.5.9", 1}, {"3.5.10", "3.5.9", 1}, {"3.5.9", "3.5.9", 0}, {"3.6.0-rc.1", "3.6.0", -1},
		{"3.6.0-rc.2", "3.6.0-rc.1", 1}, {"dev", "3.6.0", -1}, {"3.6.0", "dev", 1}, {"dev", "dev", 0},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%s, %s) = %d", tc.a, tc.b, got)
		}
	}
	for out, want := range map[string][2]string{
		"betterdesk-server 3.6.0 (built 2026-11-18, commit " + newCommit + ")\n": {"3.6.0", newCommit},
		"betterdesk-server 3.5.224 (built unknown)":                              {"3.5.224", ""},
	} {
		v, c, ok := ParseVersionOutput(out)
		if !ok || v != want[0] || c != want[1] {
			t.Errorf("%q: %q %q %v", out, v, c, ok)
		}
	}
	for _, out := range []string{"", "betterdesk 3.6.0 (built x)", "betterdesk-server 3.6.0", "x\nbetterdesk-server 3.6.0 (built x)"} {
		if _, _, ok := ParseVersionOutput(out); ok {
			t.Errorf("%q was read", out)
		}
	}
	for platform, want := range map[[2]string]string{
		{"linux", "amd64"}: "betterdesk-server-linux-amd64", {"linux", "arm"}: "betterdesk-server-linux-armv7",
		{"windows", "amd64"}: "betterdesk-server-windows-amd64.exe", {"linux", "arm64"}: "betterdesk-server-linux-arm64",
	} {
		if got := AssetName(platform[0], platform[1]); got != want {
			t.Errorf("%v: %s", platform, got)
		}
	}
}

// A body of unknown length (chunked) is cut at the limit as it streams.
func TestDownloadLimitWithoutLength(t *testing.T) {
	f := newFakeGitHub()
	address := "https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json"
	f.on("GET "+address, func(*http.Request) *http.Response {
		res := respond(200, strings.Repeat("x", 101), nil)
		res.ContentLength = -1
		return res
	})
	g := testClient(f, "")
	want := releaseFile{owner: "UNITRONIX", repo: "BetterDesk", tag: "v1.0.0", name: "manifest.json"}
	var buf bytes.Buffer
	if _, err := g.download(context.Background(), address, want, 100, &buf); err == nil || !strings.Contains(err.Error(), "larger than allowed") {
		t.Fatalf("error %v", err)
	}
	if buf.Len() > 100 {
		t.Errorf("%d bytes were kept", buf.Len())
	}
	buf.Reset()
	if n, err := g.download(context.Background(), address, want, 101, &buf); err != nil || n != 101 {
		t.Errorf("at the limit: %d %v", n, err)
	}
}
