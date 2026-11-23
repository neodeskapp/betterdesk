package generator

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/upstreamname"
)

// The vectors in testdata are fixed recordings of the real Node modules
// (testdata/README.md). Every test here replays one file and
// compares byte for byte.

func load(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// jsInput parses an input the oracle wrote with JSON.stringify; a missing
// input (undefined) is reported as missing.
func jsInput(t *testing.T, text *string) (any, bool) {
	t.Helper()
	if text == nil {
		return nil, false
	}
	v, err := parseJSON(*text)
	if err != nil {
		t.Fatalf("input %q: %v", *text, err)
	}
	return v, true
}

func TestValidateBrandingMatchesNode(t *testing.T) {
	var cases []struct {
		Input      *string  `json:"input"`
		Valid      bool     `json:"valid"`
		Errors     []string `json:"errors"`
		Normalized string   `json:"normalized"`
		Hash       string   `json:"hash"`
		Threw      string   `json:"threw"`
	}
	load(t, "validate_branding", &cases)
	if len(cases) < 1000 {
		t.Fatalf("only %d vectors", len(cases))
	}
	failed, idna := 0, 0
	for i, c := range cases {
		input, given := jsInput(t, c.Input)
		if !given {
			input = goapi.NewObject()
		}
		if needsIDNA(c.Normalized) {
			// The URL parser here refuses host names that need the rules
			// for international domain names, where Node accepts them
			// (DECISIONS A21): such a contact address is an error here.
			idna++
			continue
		}
		if key, fixed := colourListVectors[i]; fixed {
			if !checkColourListVector(t, i, key, input) {
				failed++
			}
			continue
		}
		got, err := ValidateBranding(input, nil)
		switch {
		case c.Threw != "":
			if err == nil {
				failed++
				t.Errorf("case %d %s: node threw %q, go did not", i, deref(c.Input), c.Threw)
			}
			continue
		case err != nil:
			failed++
			t.Errorf("case %d %s: go failed with %v", i, deref(c.Input), err)
			continue
		}
		c.Hash = neoDeskBrandingHash(t, i, c.Normalized, c.Hash)
		if host, fixed := ipv6HostVectors[i]; fixed {
			c.Normalized, c.Errors, c.Hash = withIPv6Host(t, c.Normalized, c.Errors, host)
			c.Valid = len(c.Errors) == 0
		}
		if fixed, changed := withFixedRelay(c.Normalized, "relay_host"); changed {
			v, err := parseJSON(fixed)
			if err != nil {
				t.Fatal(err)
			}
			c.Normalized, c.Hash = fixed, HashBranding(v.(*goapi.Object))
		}
		if got.Valid != c.Valid || !sameStrings(got.Errors, c.Errors) || stringify(got.Normalized) != c.Normalized || HashBranding(got.Normalized) != c.Hash {
			failed++
			if failed <= 20 {
				t.Errorf("case %d %s:\n want %v %v %s %s\n   go %v %v %s %s", i, deref(c.Input), c.Valid, c.Errors, c.Normalized, c.Hash,
					got.Valid, got.Errors, stringify(got.Normalized), HashBranding(got.Normalized))
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d of %d cases differ", failed, len(cases))
	}
	t.Logf("%d cases, %d left out for a contact address with an international host name", len(cases), idna)
}

// needsIDNA reports a normalized profile whose contact address Node took
// with a host that is not plain ASCII or is in punycode.
func needsIDNA(normalized string) bool {
	if normalized == "" {
		return false
	}
	v, err := parseJSON(normalized)
	if err != nil {
		return false
	}
	u, _ := prop(v, "contact_url").(string)
	if u == "" {
		return false
	}
	for _, r := range u {
		if r > 0x7f {
			return true
		}
	}
	return strings.Contains(strings.ToLower(u), "xn--")
}

// undefinedToNil turns the oracle's mark for undefined back into the
// missing value, also inside lists and objects.
func undefinedToNil(v any) any {
	switch t := v.(type) {
	case string:
		if t == "__undefined__" {
			return nil
		}
	case []any:
		for i, el := range t {
			t[i] = undefinedToNil(el)
		}
	case *goapi.Object:
		for _, k := range t.Keys() {
			t.Set(k, undefinedToNil(t.Get(k)))
		}
	}
	return v
}

func deref(s *string) string {
	if s == nil {
		return "undefined"
	}
	return *s
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSlugsMatchNode(t *testing.T) {
	var cases []struct {
		Name  string `json:"name"`
		Slug  string `json:"slug"`
		Error string `json:"error"`
	}
	load(t, "slugs", &cases)
	for _, c := range cases {
		slug := store.SlugifyBundleName(c.Name)
		if slug != c.Slug || ValidateSlug(slug) != c.Error {
			t.Errorf("%q: node %q %q, go %q %q", c.Name, c.Slug, c.Error, slug, ValidateSlug(slug))
		}
	}
}

func TestBuildTypesMatchNode(t *testing.T) {
	var cases []struct {
		Kind    string `json:"kind"`
		Input   any    `json:"input"`
		Missing bool   `json:"missing"`
		Out     any    `json:"out"`
		Queued  bool   `json:"queued"`
	}
	load(t, "build_types", &cases)
	for _, c := range cases {
		input, _ := goapi.FromGo(c.Input)
		switch c.Kind {
		case "product":
			if got := NormalizeProductType(input); got != c.Out {
				t.Errorf("product %#v: node %v, go %v", c.Input, c.Out, got)
			}
		case "status":
			text := c.Input.(string)
			if got := normalizeBuildStatus(text); got != c.Out || isQueuedBuildStatus(text) != c.Queued {
				t.Errorf("status %q: node %v %v, go %v %v", text, c.Out, c.Queued, got, isQueuedBuildStatus(text))
			}
		case "support":
			if got := IsSupportBundle(c.Input.(string)); got != c.Out {
				t.Errorf("support %q: node %v, go %v", c.Input, c.Out, got)
			}
		}
	}
}

func TestCustomTxtMatchesNode(t *testing.T) {
	var cases []struct {
		Opts    string `json:"opts"`
		JSON    string `json:"json"`
		Message string `json:"message"`
		Seed    string `json:"seed"`
		Signed  struct {
			Content string `json:"content"`
			Threw   string `json:"threw"`
		} `json:"signed"`
		Sign struct {
			Content string `json:"content"`
			Signed  bool   `json:"signed"`
		} `json:"sign"`
	}
	load(t, "custom_txt", &cases)
	signedCount := 0
	for i, c := range cases {
		raw, err := parseJSON(c.Opts)
		if err != nil {
			t.Fatal(err)
		}
		opt := func(name string) any { return undefinedToNil(prop(raw, name)) }
		o := CustomTxtOptions{AppName: opt("appName"), Host: opt("host"), Relay: opt("relay"), API: opt("api"), Key: opt("key")}
		if o2, _ := raw.(*goapi.Object); o2.Has("disableSettings") && prop(raw, "disableSettings") != "__undefined__" {
			o.DisableSettings, o.DisableSettingsSet = prop(raw, "disableSettings"), true
		}
		json := BuildSupportCustomTxt(o)
		if fixed, changed := withFixedRelay(c.JSON, "relay-server"); changed {
			// The texts made from it are checked by every other case
			if stringify(json) != fixed {
				t.Errorf("case %d json:\n want %s\n   go %s", i, fixed, stringify(json))
			}
			continue
		}
		if stringify(json) != c.JSON {
			t.Errorf("case %d json:\n node %s\n   go %s", i, c.JSON, stringify(json))
			continue
		}
		message := StableJSONBytes(json)
		// Node signed the default app name with upstream's names
		// (upstream_test.go); the same message with them must give Node's
		// bytes and Node's signature. Unsigned, the content is the text
		// itself, which the recording has with NeoDesk's names.
		nodeMessage := []byte(upstreamname.Names(string(message)))
		if base64.StdEncoding.EncodeToString(nodeMessage) != c.Message {
			t.Errorf("case %d message differs", i)
		}
		sign := SignCustomTxt(nodeMessage, c.Seed)
		if !sign.Signed {
			sign = SignCustomTxt(message, c.Seed)
		}
		if sign.Content != c.Sign.Content || sign.Signed != c.Sign.Signed {
			t.Errorf("case %d sign with seed %q: node %v %.40s, go %v %.40s", i, c.Seed, c.Sign.Signed, c.Sign.Content, sign.Signed, sign.Content)
		}
		want := c.Signed.Content
		if string(nodeMessage) != string(message) {
			want = SignCustomTxt(message, c.Seed).Content
		}
		got, _, err := BuildAndSignSupportCustomTxt(o, c.Seed)
		switch {
		case c.Signed.Threw != "":
			if err == nil || err.Error() != c.Signed.Threw {
				t.Errorf("case %d: node threw %q, go %v", i, c.Signed.Threw, err)
			}
		case err != nil || got.Content != want:
			t.Errorf("case %d: node %.40s, go %.40s %v", i, want, got.Content, err)
		default:
			signedCount++
		}
	}
	if signedCount < 100 {
		t.Errorf("only %d signed vectors", signedCount)
	}
}

func TestBase64AndKeysMatchNode(t *testing.T) {
	var cases []struct {
		Seed      string  `json:"seed"`
		Decoded   string  `json:"decoded"`
		PublicKey *string `json:"publicKey"`
	}
	load(t, "base64", &cases)
	keys := 0
	for _, c := range cases {
		got := nodeBase64Decode(c.Seed)
		if hex.EncodeToString(got) != c.Decoded {
			t.Errorf("Buffer.from(%q, 'base64'): node %s, go %x", c.Seed, c.Decoded, got)
		}
		if c.PublicKey != nil {
			keys++
			s := New(Options{Env: func() []string { return []string{"NEODESK_CUSTOM_CLIENT_SIGNING_SEED=" + c.Seed} }, DataDir: t.TempDir()})
			key, err := s.EnsureSigningKey()
			if err != nil {
				t.Fatal(err)
			}
			if decodeSigningSeed(c.Seed) != nil && key.PublicKey != *c.PublicKey {
				t.Errorf("public key of %q: node %s, go %s", c.Seed, *c.PublicKey, key.PublicKey)
			}
		}
	}
	if keys < 40 {
		t.Errorf("only %d key vectors", keys)
	}
}

// windowsInstallerAsFixed is a Windows installer Node wrote with the
// change of L293: the service and the tray shortcut have the client's
// name from custom.txt, NeoDesk Support Agent when a build gives none,
// where Node's took the name of the executable.
func windowsInstallerAsFixed(t *testing.T, node string) string {
	t.Helper()
	fixed := strings.NewReplacer(
		"$serviceName = $appName\n",
		"# The client names its service and its tray shortcut after the app name in\n"+
			"# custom.txt, given here in base64 (UTF-8)\n"+
			"$clientName = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('"+base64.StdEncoding.EncodeToString([]byte("NeoDesk Support Agent"))+"'))\n"+
			"$serviceName = $clientName\n",
		`Startup\$appName Tray.lnk"`, `Startup\$clientName Tray.lnk"`,
	).Replace(node)
	if strings.Count(fixed, "$clientName") != 3 {
		t.Fatalf("the Node installer has not the lines L293 changes:\n%s", node)
	}
	return fixed
}

func TestInstallerScriptsMatchNode(t *testing.T) {
	var cases []struct {
		Kind           string `json:"kind"`
		InstallService any    `json:"installService"`
		Autostart      any    `json:"autostart"`
		Text           string `json:"text"`
		Install        string `json:"install"`
		Uninstall      string `json:"uninstall"`
		Mode           string `json:"mode"`
	}
	load(t, "installers", &cases)
	for _, c := range cases {
		o := InstallerOptions{InstallService: truthyJSON(c.InstallService), Autostart: truthyJSON(c.Autostart)}
		switch {
		case c.Text != "":
			if got, want := WindowsInstallerScript(o), windowsInstallerAsFixed(t, c.Text); got != want {
				t.Errorf("windows %v %v differs", c.InstallService, c.Autostart)
			}
		default:
			dir := t.TempDir()
			if err := writeUnixInstallers(dir, c.Kind, o); err != nil {
				t.Fatal(err)
			}
			install, _ := os.ReadFile(filepath.Join(dir, "install-neodesk-support.sh"))
			uninstall, _ := os.ReadFile(filepath.Join(dir, "uninstall-neodesk-support.sh"))
			info, _ := os.Stat(filepath.Join(dir, "install-neodesk-support.sh"))
			if string(install) != c.Install || string(uninstall) != c.Uninstall || strconv.FormatUint(uint64(info.Mode().Perm()), 8) != c.Mode {
				t.Errorf("%s %v %v differs", c.Kind, c.InstallService, c.Autostart)
			}
		}
	}
}

func truthyJSON(v any) bool {
	if v == "__undefined__" {
		return false
	}
	g, _ := goapi.FromGo(v)
	return truthy(g)
}

func TestGenerationKeysMatchNode(t *testing.T) {
	var cases []struct {
		Fingerprint string `json:"fingerprint"`
		Commit      string `json:"commit"`
		Key         string `json:"key"`
	}
	load(t, "generation_keys", &cases)
	for _, c := range cases {
		if got := GenerationKey(c.Fingerprint, c.Commit); got != c.Key {
			t.Errorf("%s:%s: node %s, go %s", c.Fingerprint, c.Commit, c.Key, got)
		}
	}
}

// The luminance channels are what Math.pow gave; Go's math.Pow has to give
// the same bits for the contrast rule to decide alike.
func TestLuminanceMatchesNode(t *testing.T) {
	var bits []string
	load(t, "luminance", &bits)
	for i, want := range bits {
		got := strconv.FormatUint(math.Float64bits(luminanceChannel(uint64(i))), 16)
		for len(got) < 16 {
			got = "0" + got
		}
		if got != want {
			t.Errorf("channel %d: node %s, go %s", i, want, got)
		}
	}
}

func TestURLsMatchNode(t *testing.T) {
	var cases []struct {
		Input string `json:"input"`
		URL   *struct {
			Protocol string `json:"protocol"`
			Href     string `json:"href"`
		} `json:"url"`
	}
	load(t, "urls", &cases)
	for _, c := range cases {
		gotLikely := isLikelyURL(jsTrim(c.Input))
		wantLikely := c.URL != nil && (c.URL.Protocol == "http:" || c.URL.Protocol == "https:") && jsTrim(c.Input) == c.Input
		wantHref := ""
		if c.URL != nil && (c.URL.Protocol == "http:" || c.URL.Protocol == "https:") {
			wantHref = c.URL.Href
		}
		if got := HTTPURLOrEmpty(c.Input); got != wantHref {
			t.Errorf("%q: node %q, go %q", c.Input, wantHref, got)
		}
		if jsTrim(c.Input) == c.Input && gotLikely != wantLikely {
			t.Errorf("isLikelyUrl(%q): node %v, go %v", c.Input, wantLikely, gotLikely)
		}
	}
}

func TestJSONMatchesNode(t *testing.T) {
	var cases []struct {
		Text   string `json:"text"`
		Out    string `json:"out"`
		Pretty string `json:"pretty"`
	}
	load(t, "json", &cases)
	for _, c := range cases {
		v, err := parseJSON(c.Text)
		if err != nil {
			t.Errorf("%s: %v", c.Text, err)
			continue
		}
		if stringify(v) != c.Out || stringifyIndent(v) != c.Pretty {
			t.Errorf("%s:\n node %s / %s\n   go %s / %s", c.Text, c.Out, c.Pretty, stringify(v), stringifyIndent(v))
		}
	}
}
