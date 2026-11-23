// Package keys answers the server's public key and API key, the
// addresses a client is given, and the QR code a mobile client scans to
// configure itself.
package keys

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// noAPIKey stands in for the masked API key when there is none: eight
// bullets.
const noAPIKey = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"

type handlers struct {
	info *serverinfo.Service
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{info: d.ServerInfo}
	r := d.Router
	r.Get("/api/keys/public", h.publicKey, d.RequireAuth)
	r.Get("/api/keys/public/qr", h.configQR, d.RequireAuth)
	r.Get("/api/keys/public/download", h.download, d.RequireAuth)
	r.Get("/api/keys/api", h.apiKey, d.RequireAuth)
	r.Get("/api/keys/server-info", h.serverInfo, d.RequireAuth)
	r.Get("/api/keys/qr", h.configQR, d.RequireAuth)
}

// endpoints are the addresses for the client that sent r: the host it
// asks for with ?host= when it is a valid one, else what the console is
// configured with.
func (h *handlers) endpoints(r *http.Request) serverinfo.ResolvedEndpoints {
	return h.info.ResolveRustDeskEndpoints(serverinfo.RequestHostOf(r), serverinfo.QueryString(r, "host"))
}

type keyData struct {
	Key string `json:"key"`
}

// publicKey answers GET /api/keys/public: the server's public key.
func (h *handlers) publicKey(w http.ResponseWriter, r *http.Request) {
	key := h.info.ResolvePublicKey(r.Context())
	if key == "" {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "keys.not_found", nil))
		return
	}
	kit.OK(w, r, keyData{Key: string(key)})
}

type configQRData struct {
	QR                   string `json:"qr"`
	ServerID             string `json:"server_id"`
	RelayServer          string `json:"relay_server"`
	APIURL               string `json:"api_url"`
	PhoneUnreachableHost bool   `json:"phone_unreachable_host"`
}

// configQR answers GET /api/keys/public/qr and GET /api/keys/qr: the
// client configuration as a QR code, with the addresses it holds.
func (h *handlers) configQR(w http.ResponseWriter, r *http.Request) {
	endpoints := h.endpoints(r)
	code := h.info.GetServerConfigQR(r.Context(), endpoints.Endpoints())
	if code == "" {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "keys.not_found", nil))
		return
	}
	kit.OK(w, r, configQRData{
		QR:                   string(code),
		ServerID:             endpoints.Host,
		RelayServer:          endpoints.Relay,
		APIURL:               endpoints.API,
		PhoneUnreachableHost: serverinfo.IsPhoneUnreachableHost(endpoints.Host),
	})
}

// download answers GET /api/keys/public/download: the public key as the
// file id_ed25519.pub.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	key := h.info.ResolvePublicKey(r.Context())
	if key == "" {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "keys.not_found", nil))
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="id_ed25519.pub"`)
	httpx.WriteBody(w, r, http.StatusOK, "text/plain; charset=utf-8", []byte(key))
}

type apiKeyData struct {
	Key    string `json:"key"`
	Masked bool   `json:"masked"`
}

// apiKey answers GET /api/keys/api: the API key with its middle masked.
// ?show=true asks for the whole key, which acts as an administrator on
// the server, so it needs the permission server.keys.
func (h *handlers) apiKey(w http.ResponseWriter, r *http.Request) {
	show := serverinfo.QueryString(r, "show") == "true"
	if show && !kit.HasPermission(r, "server.keys") {
		kit.Fail(w, r, http.StatusForbidden, "Permission denied: server.keys")
		return
	}
	key, _ := h.info.GetAPIKey(!show)
	if key == "" {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "keys.api_not_found", nil))
		return
	}
	kit.OK(w, r, apiKeyData{Key: key, Masked: !show})
}

type serverInfoData struct {
	ServerID          string                     `json:"server_id"`
	RelayServer       string                     `json:"relay_server"`
	APIURL            string                     `json:"api_url"`
	APIKeyMasked      string                     `json:"api_key_masked"`
	EndpointSources   serverinfo.EndpointSources `json:"endpoint_sources"`
	EnvOverrideActive bool                       `json:"env_override_active"`
}

// serverInfo answers GET /api/keys/server-info: the addresses a client is
// given, where each came from, and the masked API key.
func (h *handlers) serverInfo(w http.ResponseWriter, r *http.Request) {
	endpoints := h.endpoints(r)
	key, _ := h.info.GetAPIKey(true)
	if key == "" {
		key = noAPIKey
	}
	kit.OK(w, r, serverInfoData{
		ServerID:          endpoints.Host,
		RelayServer:       endpoints.Relay,
		APIURL:            endpoints.API,
		APIKeyMasked:      key,
		EndpointSources:   endpoints.Sources,
		EnvOverrideActive: endpoints.EnvOverrideActive,
	})
}
