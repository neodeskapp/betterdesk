package branding

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file replay vectors recorded from the console's Node
// modules (testdata/nodeoracle). Each vector is the arguments of a call
// and what Node answered: the JSON text of the value, or the message of
// the error. The Go code must answer the same, byte for byte.

// loadVectors reads a recorded file with this package's own JSON reader,
// so text with broken surrogate pairs arrives as Node had it.
func loadVectors(t testing.TB, name string) any {
	t.Helper()
	dir := "testdata"
	if trial := os.Getenv("BRANDING_VECTORS_DIR"); trial != "" {
		// a trial run of the oracle, see testdata/nodeoracle/README.md
		dir = trial
	}
	file, err := os.Open(filepath.Join(dir, name+".json.gz"))
	if err != nil {
		t.Fatalf("open vectors: %v", err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("unzip %s: %v", name, err)
	}
	body, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	data, err := parseJSON(string(body))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return data
}

// arg turns a recorded argument back into a value: the marker object
// stands for undefined.
func arg(v any) any {
	if o, ok := v.(*Object); ok && o.Len() == 1 && o.Has("$undefined") {
		return Undefined
	}
	return v
}

func args(vector any) []any {
	list, _ := property(vector, "a").([]any)
	out := make([]any, len(list))
	for i, v := range list {
		out[i] = arg(v)
	}
	return out
}

// argAt returns argument i, and Undefined when the call had fewer.
func argAt(a []any, i int) any {
	if i < len(a) {
		return a[i]
	}
	return Undefined
}

func textArg(a []any, i int) string {
	s, _ := argAt(a, i).(string)
	return s
}

// outcome describes what a call did, in the words of a vector.
func outcome(value any, err error) string {
	if err != nil {
		name := "Error"
		var e *Error
		if errors.As(err, &e) {
			name = e.Name
		}
		return "throws " + name + ": " + Message(err)
	}
	if value == Undefined {
		return "undefined"
	}
	return Stringify(value)
}

// expected is the recorded outcome of a vector. hashed is true when the
// file holds only the SHA-256 of a long text.
func expected(vector any) (want string, hashed bool) {
	if e := property(vector, "e"); e != Undefined {
		return "throws " + toString(property(vector, "n")) + ": " + toString(e), false
	}
	if property(vector, "u") == true {
		return "undefined", false
	}
	switch r := property(vector, "r").(type) {
	case string:
		return r, false
	case *Object:
		return r.Text("sha256"), true
	}
	return "<vector without outcome>", false
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(wellFormed(text)))
	return hex.EncodeToString(sum[:])
}

// sameOutcome compares what Go did with a recorded outcome.
func sameOutcome(got string, vector any) (want string, same bool) {
	want, hashed := expected(vector)
	if hashed {
		return "sha256 " + want, sha256Hex(got) == want
	}
	return want, got == want
}

func clip(s string) string {
	if len(s) > 1500 {
		return s[:700] + " ... " + s[len(s)-700:]
	}
	return s
}

// oracleService is a service without a database, for the functions that
// only need the built-in palettes.
func oracleService(t testing.TB) *Service {
	t.Helper()
	s, err := New(fixtureOptions(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fixtureOptions points a service at the copies of the console's files in
// testdata/console and at folders below dir for everything it writes.
func fixtureOptions(dir string) Options {
	console := filepath.Join("testdata", "console")
	return Options{
		UploadsDir:     filepath.Join(dir, "data", "uploads"),
		FontsDir:       filepath.Join(dir, "public", "fonts"),
		FontCacheFile:  filepath.Join(dir, "data", "google-fonts-cache.json"),
		TempDir:        filepath.Join(dir, "data", "tmp"),
		ThemesDir:      filepath.Join(console, "themes"),
		PalettesFile:   filepath.Join(console, "config", "theme-palettes.json"),
		FaviconFile:    filepath.Join(console, "public", "favicon.svg"),
		SupportersFile: filepath.Join(console, "config", "supporters.json"),
		Logf:           func(string, ...any) {},
	}
}

// orNull is the JavaScript value of a result that is a text or null.
func orNull(text string, ok bool) any {
	if !ok {
		return nil
	}
	return text
}

// pureCalls maps the name of a recorded function to the Go call.
var pureCalls = map[string]func(s *Service, a []any) (any, error){
	"sanitizeSvg": func(_ *Service, a []any) (any, error) { return SanitizeSVG(argAt(a, 0)), nil },
	"validateBrandingUrl": func(_ *Service, a []any) (any, error) {
		url, ok, err := ValidateBrandingURL(argAt(a, 0))
		return orNull(url, ok), err
	},
	"sanitizeCssColorValue": func(_ *Service, a []any) (any, error) { return SanitizeCSSColorValue(argAt(a, 0)) },
	"sanitizeCustomCss":     func(_ *Service, a []any) (any, error) { return SanitizeCustomCSS(argAt(a, 0)) },
	"buildBackgroundValue": func(_ *Service, a []any) (any, error) {
		return BuildBackgroundValue(argAt(a, 0), argAt(a, 1), argAt(a, 2), argAt(a, 3))
	},
	"normalizeBackgroundSize": func(_ *Service, a []any) (any, error) { return NormalizeBackgroundSize(argAt(a, 0)) },
	"normalizeLoginPromoMode": func(_ *Service, a []any) (any, error) { return NormalizeLoginPromoMode(argAt(a, 0)) },
	"normalizeThemeMode":      func(_ *Service, a []any) (any, error) { return NormalizeThemeMode(argAt(a, 0)) },
	"resolveLoginPromoMode": func(_ *Service, a []any) (any, error) {
		return ResolveLoginPromoMode(argAt(a, 0), argAt(a, 2) == true)
	},
	"sanitizeLoginSlides":    func(_ *Service, a []any) (any, error) { return SanitizeLoginSlides(argAt(a, 0)) },
	"flattenAppearanceInput": func(_ *Service, a []any) (any, error) { return FlattenAppearanceInput(argAt(a, 0)) },
	"resolveThemeColors":     func(s *Service, a []any) (any, error) { return s.ResolveThemeColors(argAt(a, 0)) },
	"hexToMutedRgba": func(_ *Service, a []any) (any, error) {
		alpha := 0.15
		if n, ok := argAt(a, 1).(float64); ok {
			alpha = n
		}
		text, ok, err := HexToMutedRGBA(argAt(a, 0), alpha)
		return orNull(text, ok), err
	},
	"usesDefaultLogo":             func(_ *Service, a []any) (any, error) { return UsesDefaultLogo(argAt(a, 0)) },
	"getAppearanceModel":          func(s *Service, a []any) (any, error) { return s.GetAppearanceModel(argAt(a, 0)) },
	"assessAppearanceReadability": func(s *Service, a []any) (any, error) { return s.AssessAppearanceReadability(argAt(a, 0)) },
	"sanitizeFontName":            func(_ *Service, a []any) (any, error) { return SanitizeFontName(argAt(a, 0)) },
	"escapeCssString":             func(_ *Service, a []any) (any, error) { return EscapeCSSString(argAt(a, 0)) },
}

// TestOraclePure replays the functions that need no database.
func TestOraclePure(t *testing.T) {
	fixed := replayCalls(t, "pure", 30000)
	for bug, atLeast := range map[string]int{"BR1": 60, "BR7": 3, "BR13": 2600} {
		if fixed[bug] < atLeast {
			t.Errorf("%d calls answer as the fix of %s says, the recording has more", fixed[bug], bug)
		}
	}
}

// TestOracleHosts replays URLs whose host has characters outside ASCII
// or is written in Punycode.
func TestOracleHosts(t *testing.T) {
	replayCalls(t, "idna", 20000)
}

// replayCalls replays a file of recorded calls: for each function its
// list of vectors. A call whose answer a fix changes (fixedPure) is
// counted by bug instead of compared.
func replayCalls(t *testing.T, file string, atLeast int) map[string]int {
	data := loadVectors(t, file)
	s := oracleService(t)
	total := 0
	fixed := map[string]int{}
	for _, name := range data.(*Object).Keys() {
		if name == "constants" {
			continue
		}
		call, known := pureCalls[name]
		if !known {
			t.Errorf("no Go call for the recorded function %s", name)
			continue
		}
		vectors, _ := property(data, name).([]any)
		if len(vectors) == 0 {
			t.Errorf("%s: no vectors", name)
		}
		t.Run(name, func(t *testing.T) {
			failures := 0
			for i, vector := range vectors {
				a := args(vector)
				value, err := call(s, a)
				got := outcome(value, err)
				if want, same := sameOutcome(got, vector); !same {
					if bug := fixedPure(t, name, a, got, want); bug != "" {
						fixed[bug]++
						continue
					}
					failures++
					if failures <= maxShown {
						t.Errorf("vector %d: %s(%s)\n got  %s\n want %s", i, name, clip(Stringify(a)), clip(got), clip(want))
					}
				}
			}
			if failures > maxShown {
				t.Errorf("%d of %d vectors differ", failures, len(vectors))
			}
		})
		total += len(vectors)
	}
	if total < atLeast {
		t.Errorf("replayed %d vectors, the recorded file should hold at least %d", total, atLeast)
	}
	return fixed
}

// TestOracleConstants compares the fixed values the module exports.
func TestOracleConstants(t *testing.T) {
	constants := property(loadVectors(t, "pure"), "constants")
	s := oracleService(t)
	for name, got := range map[string]string{
		"DEFAULT_BRANDING":              Stringify(DefaultBranding()),
		"COLOR_TO_CSS_VAR":              Stringify(ColorToCSSVar()),
		"BUILTIN_THEME_PALETTES":        Stringify(s.GetThemePalettes()),
		"getThemePalettes":              Stringify(s.GetThemePalettes()),
		"getDefaultLoginSlides":         Stringify(DefaultLoginSlides()),
		"MAX_LOGIN_SLIDES":              Stringify(float64(MaxLoginSlides)),
		"LEGACY_DEFAULT_LOGO_URL":       LegacyDefaultLogoURL,
		"CURATED_FONTS":                 Stringify(CuratedFonts()),
		"getBrandingBeforeLoad":         Stringify(s.GetBranding()),
		"getBrandingRevisionBeforeLoad": s.Revision(),
		"luminance":                     luminanceHex(),
	} {
		if want := toString(property(constants, name)); got != want {
			t.Errorf("%s\n got  %s\n want %s", name, clip(got), clip(want))
		}
	}
}

// luminanceHex is the luminance table as the oracle records it: the bits
// of the 256 values in hex, separated by spaces.
func luminanceHex() string {
	parts := make([]string, len(luminanceComponent))
	for i, value := range luminanceComponent {
		parts[i] = fmt.Sprintf("%016x", math.Float64bits(value))
	}
	return strings.Join(parts, " ")
}

// maxShown is how many differing vectors of one function are printed.
var maxShown = func() int {
	if os.Getenv("BRANDING_SHOW_ALL") != "" {
		return 1 << 30
	}
	return 15
}()
