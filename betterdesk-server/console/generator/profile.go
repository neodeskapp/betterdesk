package generator

import (
	"context"
	"io"
	"math"
	"regexp"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// What the generator routes do to a profile around ValidateBranding, and
// the JavaScript value helpers they share with this package.

// Parse is JSON.parse.
func Parse(text string) (any, error) { return parseJSON(text) }

// Stringify is JSON.stringify.
func Stringify(v any) string { return stringify(v) }

// Prop is v.name: an object's member, nil for anything else.
func Prop(v any, name string) any { return prop(v, name) }

// Or is a || b.
func Or(a, b any) any { return or(a, b) }

// String is String(v).
func String(v any) string { return jsString(v) }

// Trim is s.trim().
func Trim(s string) string { return jsTrim(s) }

// Slice is s.slice(0, n), counted in UTF-16 code units.
func Slice(s string, n int) string { return jsSlice(s, n) }

// enrollmentKeys never leave the console.
var enrollmentKeys = []string{"enrollment_token", "has_enrollment_token", "enrollment_token_masked"}

// PublicBrandingView is a profile without its enrollment fields; a value
// that is no object stays as it is.
func PublicBrandingView(branding any) any {
	if !truthy(branding) || !isObject(branding) {
		return branding
	}
	out := spread(goapi.NewObject(), branding)
	for _, key := range enrollmentKeys {
		out.Delete(key)
	}
	return out
}

// ParseStoredBranding is a bundle's branding column as the routes read
// it: the default profile for an empty column or text that is no JSON,
// the parsed value otherwise (which may be null).
func ParseStoredBranding(text string) any {
	if text == "" {
		return DefaultBranding()
	}
	v, err := parseJSON(text)
	if err != nil {
		return DefaultBranding()
	}
	return v
}

// PlatformList is platforms as a request lists them.
func PlatformList(platforms []Platform) []any { return platformList(platforms) }

// NewBundleID is 12 random hex digits.
func (s *Service) NewBundleID() (string, error) { return GenerateBundleID(s.opts.Rand) }

// FinalizeSupportBranding completes a profile for a Support build: the
// server's host (normalized, else as given), relay, API address and
// port, the server's public key, the names, and the marks of the
// template generator. The enrollment fields are removed. Branding colours
// and logos are not compiled into the client; the client asks for them.
func (s *Service) FinalizeSupportBranding(ctx context.Context, input any) *goapi.Object {
	branding := spread(goapi.NewObject(), or(input, goapi.NewObject()))
	si := s.opts.ServerInfo
	hostInput := or(or(branding.Get("server_host"), si.ResolveClientFacingHost(nil, "")), si.DefaultServerHost())
	hostText, _ := hostInput.(string)
	norm := serverinfo.NormalizeServerHost(hostText)
	host := norm.Host
	if !norm.Valid {
		host = jsTrim(jsString(or(branding.Get("server_host"), "")))
	}
	useHTTPS := truthy(nullish(branding.Get("use_https"), true))
	apiPort := jsString(or(branding.Get("api_port"), si.DefaultAPIPort()))
	scheme := "http"
	if useHTTPS {
		scheme = "https"
	}
	omitPort := (scheme == "https" && apiPort == "443") || (scheme == "http" && apiPort == "80")
	address := scheme + "://" + host + ":" + apiPort
	if omitPort {
		address = scheme + "://" + host
	}
	apiServer := or(branding.Get("api_server"), address)
	pubKey := or(or(string(si.ResolvePublicKey(ctx)), branding.Get("public_key")), "")

	branding.Set("server_host", host)
	branding.Set("relay_host", jsTrim(jsString(or(branding.Get("relay_host"), host))))
	branding.Set("api_server", apiServer)
	branding.Set("api_port", apiPort)
	branding.Set("use_https", useHTTPS)
	branding.Set("public_key", pubKey)
	branding.Set("server_key", pubKey)
	appName := jsTrim(jsString(or(or(branding.Get("app_name"), branding.Get("company_name")), "BetterDesk Support Agent")))
	if appName == "" {
		appName = "BetterDesk Support Agent"
	}
	branding.Set("app_name", appName)
	branding.Set("company_name", jsTrim(jsString(or(branding.Get("company_name"), appName))))
	branding.Set("product_name", appName)
	branding.Set("disable_settings", branding.Get("disable_settings") != false)
	branding.Set("sku", ProductSupport)
	branding.Set("generator_kind", ProductSupport)
	branding.Set("generator_version", float64(2))
	branding.Set("server", goapi.NewObject("address", address, "api_url", apiServer, "public_key", pubKey))
	for _, key := range enrollmentKeys {
		branding.Delete(key)
	}
	return branding
}

var sixHexDigits = regexp.MustCompile(`(?i)^#([0-9a-f]{6})$`)

// ReadableOnWhite reports a colour on which white text has a contrast of
// at least 4.5:1.
func ReadableOnWhite(hex any) bool {
	m := sixHexDigits.FindStringSubmatch(jsTrim(jsString(or(hex, ""))))
	if m == nil {
		return false
	}
	var ch [3]float64
	for i := 0; i < 3; i++ {
		n, _ := strconv.ParseUint(m[1][i*2:i*2+2], 16, 8)
		ch[i] = luminanceChannel(n)
	}
	lum := 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2]
	return 1.05/(lum+0.05) >= 4.5
}

// luminanceChannel is the linear value of an sRGB channel from 0 to 255,
// as JavaScript computed it (see luminanceBits).
func luminanceChannel(n uint64) float64 {
	return math.Float64frombits(luminanceBits[n&0xff])
}

// HTTPURLOrEmpty is the URL's normal form when it is an http or https
// address, "" otherwise.
func HTTPURLOrEmpty(value any) string {
	u, ok := ssrfguard.ParseURL(jsTrim(jsString(or(value, ""))))
	if !ok || (u.Protocol != "http:" && u.Protocol != "https:") {
		return ""
	}
	return u.Href
}

// Truthy is JavaScript's truthiness.
func Truthy(v any) bool { return truthy(v) }

// ArtifactDir is the folder of the finished installers.
func (s *Service) ArtifactDir() string { return s.opts.ArtifactDir }

// UseRandom replaces the source of bundle ids and new signing seeds. The
// tests that replay what the old routes answered use it to get the ids
// the recording has; nothing else calls it.
func (s *Service) UseRandom(r io.Reader) { s.opts.Rand = r }
