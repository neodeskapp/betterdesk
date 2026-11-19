package generator

import (
	"net/http"
	"strings"
	"unicode/utf16"

	gen "github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// The rustdesk.toml generator the console had before the Support
// generator. Any signed-in account may use it.

type legacyConfigAnswer struct {
	Success bool             `json:"success"`
	Data    legacyConfigData `json:"data"`
}

type legacyConfigData struct {
	PublicKey serverinfo.NullString `json:"publicKey"`
	ServerURL string                `json:"serverUrl"`
	Defaults  legacyDefaults        `json:"defaults"`
}

type legacyDefaults struct {
	RendezvousServer string `json:"rendezvousServer"`
	APIServer        string `json:"apiServer"`
	Key              string `json:"key"`
}

// legacyConfig answers GET /api/generator/config: the server's public key
// and the API address without its first "/api".
func (h *handlers) legacyConfig(w http.ResponseWriter, r *http.Request) {
	key := h.serverInfo.ResolvePublicKey(r.Context())
	kit.JSON(w, r, http.StatusOK, legacyConfigAnswer{Success: true, Data: legacyConfigData{
		PublicKey: key,
		ServerURL: strings.Replace(h.apiURL, "/api", "", 1),
		Defaults:  legacyDefaults{Key: string(key)},
	}})
}

type generatedConfig struct {
	Success bool                `json:"success"`
	Data    generatedConfigData `json:"data"`
}

type generatedConfigData struct {
	Config   string `json:"config"`
	FileName string `json:"fileName"`
}

// generateConfig answers POST /api/generator/generate-config: the lines
// of a rustdesk.toml from serverHost, serverPort (21116), relayHost,
// relayPort and clientName, with the server's key. The relay line always
// has a port: the one in relayHost, else relayPort, else the server's relay
// port (relayEndpoint).
func (h *handlers) generateConfig(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	serverHost := prop(body, "serverHost")
	if !gen.Truthy(serverHost) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "Server host is required"})
		return
	}
	key := h.serverInfo.ResolvePublicKey(r.Context())
	lines := []string{"rendezvous_server = " + jsString(serverHost) + ":" + jsString(or(prop(body, "serverPort"), float64(21116)))}
	if relay := prop(body, "relayHost"); gen.Truthy(relay) {
		if endpoint := h.relayEndpoint(relay, prop(body, "relayPort")); endpoint != "" {
			lines = append(lines, "relay_server = "+endpoint)
		}
	}
	if key != "" {
		lines = append(lines, "key = "+string(key))
	}
	if name := prop(body, "clientName"); gen.Truthy(name) {
		lines = append(lines, "name = "+jsString(name))
	}
	kit.JSON(w, r, http.StatusOK, generatedConfig{Success: true, Data: generatedConfigData{
		Config:   strings.Join(lines, "\n"),
		FileName: "rustdesk.toml",
	}})
}

// relayEndpoint is serverinfo.RelayEndpoint for request values of any JSON
// type: an address that is not text has none, and a port that is truthy is
// taken as String() makes it, even when that is "" (String([])).
func (h *handlers) relayEndpoint(host, port any) string {
	address, _ := host.(string)
	defaultPort := h.serverInfo.DefaultRelayPort
	if gen.Truthy(port) {
		text := jsString(port)
		defaultPort = func() string { return text }
	}
	return serverinfo.RelayEndpoint(address, "", defaultPort)
}

// contentDisposition is the Content-Disposition of a download under name
// (the content-disposition package Express used): attachment;
// filename="..." for printable Latin-1 names, with filename* added (or in
// its place) for other names and for names with a %XX in them.
func contentDisposition(name string) string {
	us := utf16.Encode([]rune(name))
	quotable := len(us) > 0
	fallback := make([]byte, len(us))
	hasFallback := false
	for i, u := range us {
		if !(u >= 0x20 && u <= 0x7e || u >= 0x80 && u <= 0xff) {
			quotable = false
		}
		if u >= 0x20 && u <= 0x7e || u >= 0xa0 && u <= 0xff {
			fallback[i] = byte(u)
		} else {
			fallback[i], hasFallback = '?', true
		}
	}
	var b strings.Builder
	b.WriteString("attachment")
	if quotable || hasFallback {
		b.WriteString(`; filename="`)
		for _, c := range fallback {
			if c == '\\' || c == '"' {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
		b.WriteByte('"')
	}
	if hasFallback || !quotable || hasHexEscape(name) {
		b.WriteString("; filename*=UTF-8''")
		const upperhex = "0123456789ABCDEF"
		for i := 0; i < len(name); i++ {
			c := name[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~", c) >= 0 {
				b.WriteByte(c)
				continue
			}
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
	return b.String()
}

func hasHexEscape(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			return true
		}
	}
	return false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
