package devicevault

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"

	"golang.org/x/crypto/nacl/box"
)

// Sealed sign-in, wire format 1. A computer whose NeoDesk opens it signs
// in at its own lock screen, so the browser viewer forwards the details
// without ever holding them readable. Server, viewer and client (branch
// nd-client-signin) keep to this text:
//
// Capability and state, in PeerInfo.platform_additions (a JSON object
// string) at login:
//
//	"nd_signin": 1          the client opens packets of format 1 (absent: it can't)
//	"logon_state": "..."    prelogin (nobody signed in at the console),
//	                        locked (a signed-in session is locked),
//	                        logon_ui (LogonUI runs in the active session and
//	                        neither of those holds, e.g. the Ctrl+Alt+Del
//	                        screen), desktop (none of these); absent: unknown.
//	                        Checked in that order.
//
// The client also names "nd_signin": 1 in a "platform_additions" object
// at the top level of its system information upload (POST /api/sysinfo
// JSON body, next to "id" and "uuid"); a JSON number of 1 or more counts,
// nothing else. Once a device has, the server hands out no readable saved
// sign-in for it ("sealed_required"), from the first use on and also when
// a later upload leaves the key out.
//
// A change of the state during the session: Misc.plugin_request (Misc
// field 25, PluginRequest {string id = 1; bytes content = 2}) with id
// "nd.logon_state" and content the UTF-8 JSON {"logon_state":"locked"},
// sent when it differs from the last value sent on the connection.
//
// The request, viewer to client: Misc.plugin_request with id "nd.signin"
// and content the packet's bytes. The packet is libsodium's
// crypto_box_seal(plaintext, recipient): the ephemeral X25519 public key
// (32 bytes), then the XSalsa20-Poly1305 box (16-byte tag, ciphertext);
// the box nonce is BLAKE2b-192(ephemeral public key || recipient). Go: x/crypto
// nacl/box SealAnonymous; Rust: sodiumoxide sealedbox::open. The recipient
// is crypto_sign_ed25519_pk_to_curve25519 of the device's Ed25519 public
// key as it registered it with the server (RegisterPk.pk); the device
// opens with crypto_sign_ed25519_sk_to_curve25519 of its Ed25519 secret
// key. The plaintext is the UTF-8 JSON of SigninPacket, keys in this
// order; unknown keys are ignored:
//
//	"v"          1
//	"device_id"  the device's ID
//	"session"    the relay session's UUID of the connection (RequestRelay.uuid)
//	"username"   the account name as saved
//	"password"   the password
//	"form"       "password" (password, Return) or "name_password"
//	             (account name, Tab, password, Return)
//	"not_after"  Unix seconds (UTC); the server sets now + 60
//	"nonce"      16 random bytes, base64url without padding
//
// The client answers the first failed check and types nothing: the
// packet does not open, the JSON is bad, v is not 1, device_id is not its
// own, session is not this connection's relay session, or the nonce was
// seen before while still within its not_after: "bad_blob"; its clock is
// past not_after: "expired"; logon_state is not prelogin, locked or
// logon_ui: "not_at_lock_screen". Otherwise its service sends the secure
// attention sequence for prelogin and locked, waits for the credential
// field (about 1.5 s), types the text as Unicode characters and Tab and
// Return as virtual keys, and answers "typed". The answer, client to
// viewer: Misc.plugin_request with id "nd.signin_result" and content the
// UTF-8 JSON {"result":"typed"|"not_at_lock_screen"|"expired"|"bad_blob",
// "nonce":"<the request's nonce, \"\" when the packet did not open>"}.
// Neither side logs or keeps the plaintext.
//
// testdata/sealed-signin-v1.json is a vector of made-up keys and values.

// SigninFormat is the version of the sealed sign-in packet.
const SigninFormat = 1

// SigninPacket is the plaintext of a sealed sign-in, in the wire format's
// key order.
type SigninPacket struct {
	V        int    `json:"v"`
	DeviceID string `json:"device_id"`
	Session  string `json:"session"`
	Username string `json:"username"`
	Password string `json:"password"`
	Form     string `json:"form"`
	NotAfter int64  `json:"not_after"`
	Nonce    string `json:"nonce"`
}

// SealSignin seals p for the device with the Ed25519 public key edPub.
// rnd makes the ephemeral key; nil is crypto/rand.
func SealSignin(edPub []byte, p SigninPacket, rnd io.Reader) ([]byte, error) {
	recipient, err := X25519FromEd25519(edPub)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	plain := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if rnd == nil {
		rnd = rand.Reader
	}
	out, err := box.SealAnonymous(nil, plain, &recipient, rnd)
	clear(plain)
	return out, err
}

var (
	fieldP = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	// edD is the Edwards curve's d = -121665/121666 mod p
	edD = func() *big.Int {
		num := new(big.Int).Neg(big.NewInt(121665))
		den := new(big.Int).ModInverse(big.NewInt(121666), fieldP)
		d := new(big.Int).Mul(num, den)
		return d.Mod(d, fieldP)
	}()
)

// X25519FromEd25519 is libsodium's crypto_sign_ed25519_pk_to_curve25519 for
// a valid key: u = (1 + y) / (1 - y) mod p, little-endian. It refuses a key
// of the wrong length, a y that is not reduced, a y that is no point of the
// curve, and the neutral point. (libsodium also refuses points of small
// order and outside the main subgroup; a device's own key is neither.)
func X25519FromEd25519(edPub []byte) ([32]byte, error) {
	var out [32]byte
	if len(edPub) != 32 {
		return out, errors.New("devicevault: the device key is not 32 bytes")
	}
	le := make([]byte, 32)
	copy(le, edPub)
	le[31] &= 0x7f
	y := new(big.Int).SetBytes(reverse(le))
	if y.Cmp(fieldP) >= 0 {
		return out, errors.New("devicevault: the device key is not a curve point")
	}
	one := big.NewInt(1)
	// x^2 = (y^2 - 1) / (d y^2 + 1) must be a square
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, fieldP)
	num := new(big.Int).Sub(y2, one)
	num.Mod(num, fieldP)
	den := new(big.Int).Mul(edD, y2)
	den.Add(den, one)
	den.Mod(den, fieldP)
	inv := new(big.Int).ModInverse(den, fieldP)
	if inv == nil {
		return out, errors.New("devicevault: the device key is not a curve point")
	}
	x2 := new(big.Int).Mul(num, inv)
	x2.Mod(x2, fieldP)
	if x2.Sign() != 0 {
		exp := new(big.Int).Rsh(new(big.Int).Sub(fieldP, one), 1)
		if new(big.Int).Exp(x2, exp, fieldP).Cmp(one) != 0 {
			return out, errors.New("devicevault: the device key is not a curve point")
		}
	}
	oneMinus := new(big.Int).Sub(one, y)
	oneMinus.Mod(oneMinus, fieldP)
	if oneMinus.Sign() == 0 {
		return out, errors.New("devicevault: the device key is the neutral point")
	}
	u := new(big.Int).Add(one, y)
	u.Mul(u, new(big.Int).ModInverse(oneMinus, fieldP))
	u.Mod(u, fieldP)
	b := u.Bytes()
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out, nil
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}
