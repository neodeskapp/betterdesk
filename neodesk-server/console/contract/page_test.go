package contract

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// shellPage is the web app's page as a build writes it: the policy in a
// <meta> tag with the hash of the start script, the build's hashed files
// and the start script that names them.
func shellPage(build, version, injected string) (page, hash string) {
	script := `
				{
					__sveltekit_` + build + ` = {
						base: "",
						version: "` + version + `"
					};
					import("/_ui/immutable/entry/start.` + build + `.js").then(async (kit) => {
						kit.init(__sveltekit_` + build + `);
					});
				}
			`
	sum := sha256.Sum256([]byte(script))
	hash = base64.StdEncoding.EncodeToString(sum[:])
	page = `<!doctype html>
<html lang="en">
	<head>` + injected + `
		<meta charset="utf-8" />
		<meta http-equiv="content-security-policy" content="default-src 'self'; script-src 'self' 'sha256-` + hash + `'">
		<link href="/_ui/immutable/entry/start.` + build + `.js" rel="modulepreload">
		<link href="/_ui/immutable/chunks/` + build + `x.js" rel="modulepreload">
		<link href="/_ui/immutable/assets/0.` + build + `.css" rel="stylesheet">
		<script src="/console-boot.js"></script>
	</head>
	<body>
		<div id="bd-root">
			<script>` + script + `</script>
		</div>
	</body>
</html>
`
	return page, hash
}

func pageAnswer(page, policy string) Answer {
	header := http.Header{}
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Content-Security-Policy", policy)
	return NewAnswer(404, header, []byte(page), false, NewNormalizer("http://bdgo-c1:5000", ""))
}

// The policy's hash of the page's own inline script is a word, in the
// header and in the page; a hash of no script on the page stays as sent.
func TestPolicyHashOfThePagesOwnScriptIsAWord(t *testing.T) {
	page, hash := shellPage("U1O-cXFv", "1794272287207", "")
	other := strings.Repeat("A", 43) + "="
	a := pageAnswer(page, "script-src 'self' 'sha256-"+hash+"' 'sha256-"+other+"'; frame-ancestors 'none'")
	if got, want := a.Headers["content-security-policy"][0], "script-src 'self' 'sha256-<inline-script>' 'sha256-"+other+"'; frame-ancestors 'none'"; got != want {
		t.Errorf("policy\n got %s\nwant %s", got, want)
	}
	if strings.Contains(a.Body.Head, hash) {
		t.Errorf("the page's own policy still holds the hash: %s", a.Body.Head)
	}

	// Without the script in the body the hash is not the page's own.
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Content-Security-Policy", "script-src 'sha256-"+hash+"'")
	j := NewAnswer(200, header, []byte(`{}`), false, nil)
	if got := j.Headers["content-security-policy"][0]; got != "script-src 'sha256-"+hash+"'" {
		t.Errorf("policy of a JSON answer = %s, want the hash as sent", got)
	}
}

// Two builds of the web app give the same page; what the server puts into
// the page, a change of the template and a policy that does not allow the
// page's script still show.
func TestStablePageIsTheSameForEveryBuild(t *testing.T) {
	first, firstHash := shellPage("U1O-cXFv", "1794272287207", "")
	second, secondHash := shellPage("Bq7-2kLm", "1795000000000", "")
	a := pageAnswer(first, "script-src 'self' 'sha256-"+firstHash+"'")
	b := pageAnswer(second, "script-src 'self' 'sha256-"+secondHash+"'")
	if diffs := CompareAnswers(newGoldenAnswer(a, nil, nil), b); len(diffs) > 0 {
		t.Errorf("two builds differ: %q", diffStrings(diffs))
	}
	if strings.Contains(a.Body.Head, "U1O") {
		t.Errorf("head still names the build: %s", a.Body.Head)
	}

	cases := []struct {
		name   string
		answer Answer
		where  string
	}{
		{"the server's error tag", func() Answer {
			p, h := shellPage("Bq7-2kLm", "1795000000000", `<meta name="bd-error" content="404">`)
			return pageAnswer(p, "script-src 'self' 'sha256-"+h+"'")
		}(), "body"},
		{"another template", pageAnswer(strings.Replace(second, `<html lang="en">`, `<html lang="de">`, 1), "script-src 'self' 'sha256-"+secondHash+"'"), "body"},
		{"a hash of another script", pageAnswer(second, "script-src 'self' 'sha256-"+firstHash+"'"), "header content-security-policy"},
		{"one file more", pageAnswer(strings.Replace(second, `<script src="/console-boot.js">`, `<link href="/_ui/immutable/chunks/zz.js" rel="modulepreload"><script src="/console-boot.js">`, 1), "script-src 'self' 'sha256-"+secondHash+"'"), "body"},
	}
	for _, c := range cases {
		diffs := CompareAnswers(newGoldenAnswer(a, nil, nil), c.answer)
		if len(diffs) != 1 || diffs[0].Where != c.where {
			t.Errorf("%s: diffs %q, want one in %s", c.name, diffStrings(diffs), c.where)
		}
	}
}

// Only SvelteKit's start script loses its text; another inline script of
// a page is compared as it is.
func TestStablePageKeepsOtherScripts(t *testing.T) {
	page := `<html><head><script>window.x = 1</script></head><body><script src="/a.js"></script></body></html>`
	if got := stablePage(page, nil); got != page {
		t.Errorf("stable page = %s, want it unchanged", got)
	}
}
