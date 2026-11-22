package bdapi

import (
	"container/list"
	"crypto/ed25519"
	"encoding/base64"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The Ed25519 proof of possession a device can send with its registration
// (lib/enrollmentProof.js), the same challenge the API server's
// /api/devices/register takes (bd-enrollment-v1). The device signs, with
// the private half of the key it registers or already has:
//
//	bd-enrollment-v1
//	<METHOD>
//	<PATH>
//	<DEVICE_ID>
//	<PUBLIC_KEY canonical base64>
//	<timestamp>
//	<nonce>
//
// joined by newlines, and sends timestamp, nonce and signature in the
// headers X-BD-Enrollment-Timestamp, -Nonce and -Signature. A proof is
// good for five minutes either side of now, and each nonce once per
// device for ten minutes.
const (
	proofVersion   = "bd-enrollment-v1"
	clockSkewMs    = 5 * 60 * 1000
	nonceMaxLength = 256
	nonceTTLMs     = 10 * 60 * 1000
	nonceCacheMax  = 20000
)

// nonces are the nonces of good proofs with the time they were used, in
// the order they came (usedNonces of the old module, a Map).
type nonces struct {
	mu    sync.Mutex
	order *list.List
	at    map[string]*list.Element
}

type usedNonce struct {
	key string
	ms  int64
}

func newNonces() *nonces {
	return &nonces{order: list.New(), at: map[string]*list.Element{}}
}

// prune drops nonces older than ten minutes. With fewer than 20,000 kept
// it looks at all of them; with more it stops as soon as half that many
// are left.
func (n *nonces) prune(now int64) {
	full := n.order.Len() >= nonceCacheMax
	for e := n.order.Front(); e != nil; {
		next := e.Next()
		used := e.Value.(usedNonce)
		if now-used.ms > nonceTTLMs {
			n.order.Remove(e)
			delete(n.at, used.key)
		}
		if full && n.order.Len() <= nonceCacheMax/2 {
			return
		}
		e = next
	}
}

// markUsed notes a nonce and reports whether it was noted already (a
// replay).
func (n *nonces) markUsed(key string, now int64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prune(now)
	if _, seen := n.at[key]; seen {
		return true
	}
	n.at[key] = n.order.PushBack(usedNonce{key: key, ms: now})
	return false
}

// canonicalKey is canonicalizePublicKey: the standard base64 of a key
// that is text and decodes to 32 bytes the forgiving way Node decodes.
// A key stored as bytes counts too: 32 bytes are the key itself, as the
// signal server keeps it, and other bytes hold the text the console
// stored in a column of bytes. ok is false for everything else.
func canonicalKey(v any) (key string, ok bool) {
	if raw, isBytes := v.([]byte); isBytes {
		if len(raw) == ed25519.PublicKeySize {
			return base64.StdEncoding.EncodeToString(raw), true
		}
		v = string(raw)
	}
	s, isText := v.(string)
	if !isText || trim(s) == "" {
		return "", false
	}
	raw := nodeBase64(trim(s))
	if len(raw) != ed25519.PublicKeySize {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(raw), true
}

// proofPayload is the text the device signs.
func proofPayload(method, path, deviceID, key, timestamp, nonce string) []byte {
	return []byte(strings.Join([]string{proofVersion, strings.ToUpper(method), path, deviceID, key, timestamp, nonce}, "\n"))
}

// proofHeaders are the three headers, trimmed.
func proofHeaders(r *http.Request) (timestamp, nonce, signature string) {
	return trim(headerText(r, "X-Bd-Enrollment-Timestamp")),
		trim(headerText(r, "X-Bd-Enrollment-Nonce")),
		trim(headerText(r, "X-Bd-Enrollment-Signature"))
}

// verifyProof checks the proof of a request for a device and a public
// key. It returns "" for a good proof and the reason otherwise, in the
// order the old module checked.
func (h *handlers) verifyProof(r *http.Request, deviceID string, publicKey any) string {
	timestamp, nonce, signature := proofHeaders(r)
	if timestamp == "" || nonce == "" || signature == "" {
		return "missing_proof_headers"
	}
	if length(nonce) > nonceMaxLength {
		return "nonce_too_long"
	}
	if !deviceIDPattern(deviceID) {
		return "invalid_device_id"
	}
	signedAt, ok := jsreq.ParseDate(timestamp, time.Local)
	if !ok {
		return "invalid_timestamp"
	}
	now := h.now().UnixMilli()
	if math.Abs(float64(now-signedAt)) > clockSkewMs {
		return "timestamp_outside_skew"
	}
	key, ok := canonicalKey(publicKey)
	if !ok {
		return "invalid_public_key"
	}
	sig := nodeBase64(signature)
	if len(sig) != ed25519.SignatureSize {
		return "invalid_signature_length"
	}
	raw, _ := base64.StdEncoding.DecodeString(key)
	// The path the device sent: the mount as spelled and the path below it
	payload := proofPayload(r.Method, httpx.RawPath(r), deviceID, key, timestamp, nonce)
	if !ed25519.Verify(ed25519.PublicKey(raw), payload, sig) {
		return "invalid_signature"
	}
	if h.nonces.markUsed("enrollment:"+deviceID+":"+nonce, now) {
		return "replayed_proof"
	}
	return ""
}
