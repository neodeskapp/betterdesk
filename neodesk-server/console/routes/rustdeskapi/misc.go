package rustdeskapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// software answers GET /api/software and
// GET /api/software/client-download-link: no update, no link.
func software(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, struct{}{})
}

type serverKeyAnswer struct {
	Key string `json:"key"`
}

// serverKey answers GET /api/server-key with the server's public key, ""
// without one. It is public by design.
func (h *handlers) serverKey(w http.ResponseWriter, r *http.Request) {
	key := h.d.ServerInfo.ResolvePublicKey(r.Context())
	kit.JSON(w, r, http.StatusOK, serverKeyAnswer{Key: string(key)})
}

type fingerprintAnswer struct {
	Fingerprint string `json:"fingerprint"`
	Algorithm   string `json:"algorithm"`
}

// fingerprint answers GET /api/server-key/fingerprint: the SHA-256 of the
// key's bytes, as pairs of upper-case hex digits joined by colons.
func (h *handlers) fingerprint(w http.ResponseWriter, r *http.Request) {
	key := h.d.ServerInfo.ResolvePublicKey(r.Context())
	if string(key) == "" {
		kit.JSON(w, r, http.StatusOK, fingerprintAnswer{Algorithm: "SHA-256"})
		return
	}
	sum := sha256.Sum256(decodeBase64Loose(string(key)))
	digits := strings.ToUpper(hex.EncodeToString(sum[:]))
	pairs := make([]string, 0, len(digits)/2)
	for i := 0; i+1 < len(digits); i += 2 {
		pairs = append(pairs, digits[i:i+2])
	}
	kit.JSON(w, r, http.StatusOK, fingerprintAnswer{Fingerprint: strings.Join(pairs, ":"), Algorithm: "SHA-256"})
}

// decodeBase64Loose is Buffer.from(text, 'base64'): both alphabets,
// characters outside them skipped, the first "=" ends the data, and bits
// that make no whole byte are dropped.
func decodeBase64Loose(text string) []byte {
	var out []byte
	var acc, bits uint
	for _, c := range text {
		var v uint
		switch {
		case c >= 'A' && c <= 'Z':
			v = uint(c - 'A')
		case c >= 'a' && c <= 'z':
			v = uint(c-'a') + 26
		case c >= '0' && c <= '9':
			v = uint(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			return out
		default:
			continue
		}
		acc = acc<<6 | v
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	return out
}

// hopByHop are the headers the pass-through does not hand on.
var hopByHop = []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade"}

// recorded is the answer of the API server to a passed-through request.
type recorded struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (rec *recorded) Header() http.Header { return rec.header }
func (rec *recorded) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
}
func (rec *recorded) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.body.Write(b)
}

// telemetryKey answers GET /api/telemetry/key, the key NeoDesk clients
// fetch before they sign in, with what the server's API answers to the
// same request (middleware/goApiProxy.js): its status, its headers over
// the console's and its body.
func (h *handlers) telemetryKey(w http.ResponseWriter, r *http.Request) {
	forward := r.Clone(r.Context())
	for _, name := range hopByHop {
		forward.Header.Del(name)
	}
	client := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if client == "" {
		client = r.Header.Get("X-Real-Ip")
	}
	if client == "" {
		client = kit.ClientIP(r)
	}
	if client != "" {
		if prior := r.Header.Get("X-Forwarded-For"); prior != "" {
			forward.Header.Set("X-Forwarded-For", prior+", "+client)
		} else {
			forward.Header.Set("X-Forwarded-For", client)
		}
		forward.Header.Set("X-Real-Ip", client)
	}
	forward.Body = http.NoBody
	forward.ContentLength = 0
	rec := &recorded{header: http.Header{}}
	h.d.GoAPI.Handler().ServeHTTP(rec, forward)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	for name, values := range rec.header {
		if strings.EqualFold(name, "Transfer-Encoding") {
			continue
		}
		w.Header()[name] = values
	}
	w.WriteHeader(rec.status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(rec.body.Bytes())
	}
}
