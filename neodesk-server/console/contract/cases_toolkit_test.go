//go:build contract

package contract_test

import (
	"encoding/base64"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The toolkit: text tools that answer from the request alone, and network
// tools. The network tools are sent only what they refuse before any
// lookup or connection, or what stays on the instance: a certificate check
// of a closed port of the instance, a magic packet to the local broadcast
// address, the records of a name that is an address. What a certificate
// check answers for a real server is covered by the unit tests of
// console/routes/toolkit.
func init() {
	post := func(name, path string, body any, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: path, Body: body, Roles: roles}
	}
	raw := func(name, path, body string, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: path, RawBody: body, ContentType: "application/json", Roles: roles}
	}
	few := contract.Roles("signed-out", "viewer", "admin")
	jwt := func(parts ...string) string {
		for i, part := range parts {
			parts[i] = base64.RawURLEncoding.EncodeToString([]byte(part))
		}
		return strings.Join(parts, ".")
	}
	const header = `{"alg":"HS256","typ":"JWT"}`
	megabyte := strings.Repeat("x", 1048576)

	contract.Group("toolkit",
		// Hash
		post("hash-default", "/api/toolkit/hash", map[string]any{"text": "hello"}),
		post("hash-md5", "/api/toolkit/hash", map[string]any{"text": "hello", "algorithm": "md5"}, few...),
		post("hash-sha1", "/api/toolkit/hash", map[string]any{"text": "hello", "algorithm": "sha1"}, few...),
		post("hash-sha512", "/api/toolkit/hash", map[string]any{"text": "hello", "algorithm": "sha512"}, few...),
		post("hash-unknown-algorithm", "/api/toolkit/hash", map[string]any{"text": "hello", "algorithm": "sha3"}, few...),
		post("hash-algorithm-in-capitals", "/api/toolkit/hash", map[string]any{"text": "hello", "algorithm": "MD5"}, few...),
		post("hash-algorithm-not-text", "/api/toolkit/hash", map[string]any{"text": "hello", "algorithm": []string{"md5"}}, few...),
		post("hash-empty-text", "/api/toolkit/hash", map[string]any{"text": ""}, few...),
		post("hash-unicode", "/api/toolkit/hash", map[string]any{"text": "Grüße \U0001F600  "}, few...),
		raw("hash-lone-surrogate", "/api/toolkit/hash", `{"text":"a\ud800b"}`, few...),
		post("hash-no-text", "/api/toolkit/hash", map[string]any{"algorithm": "md5"}),
		post("hash-text-is-a-number", "/api/toolkit/hash", map[string]any{"text": 5}, few...),
		post("hash-text-is-null", "/api/toolkit/hash", map[string]any{"text": nil}, few...),
		raw("hash-array-body", "/api/toolkit/hash", `["hello"]`, few...),
		post("hash-largest-text", "/api/toolkit/hash", map[string]any{"text": megabyte}, few...),
		post("hash-text-too-large", "/api/toolkit/hash", map[string]any{"text": megabyte + "x"}, few...),
		// The limit counts UTF-16 units, not bytes: this text has more than
		// a megabyte of bytes and a third of that in units
		post("hash-large-text-in-three-byte-letters", "/api/toolkit/hash", map[string]any{"text": strings.Repeat("€", 349526)}, few...),
		contract.Case{Name: "hash-without-csrf-token", Method: "POST", Path: "/api/toolkit/hash", Body: map[string]any{"text": "hello"}, NoCSRF: true, Roles: few},
		contract.Case{Name: "hash-as-form", Method: "POST", Path: "/api/toolkit/hash", RawBody: "text=hello", ContentType: "application/x-www-form-urlencoded", Roles: few},

		// Password: the password itself is random, its shape is not
		post("password-default", "/api/toolkit/password", map[string]any{}),
		post("password-with-symbols", "/api/toolkit/password", map[string]any{"length": 32, "symbols": true}, few...),
		post("password-digits-only", "/api/toolkit/password", map[string]any{"length": 8, "uppercase": false, "lowercase": false}, few...),
		post("password-capitals-only", "/api/toolkit/password", map[string]any{"length": 20, "lowercase": "false", "digits": 0}, few...),
		post("password-no-sets", "/api/toolkit/password", map[string]any{"uppercase": false, "lowercase": false, "digits": "0", "symbols": "no"}, few...),
		post("password-shortest", "/api/toolkit/password", map[string]any{"length": 1}, few...),
		post("password-longest", "/api/toolkit/password", map[string]any{"length": 100000}, few...),
		post("password-length-as-text", "/api/toolkit/password", map[string]any{"length": " 12px"}, few...),
		post("password-length-not-a-number", "/api/toolkit/password", map[string]any{"length": "long"}, few...),
		post("password-length-fraction", "/api/toolkit/password", map[string]any{"length": 12.9}, few...),
		post("password-length-list", "/api/toolkit/password", map[string]any{"length": []int{40}}, few...),
		post("password-flags-as-text", "/api/toolkit/password", map[string]any{"uppercase": "0", "lowercase": "1", "digits": "true", "symbols": 1, "length": 10}, few...),
		post("password-flags-of-other-types", "/api/toolkit/password", map[string]any{"uppercase": nil, "lowercase": "yes", "digits": 2, "symbols": []bool{true}, "length": 10}, few...),
		// An array has a length of its own
		raw("password-array-body", "/api/toolkit/password", `[1,2,3,4,5,6,7,8,9]`, few...),

		// Base64
		post("base64-encode", "/api/toolkit/base64", map[string]any{"text": "hello wörld"}),
		post("base64-decode", "/api/toolkit/base64", map[string]any{"text": "aGVsbG8gd8O2cmxk", "mode": "decode"}),
		post("base64-decode-url-alphabet", "/api/toolkit/base64", map[string]any{"text": "8J-YgD8_", "mode": "decode"}, few...),
		post("base64-decode-skips-other-characters", "/api/toolkit/base64", map[string]any{"text": " aGV\nsb*G8= ", "mode": "decode"}, few...),
		post("base64-decode-stops-at-padding", "/api/toolkit/base64", map[string]any{"text": "aGk=aGk=", "mode": "decode"}, few...),
		post("base64-decode-no-padding", "/api/toolkit/base64", map[string]any{"text": "aGVsbG8", "mode": "decode"}, few...),
		post("base64-decode-one-character", "/api/toolkit/base64", map[string]any{"text": "a", "mode": "decode"}, few...),
		post("base64-decode-not-text", "/api/toolkit/base64", map[string]any{"text": "/w==", "mode": "decode"}, few...),
		post("base64-decode-broken-sequence", "/api/toolkit/base64", map[string]any{"text": "4oKs4oI=", "mode": "decode"}, few...),
		post("base64-decode-letters-beyond-latin", "/api/toolkit/base64", map[string]any{"text": "ŁQUJD€", "mode": "decode"}, few...),
		post("base64-decode-control-characters", "/api/toolkit/base64", map[string]any{"text": "AAECCQp/4oCo", "mode": "decode"}, few...),
		post("base64-decode-empty", "/api/toolkit/base64", map[string]any{"text": "", "mode": "decode"}, few...),
		post("base64-other-mode", "/api/toolkit/base64", map[string]any{"text": "hi", "mode": "DECODE"}, few...),
		post("base64-mode-number", "/api/toolkit/base64", map[string]any{"text": "hi", "mode": 1.50}, few...),
		post("base64-mode-zero", "/api/toolkit/base64", map[string]any{"text": "hi", "mode": 0}, few...),
		raw("base64-mode-object", "/api/toolkit/base64", `{"text":"hi","mode":{"b":1e3,"2":[null,"x "],"1":true,"b":-0}}`, few...),
		post("base64-no-text", "/api/toolkit/base64", map[string]any{"mode": "decode"}),
		post("base64-text-too-large", "/api/toolkit/base64", map[string]any{"text": megabyte + "x"}, few...),

		// URL encoding
		post("urlencode-encode", "/api/toolkit/urlencode", map[string]any{"text": "a b&c=d/e?f#g+h~i!j*k'l(m)n-o_p.q ü \U0001F600"}),
		post("urlencode-decode", "/api/toolkit/urlencode", map[string]any{"text": "a%20b%26c%2Fd+e%C3%BC%F0%9F%98%80%2b", "mode": "decode"}),
		post("urlencode-decode-incomplete-escape", "/api/toolkit/urlencode", map[string]any{"text": "100%", "mode": "decode"}),
		post("urlencode-decode-not-hex", "/api/toolkit/urlencode", map[string]any{"text": "%zz", "mode": "decode"}, few...),
		post("urlencode-decode-broken-sequence", "/api/toolkit/urlencode", map[string]any{"text": "%E2%82", "mode": "decode"}, few...),
		post("urlencode-decode-overlong", "/api/toolkit/urlencode", map[string]any{"text": "%C0%AF", "mode": "decode"}, few...),
		post("urlencode-decode-surrogate", "/api/toolkit/urlencode", map[string]any{"text": "%ED%A0%80", "mode": "decode"}, few...),
		post("urlencode-decode-lone-continuation", "/api/toolkit/urlencode", map[string]any{"text": "%80", "mode": "decode"}, few...),
		post("urlencode-decode-control-characters", "/api/toolkit/urlencode", map[string]any{"text": "%00%01%09%0A%7F%E2%80%A8", "mode": "decode"}, few...),
		raw("urlencode-encode-lone-surrogate", "/api/toolkit/urlencode", `{"text":"a\ud800b"}`, few...),
		raw("urlencode-encode-low-surrogate-first", "/api/toolkit/urlencode", `{"text":"\udc00\ud800"}`, few...),
		raw("urlencode-encode-pair-in-escapes", "/api/toolkit/urlencode", `{"text":"😀"}`, few...),
		raw("urlencode-decode-keeps-lone-surrogate", "/api/toolkit/urlencode", `{"text":"%41\ud800","mode":"decode"}`, few...),
		post("urlencode-other-mode", "/api/toolkit/urlencode", map[string]any{"text": "a b", "mode": []string{"decode"}}, few...),
		post("urlencode-no-text", "/api/toolkit/urlencode", map[string]any{}),
		post("urlencode-text-too-large", "/api/toolkit/urlencode", map[string]any{"text": megabyte + "x", "mode": "decode"}, few...),

		// JWT
		contract.Case{
			Name: "jwt-expired", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"sub":"1234567890","name":"Jo","exp":1000000000,"iat":999999000}`) + ".c2ln"},
			Volatile: []string{"data.expiry.secondsLeft"},
		},
		contract.Case{
			Name: "jwt-valid-for-long", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"exp":9999999999.5,"roles":["a","b"],"n":{"10":1,"b":2,"2":3},"big":1e21,"small":1.0,"neg":-0}`) + ".sig"},
			Volatile: []string{"data.expiry.secondsLeft"}, Roles: few,
		},
		post("jwt-no-expiry", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"sub":"x","exp":0}`) + ".sig"}, few...),
		post("jwt-two-parts", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(`{"alg":"none"}`, `{"sub":"x"}`)}, few...),
		post("jwt-four-parts", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"sub":"x"}`) + ".a.b"}, few...),
		post("jwt-empty-signature", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"sub":"x"}`) + "."}, few...),
		post("jwt-one-part", "/api/toolkit/jwt-decode", map[string]any{"token": "abcdef"}),
		post("jwt-header-not-json", "/api/toolkit/jwt-decode", map[string]any{"token": jwt("hello there, this is no JSON", `{}`)}, few...),
		post("jwt-header-empty", "/api/toolkit/jwt-decode", map[string]any{"token": "." + jwt(`{}`)}, few...),
		post("jwt-payload-cut-off", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"sub":"x"`)}, few...),
		post("jwt-payload-trailing-text", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"sub":"x"} x`)}, few...),
		post("jwt-payload-bad-string", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, "{\"sub\":\"a\nb\"}")}, few...),
		post("jwt-payload-single-quotes", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{'sub':'x', "a long tail to push the place of the error out of sight":1}`)}, few...),
		post("jwt-payload-null", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `null`)}, few...),
		post("jwt-payload-number", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(`"text"`, `42`)}, few...),
		post("jwt-payload-list", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(`[1]`, `[{"exp":1}]`)}, few...),
		contract.Case{
			Name: "jwt-expiry-as-text", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"exp":" 0x10 "}`)},
			Volatile: []string{"data.expiry.secondsLeft"}, Roles: few,
		},
		contract.Case{
			Name: "jwt-expiry-true", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"exp":true}`)},
			Volatile: []string{"data.expiry.secondsLeft"}, Roles: few,
		},
		contract.Case{
			Name: "jwt-expiry-list", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"exp":[[1700000000.9]]}`)},
			Volatile: []string{"data.expiry.secondsLeft"}, Roles: few,
		},
		contract.Case{
			Name: "jwt-expiry-before-year-zero", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"exp":-99999999999}`)},
			Volatile: []string{"data.expiry.secondsLeft"}, Roles: few,
		},
		contract.Case{
			Name: "jwt-expiry-far-future", Method: "POST", Path: "/api/toolkit/jwt-decode",
			Body:     map[string]any{"token": jwt(header, `{"exp":8640000000000}`)},
			Volatile: []string{"data.expiry.secondsLeft"}, Roles: few,
		},
		post("jwt-expiry-out-of-range", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"exp":8640000000001}`)}, few...),
		post("jwt-expiry-not-a-number", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"exp":"soon"}`)}, few...),
		post("jwt-expiry-object", "/api/toolkit/jwt-decode", map[string]any{"token": jwt(header, `{"exp":{"at":1}}`)}, few...),
		post("jwt-no-token", "/api/toolkit/jwt-decode", map[string]any{}),
		post("jwt-token-is-a-number", "/api/toolkit/jwt-decode", map[string]any{"token": 12.5}, few...),

		// Certificate check
		post("ssl-no-host", "/api/toolkit/ssl", map[string]any{"port": 443}),
		post("ssl-host-is-a-number", "/api/toolkit/ssl", map[string]any{"host": 127}, few...),
		post("ssl-host-with-a-space", "/api/toolkit/ssl", map[string]any{"host": "exa mple.com"}, few...),
		post("ssl-host-with-a-bracket", "/api/toolkit/ssl", map[string]any{"host": "exa<mple>.com"}, few...),
		post("ssl-host-only-a-scheme", "/api/toolkit/ssl", map[string]any{"host": "https://"}, few...),
		post("ssl-host-too-long", "/api/toolkit/ssl", map[string]any{"host": strings.Repeat("a", 254)}, few...),
		post("ssl-localhost", "/api/toolkit/ssl", map[string]any{"host": "localhost"}),
		post("ssl-localhost-as-url", "/api/toolkit/ssl", map[string]any{"host": "https://LocalHost:8443/path"}, few...),
		post("ssl-loopback", "/api/toolkit/ssl", map[string]any{"host": "127.0.0.1", "port": 5000}, few...),
		post("ssl-metadata-address", "/api/toolkit/ssl", map[string]any{"host": "http://169.254.169.254/latest"}, few...),
		post("ssl-metadata-name", "/api/toolkit/ssl", map[string]any{"host": " metadata.google.internal "}, few...),
		post("ssl-port-too-high", "/api/toolkit/ssl", map[string]any{"host": "10.255.255.1", "port": 65536}, few...),
		post("ssl-port-negative", "/api/toolkit/ssl", map[string]any{"host": "10.255.255.1", "port": "-1"}, few...),
		// Nothing listens on port 1 of the instance, which its own host name
		// reaches. 0.0.0.0 would reach it too and is refused
		post("ssl-closed-port", "/api/toolkit/ssl", map[string]any{"host": "{console_host}", "port": 1}, few...),
		post("ssl-closed-port-as-text", "/api/toolkit/ssl", map[string]any{"host": "0.0.0.0", "port": "1.9e3"}, few...),

		// Wake-on-LAN
		post("wol-no-mac", "/api/toolkit/wol", map[string]any{}),
		post("wol-mac-is-a-number", "/api/toolkit/wol", map[string]any{"mac": 123456789012}, few...),
		post("wol-mac-too-short", "/api/toolkit/wol", map[string]any{"mac": "aa:bb:cc:dd:ee"}),
		post("wol-mac-not-hex", "/api/toolkit/wol", map[string]any{"mac": "aa:bb:cc:dd:ee:gg"}, few...),
		post("wol-mac-with-dots", "/api/toolkit/wol", map[string]any{"mac": "aabb.ccdd.eeff"}, few...),
		post("wol-send", "/api/toolkit/wol", map[string]any{"mac": "aa:bb:cc:dd:ee:0f"}),
		post("wol-send-other-spelling", "/api/toolkit/wol", map[string]any{"mac": "AA-bb-:cC:-dd--EE0F", "broadcast": "127.255.255.255"}, few...),
		post("wol-send-ligatures", "/api/toolkit/wol", map[string]any{"mac": "ﬀﬀﬀﬀﬀﬀ"}, few...),
		post("wol-broadcast-not-text", "/api/toolkit/wol", map[string]any{"mac": "aabbccddee0f", "broadcast": 255}, few...),
		post("wol-broadcast-ipv6", "/api/toolkit/wol", map[string]any{"mac": "aabbccddee0f", "broadcast": "::1"}, few...),

		// DNS records
		post("whois-no-domain", "/api/toolkit/whois", map[string]any{}),
		post("whois-domain-is-a-list", "/api/toolkit/whois", map[string]any{"domain": []string{"example.com"}}, few...),
		post("whois-domain-with-a-space", "/api/toolkit/whois", map[string]any{"domain": "exa mple.com"}, few...),
		post("whois-domain-starts-with-a-dash", "/api/toolkit/whois", map[string]any{"domain": "-example.com"}, few...),
		post("whois-domain-one-letter", "/api/toolkit/whois", map[string]any{"domain": "a"}, few...),
		post("whois-domain-too-long", "/api/toolkit/whois", map[string]any{"domain": strings.Repeat("a", 254)}, few...),
		post("whois-localhost", "/api/toolkit/whois", map[string]any{"domain": "https://LOCALHOST:8443/x"}),
		post("whois-loopback", "/api/toolkit/whois", map[string]any{"domain": "127.0.0.1"}, few...),
		post("whois-metadata", "/api/toolkit/whois", map[string]any{"domain": "Metadata.Goog"}, few...),
		// An address is a name like any other to the record lookups, and
		// one that has no records
		post("whois-address", "/api/toolkit/whois", map[string]any{"domain": "10.0.0.1"}, contract.Roles("viewer", "admin")...),
	)
}
