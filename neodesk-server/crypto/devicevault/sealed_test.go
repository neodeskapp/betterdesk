package devicevault

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

// vectorFile is the test vector of the sealed sign-in packet, format 1,
// for the client's side (branch nd-client-signin). Made up: the seed opens
// nothing. ND_WRITE_SIGNIN_VECTOR=1 writes it anew.
var vectorFile = filepath.Join("testdata", "sealed-signin-v1.json")

type vector struct {
	Comment          string `json:"comment"`
	Ed25519SeedHex   string `json:"ed25519_seed_hex"`
	Ed25519PublicHex string `json:"ed25519_public_hex"`
	X25519PublicHex  string `json:"x25519_public_hex"`
	X25519SecretHex  string `json:"x25519_secret_hex"`
	Plaintext        string `json:"plaintext"`
	PacketBase64     string `json:"packet_base64"`
}

// deviceKeys are the made-up device's Ed25519 key pair and the X25519 key
// pair libsodium converts it to: crypto_sign_ed25519_sk_to_curve25519 is
// the first 32 bytes of SHA-512 of the seed, clamped.
func deviceKeys(seed []byte) (edPub ed25519.PublicKey, xPub, xSec [32]byte) {
	edPub = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	h := sha512.Sum512(seed)
	copy(xSec[:], h[:32])
	xSec[0] &= 248
	xSec[31] &= 127
	xSec[31] |= 64
	k, err := ecdh.X25519().NewPrivateKey(xSec[:])
	if err != nil {
		panic(err)
	}
	copy(xPub[:], k.PublicKey().Bytes())
	return edPub, xPub, xSec
}

// fixedRand is a reader of the same bytes again and again: a sealed packet
// with a known ephemeral key, for a vector that stays the same.
type fixedRand struct{}

func (fixedRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(0x5a + i)
	}
	return len(p), nil
}

func vectorPacket() SigninPacket {
	return SigninPacket{
		V: 1, DeviceID: "123456789", Session: "9f1c2b7e-0d4a-4c3b-8a55-5e6f7a8b9c0d",
		Username: `LAB\lab-admin`, Password: `Made-Up-Vector-Pass-ä€@\<&>`, Form: "name_password",
		NotAfter: 1795000000, Nonce: "AAECAwQFBgcICQoLDA0ODw",
	}
}

// TestEd25519KeysConvertAsLibsodiumConvertsThem: the X25519 public key made
// from a device's Ed25519 public key is the one its X25519 secret key
// (libsodium's conversion of the Ed25519 secret key) has, for many keys;
// a key that is not a curve point is refused.
func TestEd25519KeysConvertAsLibsodiumConvertsThem(t *testing.T) {
	for i := 0; i < 64; i++ {
		seed := bytes.Repeat([]byte{byte(i * 7)}, 32)
		seed[0] = byte(i)
		edPub, xPub, _ := deviceKeys(seed)
		got, err := X25519FromEd25519(edPub)
		if err != nil || got != xPub {
			t.Fatalf("key %d: %x %v, want %x", i, got, err, xPub)
		}
	}
	for _, bad := range [][]byte{nil, make([]byte, 31), bytes.Repeat([]byte{0xff}, 32)} {
		if _, err := X25519FromEd25519(bad); err == nil {
			t.Errorf("%x converts", bad)
		}
	}
	// y = 1 (the neutral point) has no Montgomery form
	one := make([]byte, 32)
	one[0] = 1
	if _, err := X25519FromEd25519(one); err == nil {
		t.Error("the neutral point converts")
	}
}

// TestSealedSigninOpensOnlyForTheDevice: a packet opens with the device's
// X25519 key pair to the JSON of the wire format (keys in this order, no
// HTML escaping), not with another device's, and two seals differ.
func TestSealedSigninOpensOnlyForTheDevice(t *testing.T) {
	edPub, xPub, xSec := deviceKeys(bytes.Repeat([]byte{1}, 32))
	p := vectorPacket()
	packet, err := SealSignin(edPub, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) < 48 || bytes.Contains(packet, []byte("Made-Up")) {
		t.Fatalf("packet of %d bytes", len(packet))
	}
	plain, ok := box.OpenAnonymous(nil, packet, &xPub, &xSec)
	if !ok {
		t.Fatal("the device does not open the packet")
	}
	want := `{"v":1,"device_id":"123456789","session":"9f1c2b7e-0d4a-4c3b-8a55-5e6f7a8b9c0d","username":"LAB\\lab-admin","password":"Made-Up-Vector-Pass-ä€@\\<&>","form":"name_password","not_after":1795000000,"nonce":"AAECAwQFBgcICQoLDA0ODw"}`
	if string(plain) != want {
		t.Fatalf("plaintext\n got  %s\n want %s", plain, want)
	}
	_, otherPub, otherSec := deviceKeys(bytes.Repeat([]byte{2}, 32))
	if _, ok := box.OpenAnonymous(nil, packet, &otherPub, &otherSec); ok {
		t.Error("another device opens the packet")
	}
	again, _ := SealSignin(edPub, p, nil)
	if bytes.Equal(again, packet) {
		t.Error("two seals are the same")
	}
}

// TestSealedSigninVector: the vector file opens with its keys to its
// plaintext, and sealing its plaintext with the fixed ephemeral key gives
// its packet, so the client's side can test against it.
func TestSealedSigninVector(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, 32)
	edPub, xPub, xSec := deviceKeys(seed)
	fresh, err := SealSignin(edPub, vectorPacket(), fixedRand{})
	if err != nil {
		t.Fatal(err)
	}
	plain, ok := box.OpenAnonymous(nil, fresh, &xPub, &xSec)
	if !ok {
		t.Fatal("the vector's packet does not open")
	}
	if os.Getenv("ND_WRITE_SIGNIN_VECTOR") == "1" {
		v := vector{
			Comment:          "NeoDesk sealed sign-in, format 1 (made-up keys and values). packet = crypto_box_seal(plaintext, x25519_public); x25519_public = crypto_sign_ed25519_pk_to_curve25519(ed25519_public); x25519_secret = crypto_sign_ed25519_sk_to_curve25519 of the key pair of ed25519_seed.",
			Ed25519SeedHex:   hex.EncodeToString(seed),
			Ed25519PublicHex: hex.EncodeToString(edPub),
			X25519PublicHex:  hex.EncodeToString(xPub[:]),
			X25519SecretHex:  hex.EncodeToString(xSec[:]),
			Plaintext:        string(plain),
			PacketBase64:     base64.StdEncoding.EncodeToString(fresh),
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorFile, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Ed25519PublicHex != hex.EncodeToString(edPub) || v.X25519PublicHex != hex.EncodeToString(xPub[:]) || v.X25519SecretHex != hex.EncodeToString(xSec[:]) {
		t.Fatal("the vector's keys are not the seed's")
	}
	packet, err := base64.StdEncoding.DecodeString(v.PacketBase64)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, fresh) {
		t.Error("sealing the vector's plaintext no longer gives its packet")
	}
	opened, ok := box.OpenAnonymous(nil, packet, &xPub, &xSec)
	if !ok || string(opened) != v.Plaintext || !strings.Contains(v.Plaintext, `"v":1`) {
		t.Errorf("the vector's packet opens to %q, %v", opened, ok)
	}
}
