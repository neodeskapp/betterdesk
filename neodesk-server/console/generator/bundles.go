package generator

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// Platform is one installer the generator can deliver.
type Platform struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Format   string `json:"format"`
	Label    string `json:"label"`
}

// Key is platform/arch/format, how the builds are told apart.
func (p Platform) Key() string { return p.Platform + "/" + p.Arch + "/" + p.Format }

// Platforms is the catalogue of installers, in the order the download
// page shows them (agentBundleService.PLATFORMS).
var Platforms = []Platform{
	{"windows", "x64", "portable", "Windows x64 portable (.zip)"},
	{"windows", "arm64", "portable", "Windows ARM64 portable (.zip)"},
	{"linux", "x64", "portable", "Linux x64 portable (.tar.gz)"},
	{"linux", "x64", "deb", "Debian/Ubuntu x64 package (.deb)"},
	{"linux", "x64", "rpm", "Fedora/RHEL/openSUSE x64 package (.rpm)"},
	{"linux", "x64", "arch", "Arch Linux x64 package (.pkg.tar.zst)"},
	{"linux", "arm64", "portable", "Linux ARM64 portable (.tar.gz)"},
	{"linux", "arm64", "deb", "Debian/Ubuntu ARM64 package (.deb)"},
	{"linux", "arm64", "rpm", "Fedora/RHEL/openSUSE ARM64 package (.rpm)"},
	{"linux", "arm64", "arch", "Arch Linux ARM64 package (.pkg.tar.zst)"},
	{"macos", "x64", "portable", "macOS Intel portable (.tar.gz)"},
	{"macos", "arm64", "portable", "macOS Apple Silicon portable (.tar.gz)"},
}

// supportedLangs are the languages a profile may name as its default.
var supportedLangs = map[string]bool{
	"ar": true, "cs": true, "da": true, "de": true, "en": true, "es": true, "fi": true, "fr": true, "hi": true,
	"hu": true, "id": true, "it": true, "ja": true, "ko": true, "nb": true, "nl": true, "pl": true, "pt": true,
	"ro": true, "sv": true, "th": true, "tr": true, "uk": true, "vi": true, "zh": true, "zh-TW": true,
}

const (
	maxLogoBytes    = 10 * 1024 * 1024
	maxShortText    = 500
	maxName         = 100
	maxContact      = 200
	maxProductLabel = 200
	maxSlugLength   = 32
	minSlugLength   = 2
)

// portalOnlyKeys are stored with a profile but left out of its hash, so
// that changing the download page's text rebuilds nothing.
var portalOnlyKeys = []string{"product_label", "hide_product_type"}

var (
	hexColor       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	schemePrefix   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	slugPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)
	underscoreWord = regexp.MustCompile(`_([a-z])`)
)

// clip is the profile's text rule: a string trimmed and cut to max UTF-16
// code units, anything that is not a string "".
func clip(v any, max int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return jsSlice(jsTrim(s), max)
}

func isDataURLImage(v any) bool {
	s, ok := v.(string)
	if !ok || s == "" || !strings.HasPrefix(s, "data:image/") || !strings.Contains(s, ";base64,") {
		return false
	}
	return len(s) <= maxLogoBytes
}

func isLikelyURL(s string) bool {
	if s == "" {
		return false
	}
	u, ok := ssrfguard.ParseURL(s)
	return ok && (u.Protocol == "http:" || u.Protocol == "https:")
}

// Validation is what ValidateBranding makes of a profile.
type Validation struct {
	Valid  bool
	Errors []string
	// Normalized is the cleaned profile, an object with its keys in the
	// order the old service set them.
	Normalized *goapi.Object
}

// ValidateBranding cleans a profile an operator sent: texts trimmed and
// cut to their length, colours checked and in lower case, unknown
// languages replaced by English, the server's host read with
// serverinfo.NormalizeServerHost, the relay as host:port with relayPort
// when it has no port (nil: 21117). Errors lists what was wrong, in the
// order the checks ran; Normalized holds defaults in place of the wrong
// values (agentBundleService.validateBranding).
//
// The error is the TypeError the old service threw for a null profile.
func ValidateBranding(input any, relayPort func() string) (Validation, error) {
	if input == nil {
		return Validation{}, typeError("Cannot read properties of null (reading 'app_name')")
	}
	var errs []string
	out := goapi.NewObject()
	get := func(name string) any { return prop(input, name) }

	appName := clip(or(or(or(get("app_name"), get("appName")), get("company_name")), get("companyName")), maxName)
	out.Set("app_name", appName)
	companyName := clip(or(or(get("company_name"), get("companyName")), appName), maxName)
	out.Set("company_name", companyName)
	if companyName == "" {
		companyName = "BetterDesk Support Agent"
		out.Set("company_name", companyName)
	}
	if appName == "" {
		out.Set("app_name", companyName)
	}
	// The relay is kept as host:port: a bare host makes RustDesk 1.4.8 and
	// 1.4.9 clients ask the API port for the relay
	out.Set("relay_host", serverinfo.RelayEndpoint(clip(or(or(or(get("relay_host"), get("relayHost")), get("relay_server")), ""), 253), "", relayPortOr(relayPort)))
	out.Set("api_server", clip(or(or(get("api_server"), get("apiServer")), ""), maxContact))
	out.Set("api_port", clip(jsString(or(or(get("api_port"), get("apiPort")), "")), 8))
	out.Set("disable_settings", get("disable_settings") != false && get("disableSettings") != false)

	out.Set("short_text", clip(or(get("short_text"), get("shortText")), maxShortText))
	applyPortalProductFields(input, out)
	out.Set("contact_email", clip(or(get("contact_email"), get("contactEmail")), maxContact))
	out.Set("contact_phone", clip(or(get("contact_phone"), get("contactPhone")), maxContact))
	contactURL := clip(or(get("contact_url"), get("contactUrl")), maxContact)
	out.Set("contact_url", contactURL)
	if contactURL != "" {
		if !schemePrefix.MatchString(contactURL) {
			if candidate := "https://" + contactURL; isLikelyURL(candidate) {
				contactURL = candidate
				out.Set("contact_url", contactURL)
			}
		}
		if !isLikelyURL(contactURL) {
			errs = append(errs, "contact_url_invalid")
		}
	}

	logo := or(or(get("logo_data_url"), get("logoDataUrl")), "")
	if truthy(logo) {
		if !isDataURLImage(logo) {
			errs = append(errs, "logo_invalid")
			out.Set("logo_data_url", "")
		} else {
			out.Set("logo_data_url", logo)
		}
	} else {
		out.Set("logo_data_url", "")
	}

	// A colour is text: a list, also one that holds a valid colour, is an
	// invalid colour like any other value.
	checkColor := func(key string, value any, fallback, errKey string) {
		color, isText := value.(string)
		if !isText || !hexColor.MatchString(color) {
			errs = append(errs, errKey)
			color = fallback
		}
		out.Set(key, strings.ToLower(color))
	}
	checkColor("primary_color", or(or(get("primary_color"), get("primaryColor")), "#2563eb"), "#2563eb", "primary_color_invalid")
	checkColor("accent_color", or(or(get("accent_color"), get("accentColor")), "#e0f2fe"), "#e0f2fe", "accent_color_invalid")
	for _, field := range [][2]string{
		{"background_color", "#ffffff"},
		{"surface_color", "#f3f4f6"},
		{"text_color", "#1f2937"},
		{"text_muted_color", "#6b7280"},
		{"status_ready_color", "#22c55e"},
		{"header_text_color", "#1f2937"},
	} {
		key, fallback := field[0], field[1]
		alt := underscoreWord.ReplaceAllStringFunc(key, func(m string) string { return strings.ToUpper(m[1:]) })
		checkColor(key, or(or(get(key), get(alt)), fallback), fallback, key+"_invalid")
	}

	out.Set("allow_unattended", truthy(nullish(nullish(get("allow_unattended"), get("allowUnattended")), false)))
	installService := truthy(nullish(nullish(get("install_service"), get("installService")), false))
	out.Set("install_service", installService)
	autostart := truthy(nullish(nullish(get("autostart"), get("autoStart")), false))
	out.Set("autostart", autostart)
	if autostart {
		out.Set("install_service", true)
	}

	var capsIn any = goapi.NewObject()
	if caps := get("capabilities"); truthy(caps) && isObject(caps) {
		capsIn = caps
	}
	capability := func(name string) bool {
		v := prop(capsIn, name)
		if v == nil {
			return true
		}
		return truthy(v)
	}
	out.Set("capabilities", goapi.NewObject(
		"desktop", capability("desktop"),
		"files", capability("files"),
		"clipboard", capability("clipboard"),
		"audio", capability("audio"),
		"terminal", capability("terminal"),
		"restart", capability("restart"),
	))

	lang := jsString(or(or(get("default_lang"), get("defaultLang")), "en"))
	if !supportedLangs[lang] {
		lang = "en"
	}
	out.Set("default_lang", lang)

	host := clip(or(get("server_host"), get("serverHost")), 253)
	out.Set("server_host", host)
	if host != "" {
		norm := serverinfo.NormalizeServerHost(host)
		if !norm.Valid {
			errs = append(errs, norm.Error)
		} else {
			out.Set("server_host", norm.Host)
		}
	} else {
		errs = append(errs, "server_host_required")
	}
	out.Set("use_https", truthy(nullish(nullish(get("use_https"), get("useHttps")), true)))

	if server := get("server"); truthy(server) && isObject(server) {
		out.Set("server", goapi.NewObject(
			"address", clip(or(prop(server, "address"), ""), maxContact),
			"api_url", clip(or(prop(server, "api_url"), ""), maxContact),
			"public_key", clip(or(prop(server, "public_key"), ""), 256),
		))
	} else {
		out.Set("server", goapi.NewObject("address", "", "api_url", "", "public_key", ""))
	}
	if errs == nil {
		errs = []string{}
	}
	return Validation{Valid: len(errs) == 0, Errors: errs, Normalized: out}, nil
}

// typeError is a TypeError the old code threw.
type typeError string

func (e typeError) Error() string { return string(e) }

func applyPortalProductFields(input any, out *goapi.Object) {
	out.Set("product_label", clip(or(or(prop(input, "product_label"), prop(input, "productLabel")), ""), maxProductLabel))
	out.Set("hide_product_type", truthy(nullish(nullish(prop(input, "hide_product_type"), prop(input, "hideProductType")), false)))
}

// HashBranding is the build cache key of a profile: the SHA-256 in hex of
// the profile written with sorted keys, without the download page's own
// fields. Bundles with the same hash share their installers.
func HashBranding(normalized *goapi.Object) string {
	forHash := normalized.Clone()
	if forHash == nil {
		forHash = goapi.NewObject()
	}
	for _, key := range portalOnlyKeys {
		forHash.Delete(key)
	}
	sum := sha256.Sum256([]byte(stableStringify(forHash)))
	return hex.EncodeToString(sum[:])
}

// GenerateBundleID returns 12 random hex digits.
func GenerateBundleID(random io.Reader) (string, error) {
	b := make([]byte, 6)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// The slugs themselves are made by store.SlugifyBundleName and
// store.AllocateBundleSlug, which the schema migration shares.

// ValidateSlug checks an address part: "" when it is fine, else
// slug_invalid, slug_too_short or slug_too_long.
func ValidateSlug(slug string) string {
	switch {
	case slug == "":
		return "slug_invalid"
	case jsLength(slug) < minSlugLength:
		return "slug_too_short"
	case jsLength(slug) > maxSlugLength:
		return "slug_too_long"
	case !slugPattern.MatchString(slug):
		return "slug_invalid"
	}
	return ""
}

// PublicBundleID is the address part of a bundle: its slug, else its id.
func PublicBundleID(slug, bundleID string) string {
	if slug != "" {
		return slug
	}
	return bundleID
}

// DefaultBranding is the profile the editor starts from, and what a row
// whose branding column cannot be read shows.
func DefaultBranding() *goapi.Object {
	return goapi.NewObject(
		"app_name", "BetterDesk Support Agent",
		"company_name", "",
		"short_text", "",
		"product_label", "",
		"hide_product_type", false,
		"contact_email", "",
		"contact_phone", "",
		"contact_url", "",
		"logo_data_url", "",
		"primary_color", "#2563eb",
		"accent_color", "#e0f2fe",
		"background_color", "#ffffff",
		"surface_color", "#f3f4f6",
		"text_color", "#1f2937",
		"text_muted_color", "#6b7280",
		"status_ready_color", "#22c55e",
		"header_text_color", "#1f2937",
		"allow_unattended", false,
		"install_service", false,
		"autostart", false,
		"disable_settings", true,
		"capabilities", goapi.NewObject("desktop", true, "files", true, "clipboard", true, "audio", true, "terminal", true, "restart", true),
		"default_lang", "en",
		"server_host", "",
		"relay_host", "",
		"api_server", "",
		"api_port", "",
		"use_https", true,
		"public_key", "",
		"server", goapi.NewObject("address", "", "api_url", "", "public_key", ""),
	)
}

// randomReader is the source of bundle ids when Options.Rand is nil.
var randomReader io.Reader = rand.Reader
