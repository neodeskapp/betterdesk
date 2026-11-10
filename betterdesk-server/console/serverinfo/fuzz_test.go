package serverinfo

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// nodeReadEnv is the rule the Node console read env files by
// (parseEnvFile of the public endpoints service, and the loader in
// config/config.js): written here on its own, as the yardstick for what a
// rewritten file means. A key set twice has its last value.
func nodeReadEnv(text string) map[string]string {
	out := map[string]string{}
	for len(text) > 0 {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
			if strings.HasSuffix(line, "\r") {
				line = line[:len(line)-1]
			}
		} else {
			text = ""
		}
		line = strings.TrimFunc(line, fuzzIsSpace)
		if line == "" || line[0] == '#' {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		out[strings.TrimFunc(line[:eq], fuzzIsSpace)] = strings.TrimFunc(line[eq+1:], fuzzIsSpace)
	}
	return out
}

// fuzzIsSpace lists JavaScript's white space on its own, so the yardstick
// does not lean on the code under test.
func fuzzIsSpace(r rune) bool {
	return strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", r)
}

// nodeLines cuts text into lines the way the Node code did: at "\n", with
// a "\r" before it dropped.
func nodeLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i := range lines[:len(lines)-1] {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// lineSetsKey reports whether an update of key rewrites the line.
func lineSetsKey(line, key string) bool {
	trimmed := strings.TrimFunc(line, fuzzIsSpace)
	if trimmed == "" || trimmed[0] == '#' {
		return false
	}
	eq := strings.Index(line, "=")
	return eq > 0 && strings.TrimFunc(line[:eq], fuzzIsSpace) == key
}

// readableKey reports whether a reader of the file can see a key written
// as "key=value": it is not blank, has nothing to trim, no "=" and no line
// break, and does not look like a comment.
func readableKey(key string) bool {
	return key != "" && strings.TrimFunc(key, fuzzIsSpace) == key && !strings.ContainsAny(key, "=\n") && key[0] != '#' && key != "__proto__"
}

// checkUpsert applies one update and checks what must hold for it.
func checkUpsert(t *testing.T, text, key, value string) string {
	t.Helper()
	out := UpsertEnvKey(text, key, value)
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("UpsertEnvKey(%q, %q, %q) = %q does not end with a line break", text, key, value, out)
	}

	in := nodeLines(text)
	if in[len(in)-1] == "" {
		// What follows the last line break is not a line.
		in = in[:len(in)-1]
	}
	outLines := strings.Split(out, "\n")
	set := key + "=" + value

	if !strings.Contains(set, "\n") {
		// One line in, one line out: every line keeps its place. A line
		// that does not set the key is the same byte for byte.
		if len(outLines) < len(in)+1 {
			t.Fatalf("UpsertEnvKey(%q, %q, %q) = %q lost lines", text, key, value, out)
		}
		replaced := false
		for i, line := range in {
			want := line
			if lineSetsKey(line, key) {
				want, replaced = set, true
			}
			if outLines[i] != want {
				t.Fatalf("UpsertEnvKey(%q, %q, %q): line %d is %q, want %q", text, key, value, i, outLines[i], want)
			}
		}
		rest := outLines[len(in):]
		wantRest := []string{set, ""}
		if replaced {
			wantRest = []string{""}
		}
		if strings.Join(rest, "\n") != strings.Join(wantRest, "\n") {
			t.Fatalf("UpsertEnvKey(%q, %q, %q) ends with %q, want %q", text, key, value, rest, wantRest)
		}
	} else {
		// The new text spans lines. The lines that do not set the key are
		// still there, unchanged and in order.
		at := 0
		for _, line := range in {
			if lineSetsKey(line, key) {
				continue
			}
			for at < len(outLines) && outLines[at] != line {
				at++
			}
			if at == len(outLines) {
				t.Fatalf("UpsertEnvKey(%q, %q, %q) = %q lost the line %q", text, key, value, out, line)
			}
			at++
		}
	}

	if readableKey(key) && !strings.Contains(value, "\n") {
		want := strings.TrimFunc(value, fuzzIsSpace)
		got, ok := nodeReadEnv(out)[key]
		if !ok || got != want {
			t.Fatalf("after UpsertEnvKey(%q, %q, %q) the file %q reads %q for the key, want %q", text, key, value, out, got, want)
		}
		if again := ParseEnvFile(out)[key]; again != want {
			t.Fatalf("ParseEnvFile reads %q for %q from %q, want %q", again, key, out, want)
		}
	}
	return out
}

func FuzzEnvMerge(f *testing.F) {
	for _, seed := range [][5]string{
		{"", "PORT", "5000", "HOST", "0.0.0.0"},
		{"PORT=5000\n", "PORT", "6000", "PORT", "7000"},
		{"# custom\r\nMY_FEATURE=true\r\nPORT=5000\r\nSESSION_SECRET=keep-me\r\n", "PORT", "1", "NEW", "a=b #c $d"},
		{"A=1\nA=2\n  A = 3\nexport A=4\n#A=5\n", "A", "x y", "B", ""},
		{"no trailing newline", "K", "v", "K", "w"},
		{"=x\n==\n\ufeffA=1\n\u00a0B=2", "A", " lead", "B", "trail "},
		{"A=1\rB=2\r\n\r\n", "B", "\"quoted\"", "A", "'single'"},
		{"PUBLIC_API_URL=https://old.example\n", "PUBLIC_API_URL", "https://api.example.com:21121/base?x=1", "PUBLIC_SERVER_ID", "desk.example.com"},
		{"A=1\n", "A", "multi\nline", "", "empty key"},
		{"A=1\n", " A", "x", "A ", "y"},
		{"\xff\xfe=\x80\nA=\xe2\x82\n", "A", "\xff", "\xff\xfe", "v"},
	} {
		f.Add(seed[0], seed[1], seed[2], seed[3], seed[4])
	}
	f.Fuzz(func(t *testing.T, text, key1, value1, key2, value2 string) {
		after := checkUpsert(t, text, key1, value1)
		final := checkUpsert(t, after, key2, value2)

		// The first update is still in force after the second, unless the
		// second changed the same key. A second key such as "A=x" does: its
		// line reads as the key A.
		_, shadowed := nodeReadEnv(key2 + "=" + value2)[key1]
		if !shadowed && readableKey(key1) && !strings.Contains(value1, "\n") && !strings.Contains(key2+"="+value2, "\n") {
			if got := nodeReadEnv(final)[key1]; got != strings.TrimFunc(value1, fuzzIsSpace) {
				t.Fatalf("after setting %q then %q the file %q reads %q for the first key", key1, key2, final, got)
			}
		}

		// The other functions that read the same text must take anything.
		parsed := ParseEnvLines(text)
		for _, entry := range parsed.Entries {
			if entry.Type == EnvPair {
				if v, ok := parsed.Get(entry.Key); !ok || entry.Key == "" {
					t.Fatalf("ParseEnvLines(%q): pair %+v is not in the map (%q)", text, entry, v)
				}
			}
		}
		merged, added := MergeEnv(text, after)
		if !strings.HasPrefix(merged, text) {
			t.Fatalf("MergeEnv(%q, %q) = %q changed the existing text", text, after, merged)
		}
		for _, key := range added {
			if _, had := parsed.Get(key); had {
				t.Fatalf("MergeEnv(%q, %q) added %q, which the file has", text, after, key)
			}
		}
		ApplySubstitutions(text, []Substitution{{key1, value1}, {key2, value2}})
		_ = AssertResolvedSubstitutions([]Substitution{{key1, value1}})
		ParseSystemdEnvironment(text)
		PatchSystemdEnvironment(text, EnvVars{P2PFirst: value1, P2PFallbackMs: value2})
		ParseDockerComposeEnvironment(text)
		_, _ = PatchDockerComposeEnvironment(text, EnvVars{AlwaysUseRelay: value1})
	})
}

// javaScriptText is s as a JavaScript string holds it: every byte that is
// not part of valid UTF-8 is U+FFFD.
func javaScriptText(s string) string {
	return string([]rune(s))
}

func FuzzConfigURI(f *testing.F) {
	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, seed := range []struct {
		host, relay, api, key string
		https                 bool
		port                  int
	}{
		{"desk.example.com", "", "", validKey, false, 21114},
		{"https://desk.example.com:8443/path", "relay.example.com", "https://api.example.com", validKey, true, 443},
		{"203.0.113.10", "203.0.113.10", "http://203.0.113.10:21114", " " + validKey + "\n", false, 80},
		{"[2001:db8::1]:21116", "::1", "", "", true, 0},
		{"m\u00fcnchen.de", "\u65e5\u672c\u8a9e.jp", "https://b\u00fccher.example/\"q\"", "YOUR_PUBLIC_KEY", false, 21114},
		{"", "", "", "v1.4.9_public_key_placeholder...", false, 21114},
		{"a\"b\\c\n\t\u2028\u0000", "\xff\xfe", "\xe2\x82", strings.TrimRight(validKey, "="), true, 65535},
		{"example.com.", " spaced.example ", " ", validKey + "==", false, 21114},
	} {
		f.Add(seed.host, seed.relay, seed.api, seed.key, seed.https, seed.port)
	}
	f.Fuzz(func(t *testing.T, host, relay, api, key string, https bool, port int) {
		s := New(Options{GoAPIPort: port, APIURL: "http://127.0.0.1:21114/api"})
		payload := s.BuildRustDeskConfigPayload(Endpoints{Host: host, Relay: relay, API: api}, PayloadOptions{PublicKey: &key, UseHTTPS: &https})

		// The payload holds what was put in.
		if payload.Host == "" || payload.Relay == "" || payload.API == "" {
			t.Fatalf("payload %+v has an empty address", payload)
		}
		if payload.Host != NormalizeHostInput(host) {
			t.Fatalf("host %q, want %q", payload.Host, NormalizeHostInput(host))
		}
		wantRelay := payload.Host
		if relay != "" {
			wantRelay = NormalizeHostInput(relay)
		}
		if payload.Relay != wantRelay {
			t.Fatalf("relay %q, want %q", payload.Relay, wantRelay)
		}
		if api != "" && payload.API != api {
			t.Fatalf("API address %q, want %q", payload.API, api)
		}
		if api == "" && !strings.HasPrefix(payload.API, "http") {
			t.Fatalf("built API address %q", payload.API)
		}
		switch {
		case payload.Key == "":
			if IsValidRustDeskPublicKey(key) {
				t.Fatalf("the valid key %q was dropped", key)
			}
		case !IsValidRustDeskPublicKey(key) || payload.Key != strings.TrimFunc(key, fuzzIsSpace):
			t.Fatalf("key %q became %q", key, payload.Key)
		default:
			raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(payload.Key, "="))
			if err != nil || len(raw) != 32 {
				t.Fatalf("key %q passed and is not 32 bytes of base64", payload.Key)
			}
		}

		// The link decodes back to the payload the way a client reads it.
		uri := EncodeRustDeskConfigURI(payload)
		cli := EncodeRustDeskCliConfigString(payload)
		if uri != ConfigURIPrefix+cli {
			t.Fatalf("link %q is not the prefix and %q", uri, cli)
		}
		for i := 0; i < len(cli); i++ {
			c := cli[i]
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/') {
				t.Fatalf("config string %q holds %q", cli, c)
			}
		}
		reversed := []byte(cli)
		for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
			reversed[i], reversed[j] = reversed[j], reversed[i]
		}
		raw, err := base64.RawStdEncoding.DecodeString(string(reversed))
		if err != nil {
			t.Fatalf("config string %q: %v", cli, err)
		}
		if !utf8.Valid(raw) || !json.Valid(raw) {
			t.Fatalf("config string %q holds %q, which is not JSON text", cli, raw)
		}
		var decoded ConfigPayload
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("config string %q holds %q: %v", cli, raw, err)
		}
		want := ConfigPayload{
			Host: javaScriptText(payload.Host), Relay: javaScriptText(payload.Relay),
			API: javaScriptText(payload.API), Key: javaScriptText(payload.Key),
		}
		if decoded != want {
			t.Fatalf("config string %q decodes to %+v, want %+v", cli, decoded, want)
		}
		if !strings.HasPrefix(string(raw), `{"host":"`) || strings.Count(string(raw), `,"relay":"`) < 1 {
			t.Fatalf("JSON %q does not start with the host", raw)
		}

		// The functions that read hosts and addresses must take anything.
		NormalizeServerHost(host)
		StripRequestHost(host)
		IsPhoneUnreachableHost(host)
		parseJSURL(api)
		parseJSURL("https://" + host + "/" + relay)
		_, _ = NormalizeSettings(PublicEndpointsInput{PublicServerID: host, PublicRelayServer: relay, PublicAPIURL: api})
		New(Options{APIURL: api}).DefaultServerHost()
	})
}
