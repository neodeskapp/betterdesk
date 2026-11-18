package generator

import (
	"crypto/ed25519"
	"encoding/base64"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// The Support client reads its server settings from custom.txt: the JSON
// below with sorted keys, signed with Ed25519 and written as
// base64(signature || message), the format of BetterDesk-Client's
// sign_custom_client_config.py. The client checks the signature with the
// public key compiled into it, so these bytes are a contract.

// CustomTxtOptions are the settings that go into custom.txt.
type CustomTxtOptions struct {
	AppName any
	Host    any
	Relay   any
	API     any
	Key     any
	// DisableSettings is true, "Y" or "y" for "Y", anything else "N".
	// Unset means true.
	DisableSettings any
	// DisableSettingsSet says DisableSettings was given.
	DisableSettingsSet bool
}

// BuildSupportCustomTxt builds the JSON of custom.txt
// (customTxtBuilder.buildSupportCustomTxt).
func BuildSupportCustomTxt(o CustomTxtOptions) *goapi.Object {
	text := func(v any) string {
		if !truthy(v) {
			return ""
		}
		return jsTrim(jsString(v))
	}
	disable := "N"
	if !o.DisableSettingsSet {
		disable = "Y"
	} else if o.DisableSettings == true || o.DisableSettings == "Y" || o.DisableSettings == "y" {
		disable = "Y"
	}
	appName := "BetterDesk Support Agent"
	if truthy(o.AppName) {
		if name := jsTrim(jsString(o.AppName)); name != "" {
			appName = name
		}
	}
	return goapi.NewObject(
		"app-name", appName,
		"conn-type", "incoming",
		"disable-settings", disable,
		"override-settings", goapi.NewObject(
			"custom-rendezvous-server", text(o.Host),
			"relay-server", text(or(o.Relay, o.Host)),
			"api-server", text(o.API),
			"key", text(o.Key),
			"hide-server-settings", "Y",
			"hide-help-cards", "Y",
		),
	)
}

// StableJSONBytes is the message that is signed: the JSON with its keys
// sorted at every level, without white space, as UTF-8.
func StableJSONBytes(v any) []byte {
	return []byte(dbText(stableStringify(v)))
}

// Signed is a custom.txt and whether it carries a signature.
type Signed struct {
	Content string
	Signed  bool
}

// SignCustomTxt signs message with the 32-byte seed given in base64
// (read the way Node reads base64). Without a seed, or with one that does
// not decode to 32 bytes, the content is the message itself, unsigned.
func SignCustomTxt(message []byte, seedBase64 string) Signed {
	seedText := jsTrim(seedBase64)
	if seedText == "" {
		return Signed{Content: string(message)}
	}
	seed := nodeBase64Decode(seedText)
	if len(seed) != ed25519.SeedSize {
		return Signed{Content: string(message)}
	}
	key := ed25519.NewKeyFromSeed(seed)
	signature := ed25519.Sign(key, message)
	signed := make([]byte, 0, len(signature)+len(message))
	signed = append(append(signed, signature...), message...)
	return Signed{Content: base64.StdEncoding.EncodeToString(signed), Signed: true}
}

// The errors of the signing step. Their texts are the codes the old
// service threw; a build that fails with one records it.
const (
	ErrSigningSeedRequired = codeError("custom_client_signing_seed_required")
	ErrSigningSeedInvalid  = codeError("custom_client_signing_seed_invalid")
	ErrSigningFailed       = codeError("custom_client_signing_failed")
)

// codeError is an error whose text is its code.
type codeError string

func (e codeError) Error() string { return string(e) }

// requireSigningSeed checks that a seed is given and decodes to 32 bytes.
func requireSigningSeed(seedBase64 string) (string, error) {
	seedText := jsTrim(seedBase64)
	if seedText == "" {
		return "", ErrSigningSeedRequired
	}
	if len(nodeBase64Decode(seedText)) != ed25519.SeedSize {
		return "", ErrSigningSeedInvalid
	}
	return seedText, nil
}

// BuildAndSignSupportCustomTxt builds custom.txt and signs it; a Support
// build without a valid seed is refused
// (customTxtBuilder.buildAndSignSupportCustomTxt).
func BuildAndSignSupportCustomTxt(o CustomTxtOptions, seedBase64 string) (Signed, *goapi.Object, error) {
	json := BuildSupportCustomTxt(o)
	seed, err := requireSigningSeed(seedBase64)
	if err != nil {
		return Signed{}, nil, err
	}
	result := SignCustomTxt(StableJSONBytes(json), seed)
	if !result.Signed {
		return Signed{}, nil, ErrSigningFailed
	}
	return result, json, nil
}

// nodeBase64Decode decodes base64 the way Buffer.from(text, 'base64')
// does: both alphabets (+/ and -_), characters outside them skipped,
// decoding ends at the first "=", and a last group of one character is
// dropped.
func nodeBase64Decode(s string) []byte {
	var out []byte
	var acc uint32
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= 'a' && c <= 'z':
			v = c - 'a' + 26
		case c >= '0' && c <= '9':
			v = c - '0' + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			i = len(s)
			continue
		default:
			continue
		}
		acc = acc<<6 | uint32(v)
		n++
		if n == 4 {
			out = append(out, byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	switch n {
	case 2:
		out = append(out, byte(acc>>4))
	case 3:
		out = append(out, byte(acc>>10), byte(acc>>2))
	}
	return out
}
