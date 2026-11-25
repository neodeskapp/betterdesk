package onboard

import (
	"regexp"
	"strings"
	"testing"
)

func noScriptFacts(ua string) NoScriptFacts {
	dl := func(os, arch, format string) NoScriptDownload {
		return NoScriptDownload{OS: os, Arch: arch, Format: format,
			URL:  "/api/onboard/public/ABCD2345/download?arch=" + arch + "&format=" + format + "&os=" + os,
			File: "NeoDesk-Setup-desk.example.com-ABCD2345." + format}
	}
	return NoScriptFacts{
		Server: "Acme <Desk>", Address: "https://desk.example.com", DisplayCode: "ABCD-2345", UserAgent: ua,
		Downloads:      []NoScriptDownload{dl("windows", "x64", "exe"), dl("windows", "arm64", "exe"), dl("macos", "x64", "dmg"), dl("macos", "arm64", "dmg"), dl("linux", "x64", "deb")},
		InstallCommand: "curl -fsSL 'https://desk.example.com/join/ABCD2345/install.sh' | sudo sh",
		DeepLink:       "neodesk://join/desk.example.com/ABCD2345",
	}
}

func keysOnly(key string, _ map[string]any) string { return key }

var mainLink = regexp.MustCompile(`<a class="main" href="([^"]*)"`)

// TestTheInvitePageOffersTheDownloadWithoutJavaScript: the block names the
// three steps and offers the package for the browser's system as the main
// link, every other package below.
func TestTheInvitePageOffersTheDownloadWithoutJavaScript(t *testing.T) {
	for _, tc := range []struct{ name, ua, main string }{
		{"windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130", "arch=x64&format=exe&os=windows"},
		{"windows on arm", "Mozilla/5.0 (Windows NT 10.0; ARM64) Chrome/130", "arch=arm64&format=exe&os=windows"},
		{"mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) Safari/605.1.15", "arch=arm64&format=dmg&os=macos"},
	} {
		block := NoScriptJoin(keysOnly, noScriptFacts(tc.ua))
		m := mainLink.FindStringSubmatch(block)
		if m == nil || !strings.HasSuffix(unamp(m[1]), tc.main) {
			t.Errorf("%s: main download %v, want %s", tc.name, m, tc.main)
		}
		if strings.Count(block, `download="`) != 5 {
			t.Errorf("%s: not every package is offered once:\n%s", tc.name, block)
		}
		at := -1
		for _, step := range []string{"onboard.public.welcome_step_1", "onboard.public.welcome_step_2", "onboard.public.welcome_step_3"} {
			if next := strings.Index(block, step); next < at {
				t.Errorf("%s: %s missing or out of order", tc.name, step)
			} else {
				at = next
			}
		}
		if !strings.HasPrefix(block, "<noscript>") || !strings.HasSuffix(block, "</noscript>") || strings.Contains(block, "<script") {
			t.Errorf("%s: not a noscript block:\n%s", tc.name, block)
		}
	}
	mac := NoScriptJoin(keysOnly, noScriptFacts("Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5)"))
	if !strings.Contains(mac, "onboard.public.join_steps_macos_4") || !strings.Contains(mac, `href="neodesk://join/desk.example.com/ABCD2345"`) {
		t.Errorf("a Mac without its steps or the Open NeoDesk link:\n%s", mac)
	}
}

// TestTheInvitePageWithoutJavaScriptOnLinuxAndPhones: Linux gets the
// install command; a phone no main link, every package in the list.
func TestTheInvitePageWithoutJavaScriptOnLinuxAndPhones(t *testing.T) {
	linux := NoScriptJoin(keysOnly, noScriptFacts("Mozilla/5.0 (X11; Linux x86_64) Chrome/130"))
	if !strings.Contains(linux, "<pre>curl -fsSL &#39;https://desk.example.com/join/ABCD2345/install.sh&#39; | sudo sh</pre>") || mainLink.MatchString(linux) {
		t.Errorf("linux:\n%s", linux)
	}
	phone := NoScriptJoin(keysOnly, noScriptFacts("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148"))
	if mainLink.MatchString(phone) || strings.Count(phone, `download="`) != 5 {
		t.Errorf("phone:\n%s", phone)
	}
}

// TestTheNoScriptBlockEscapes: names from the branding are text.
func TestTheNoScriptBlockEscapes(t *testing.T) {
	block := NoScriptJoin(func(key string, vars map[string]any) string {
		if key == "onboard.public.join_lead" {
			return vars["server"].(string)
		}
		return key
	}, noScriptFacts(""))
	if strings.Contains(block, "Acme <Desk>") || !strings.Contains(block, "Acme &lt;Desk&gt;") {
		t.Errorf("not escaped:\n%s", block)
	}
}

// unamp undoes the escaping of & in an attribute.
func unamp(s string) string { return strings.ReplaceAll(s, "&amp;", "&") }
