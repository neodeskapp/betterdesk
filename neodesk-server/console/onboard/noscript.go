package onboard

import (
	"html"
	"strings"
)

// The invite page (/join/<code>) is the web app's, which needs JavaScript.
// For a browser without it the server puts the page's essentials into the
// page as a <noscript> block: the three steps, the download for the
// system the browser names, the other systems, and the code for a NeoDesk
// that is already installed. It tells nothing about the code itself (the
// links carry the code of the address; a code that cannot be used answers
// its downloads with 404), so it needs no lookup and no limit.

// NoScriptDownload is one package of the client release.
type NoScriptDownload struct {
	OS, Arch, Format string
	// URL is the download address with the page's code; File the name the
	// package is saved under.
	URL, File string
}

// NoScriptFacts are what the block shows.
type NoScriptFacts struct {
	// Server is the console's name, Address its address (for a NeoDesk
	// that joins with the code alone).
	Server, Address string
	// DisplayCode is the page's code as people read it (ABCD-2345).
	DisplayCode string
	// UserAgent is the browser's, to pick the download for its system.
	UserAgent string
	Downloads []NoScriptDownload
	// InstallCommand is the Linux command line; DeepLink opens an
	// installed NeoDesk with the code (macOS).
	InstallCommand, DeepLink string
}

// systemOf is the system and processor a browser names: windows, macos or
// linux, and x64 or arm64; "" for a phone, a tablet or anything else.
func systemOf(ua string) (system, arch string) {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipad") || strings.Contains(l, "android") || strings.Contains(l, "mobile"):
		return "", ""
	case strings.Contains(l, "windows"):
		if strings.Contains(l, "arm64") || strings.Contains(l, "aarch64") {
			return "windows", "arm64"
		}
		return "windows", "x64"
	case strings.Contains(l, "macintosh") || strings.Contains(l, "mac os x"):
		// Browsers on a Mac do not tell the processor: Apple chips first
		return "macos", "arm64"
	case strings.Contains(l, "linux") || strings.Contains(l, "x11"):
		return "linux", "x64"
	}
	return "", ""
}

// mainDownload is the download for the system, nil for none.
func mainDownload(f NoScriptFacts, system, arch string) *NoScriptDownload {
	var fallback *NoScriptDownload
	for i := range f.Downloads {
		d := &f.Downloads[i]
		if d.OS != system {
			continue
		}
		if d.Arch == arch {
			return d
		}
		if fallback == nil && (d.Arch == "x64" || system == "macos") {
			fallback = d
		}
	}
	return fallback
}

// downloadName is how the list names a package: "Windows x64", "macOS
// Apple chip", "Linux x64 (rpm)".
func downloadName(t Translate, d NoScriptDownload) string {
	switch d.OS {
	case "macos":
		if d.Arch == "arm64" {
			return "macOS " + t("onboard.public.mac_apple", nil)
		}
		return "macOS " + t("onboard.public.mac_intel", nil)
	case "windows":
		return "Windows " + d.Arch
	}
	return "Linux " + d.Arch + " (" + d.Format + ")"
}

// noScriptStyle keeps the block readable at phone width, light and dark.
const noScriptStyle = `.nd-ns{box-sizing:border-box;max-width:40rem;margin:0 auto;padding:24px 16px;font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:#1f2933;background:#fff;overflow-wrap:anywhere}` +
	`.nd-ns h1{font-size:1.5rem;line-height:1.25;margin:0 0 8px}.nd-ns h2{font-size:1.05rem;margin:24px 0 8px}.nd-ns p,.nd-ns ol,.nd-ns ul{margin:0 0 12px}` +
	`.nd-ns .main{display:inline-block;padding:12px 20px;border-radius:6px;background:#1d4ed8;color:#fff;font-weight:600;text-decoration:none}` +
	`.nd-ns .muted{color:#6b7280;font-size:.9rem}.nd-ns .code{font:600 1.5rem/1.3 ui-monospace,monospace;letter-spacing:.1em}.nd-ns pre{white-space:pre-wrap;background:#f4f5f7;padding:12px;border-radius:6px}` +
	`@media (prefers-color-scheme:dark){.nd-ns,body{background:#0d1117;color:#e6edf3}.nd-ns .muted{color:#8b949e}.nd-ns a{color:#79c0ff}.nd-ns .main{background:#58a6ff;color:#0d1117}.nd-ns pre{background:#161b22}}`

// NoScriptJoin is the <noscript> block of an invite page.
func NoScriptJoin(t Translate, f NoScriptFacts) string {
	e := html.EscapeString
	vars := map[string]any{"server": f.Server, "company": f.Server, "address": f.Address}
	system, arch := systemOf(f.UserAgent)
	main := mainDownload(f, system, arch)

	var b strings.Builder
	w := func(s ...string) {
		for _, part := range s {
			b.WriteString(part)
		}
	}
	w(`<noscript><style>`, noScriptStyle, `</style><main class="nd-ns">`)
	w(`<h1>`, e(t("onboard.public.join_title", vars)), `</h1>`)
	w(`<p>`, e(t("onboard.public.join_lead", vars)), `</p>`)
	w(`<ol>`)
	for i := 1; i <= 3; i++ {
		w(`<li>`, e(t("onboard.public.welcome_step_"+string(rune('0'+i)), vars)), `</li>`)
	}
	w(`</ol>`)

	switch {
	case system == "linux" && f.InstallCommand != "":
		w(`<p>`, e(t("onboard.public.command_hint_join", vars)), `</p><pre>`, e(f.InstallCommand), `</pre>`)
	case main != nil:
		label := "onboard.public.download_join"
		if system == "windows" {
			label = "onboard.public.download_join_windows"
		} else if system == "macos" {
			label = "onboard.public.download_join_macos"
		}
		w(`<p><a class="main" href="`, e(main.URL), `" download="`, e(main.File), `">`, e(t(label, vars)), `</a></p>`)
		w(`<p class="muted">`, e(main.File), `</p>`)
		if system == "windows" {
			key := "onboard.public.join_steps_windows_3"
			if main.Format == "msi" {
				key = "onboard.public.join_steps_windows_msi_3"
			}
			w(`<p class="muted">`, e(t(key, vars)), `</p>`)
		}
		if system == "macos" {
			w(`<h2>`, e(t("onboard.public.mac_steps_title", vars)), `</h2><ol>`)
			for i := 1; i <= 4; i++ {
				w(`<li>`, e(t("onboard.public.join_steps_macos_"+string(rune('0'+i)), vars)), `</li>`)
			}
			w(`</ol>`)
			if f.DeepLink != "" {
				w(`<p><a href="`, e(f.DeepLink), `">`, e(t("onboard.public.open_app", vars)), `</a></p>`)
			}
		}
	}

	var others []NoScriptDownload
	for _, d := range f.Downloads {
		if main == nil || d.URL != main.URL {
			others = append(others, d)
		}
	}
	if len(others) > 0 {
		w(`<h2>`, e(t("onboard.public.other_systems", vars)), `</h2><ul>`)
		for _, d := range others {
			w(`<li><a href="`, e(d.URL), `" download="`, e(d.File), `">`, e(downloadName(t, d)), `</a></li>`)
		}
		w(`</ul>`)
	}
	w(`<h2>`, e(t("onboard.public.code_hint_join", vars)), `</h2>`)
	w(`<p class="code" dir="ltr">`, e(f.DisplayCode), `</p>`)
	if f.Address != "" {
		w(`<p class="muted">`, e(t("onboard.public.server_address", vars)), `</p>`)
	}
	w(`</main></noscript>`)
	return b.String()
}
