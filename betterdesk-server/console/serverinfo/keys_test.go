package serverinfo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type oracleConfig struct {
	APIURL    string `json:"apiUrl"`
	GoAPIPort int    `json:"goApiPort"`
	APIPort   int    `json:"apiPort"`
}

func (c oracleConfig) options() Options {
	return Options{APIURL: c.APIURL, GoAPIPort: c.GoAPIPort, APIPort: c.APIPort}
}

// endpointArg is the first argument of the payload functions as a script
// passed it: a bare host or an object.
type endpointArg struct {
	Endpoints
}

func (a *endpointArg) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &a.Host)
	}
	var object struct {
		Host  string `json:"host"`
		Relay string `json:"relay"`
		API   string `json:"api"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	a.Endpoints = Endpoints{Host: object.Host, Relay: object.Relay, API: object.API}
	return nil
}

type payloadOptions struct {
	UseHTTPS  *bool   `json:"useHttps"`
	PublicKey *string `json:"publicKey"`
}

func (o payloadOptions) options() PayloadOptions {
	return PayloadOptions{UseHTTPS: o.UseHTTPS, PublicKey: o.PublicKey}
}

type keyVectors struct {
	ValidKey []struct {
		In  string `json:"in"`
		Out bool   `json:"out"`
	} `json:"valid_key"`
	APIURLForHost []struct {
		GoAPIPort int    `json:"goApiPort"`
		APIPort   int    `json:"apiPort"`
		Host      string `json:"host"`
		HTTPS     bool   `json:"https"`
		Out       string `json:"out"`
	} `json:"api_url_for_host"`
	Payload []struct {
		Cfg     oracleConfig   `json:"cfg"`
		FileKey *string        `json:"fileKey"`
		Arg     endpointArg    `json:"arg"`
		Opts    payloadOptions `json:"opts"`
		Payload ConfigPayload  `json:"payload"`
		CLI     string         `json:"cli"`
		URI     string         `json:"uri"`
	} `json:"payload"`
	RelayEndpoint []struct {
		Env  map[string]string `json:"env"`
		In   string            `json:"in"`
		Port json.RawMessage   `json:"port"`
		Out  string            `json:"out"`
	} `json:"relay_endpoint"`
	KeyFiles []struct {
		State struct {
			Name  string  `json:"name"`
			Bytes *string `json:"bytes"`
			Dir   bool    `json:"dir"`
		} `json:"state"`
		PublicKey        *string `json:"publicKey"`
		APIKeyMasked     *string `json:"apiKeyMasked"`
		APIKeyMaskedTrue *string `json:"apiKeyMaskedTrue"`
		APIKey           *string `json:"apiKey"`
	} `json:"key_files"`
	HalfCharacters []struct {
		Bytes        string `json:"bytes"`
		WellFormed   bool   `json:"wellFormed"`
		ToWellFormed string `json:"toWellFormed"`
	} `json:"api_key_half_characters"`
	Runs []struct {
		Cfg   oracleConfig `json:"cfg"`
		Steps []struct {
			AdvanceMs  int64           `json:"advanceMs"`
			FileKey    *string         `json:"fileKey"`
			APIKeyFile *string         `json:"apiKeyFile"`
			Go         apiAnswer       `json:"go"`
			QRFails    bool            `json:"qrFails"`
			Call       string          `json:"call"`
			Arg        endpointArg     `json:"arg"`
			Opts       payloadOptions  `json:"opts"`
			Out        json.RawMessage `json:"out"`
			GoCalls    []struct {
				URL     string `json:"url"`
				Timeout int    `json:"timeout"`
			} `json:"goCalls"`
			QRTexts []string `json:"qrTexts"`
		} `json:"steps"`
	} `json:"runs"`
	QROptions []json.RawMessage `json:"qr_options"`
}

func TestIsValidRustDeskPublicKeyAgainstNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "valid_key", len(v.ValidKey), 400)
	m := &mismatches{t: t}
	valid := 0
	for _, c := range v.ValidKey {
		if c.Out {
			valid++
		}
		if got := IsValidRustDeskPublicKey(c.In); got != c.Out {
			m.errorf("IsValidRustDeskPublicKey(%q) = %v, Node answers %v", c.In, got, c.Out)
		}
	}
	if valid < 50 || valid > len(v.ValidKey)-50 {
		t.Fatalf("%d of %d keys are valid: the vectors do not test both answers", valid, len(v.ValidKey))
	}
}

func TestAPIURLForHostAgainstNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "api_url_for_host", len(v.APIURLForHost), 300)
	for _, c := range v.APIURLForHost {
		s := New(Options{GoAPIPort: c.GoAPIPort, APIPort: c.APIPort})
		if got := s.APIURLForHost(c.Host, c.HTTPS); got != c.Out {
			t.Errorf("ports %d/%d: APIURLForHost(%q, %v) = %q, Node answers %q", c.GoAPIPort, c.APIPort, c.Host, c.HTTPS, got, c.Out)
		}
	}
}

// relayPortArg is the port argument of relayEndpointForHost as Go takes
// it: "" where JavaScript's `port || default` picks the default (no port,
// "", 0), the number or the text otherwise.
func relayPortArg(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("port %s: %v", raw, err)
	}
	switch p := v.(type) {
	case nil:
		return ""
	case string:
		return p
	case float64:
		if p == 0 {
			return ""
		}
		return numberString(p)
	}
	t.Fatalf("port %s has a type the script does not send", raw)
	return ""
}

func TestRelayEndpointForHostAgainstNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "relay_endpoint", len(v.RelayEndpoint), 20000)
	m := &mismatches{t: t}
	kinds := map[string]bool{}
	for i, c := range v.RelayEndpoint {
		env := c.Env
		s := New(Options{Getenv: func(key string) string { return env[key] }})
		port := relayPortArg(t, c.Port)
		got := s.RelayEndpointForHost(c.In, port)
		if got != c.Out {
			m.errorf("case %d: RelayEndpointForHost(%q, %q) with %v = %q, Node answers %q", i, c.In, port, env, got, c.Out)
		}
		switch {
		case c.Out == "":
			kinds["empty"] = true
		case strings.HasPrefix(c.Out, "["):
			kinds["bracketed"] = true
		case strings.HasSuffix(c.Out, ":21117"):
			kinds["default port"] = true
		case strings.HasSuffix(c.Out, ":24117"):
			kinds["second setting"] = true
		}
	}
	for _, kind := range []string{"empty", "bracketed", "default port", "second setting"} {
		if !kinds[kind] {
			t.Errorf("no vector gives a relay of the kind %q", kind)
		}
	}
}

// putKeyFile writes or removes a key file.
func putKeyFile(t testing.TB, path string, content *string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if content != nil {
		if err := os.WriteFile(path, []byte(*content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigPayloadAgainstNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "payload", len(v.Payload), 5000)
	pubKey := filepath.Join(t.TempDir(), "id_ed25519.pub")
	m := &mismatches{t: t}
	withKey := 0
	for i, c := range v.Payload {
		putKeyFile(t, pubKey, c.FileKey)
		opts := c.Cfg.options()
		opts.PubKeyPath = pubKey
		opts.Logger = &recordingLogger{}
		s := New(opts)

		got := s.BuildRustDeskConfigPayload(c.Arg.Endpoints, c.Opts.options())
		if got != c.Payload {
			m.errorf("case %d: payload %+v, Node built %+v (config %+v, arg %+v)", i, got, c.Payload, c.Cfg, c.Arg.Endpoints)
			continue
		}
		if got.Key != "" {
			withKey++
		}
		if cli := EncodeRustDeskCliConfigString(got); cli != c.CLI {
			m.errorf("case %d: config string\n%s\nNode wrote\n%s", i, cli, c.CLI)
		}
		if uri := EncodeRustDeskConfigURI(got); uri != c.URI {
			m.errorf("case %d: config link\n%s\nNode wrote\n%s", i, uri, c.URI)
		}
	}
	if withKey < 1000 {
		t.Fatalf("only %d payloads carry a key", withKey)
	}
}

func TestKeyFilesAgainstNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "key_files", len(v.KeyFiles), 15)
	dir := t.TempDir()
	pubKey, apiKey := filepath.Join(dir, "id_ed25519.pub"), filepath.Join(dir, ".api_key")
	text := func(s *string) string {
		if s == nil {
			return "null"
		}
		return *s
	}
	for _, c := range v.KeyFiles {
		for _, path := range []string{pubKey, apiKey} {
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			switch {
			case c.State.Dir:
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case c.State.Bytes != nil:
				if err := os.WriteFile(path, decodeBase64(t, *c.State.Bytes), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		s := New(Options{PubKeyPath: pubKey, APIKeyPath: apiKey, Logger: &recordingLogger{}})

		if got := string(s.GetPublicKey()); got != text(c.PublicKey) && !(c.PublicKey == nil && got == "") {
			t.Errorf("%s: GetPublicKey = %q, Node answers %s", c.State.Name, got, text(c.PublicKey))
		}
		for _, call := range []struct {
			masked bool
			want   *string
		}{{true, c.APIKeyMasked}, {true, c.APIKeyMaskedTrue}, {false, c.APIKey}} {
			if fixed, isFixed := shortKeyMasks[c.State.Name]; isFixed && call.masked {
				call.want = &fixed
			}
			got, ok := s.GetAPIKey(call.masked)
			if ok != (call.want != nil) || (ok && got != *call.want) {
				t.Errorf("%s: GetAPIKey(%v) = %q, %v, Node answers %s", c.State.Name, call.masked, got, ok, text(call.want))
			}
		}
	}
}

// A masked key cut through a character outside the Basic Multilingual
// Plane holds half of it in JavaScript. A Go string cannot, so the half is
// U+FFFD, which is also what JavaScript's toWellFormed makes of it.
func TestAPIKeyMaskCutsThroughCharacter(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "api_key_half_characters", len(v.HalfCharacters), 3)
	apiKey := filepath.Join(t.TempDir(), ".api_key")
	for _, c := range v.HalfCharacters {
		key := decodeBase64(t, c.Bytes)
		want, isFixed := shortKeyCutMasks[string(key)]
		if !isFixed {
			want = c.ToWellFormed
		}
		if err := os.WriteFile(apiKey, key, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, ok := New(Options{APIKeyPath: apiKey}).GetAPIKey(true); !ok || got != want {
			t.Errorf("GetAPIKey(true) = %q, want %q", got, want)
		}
	}
	// A key over 16 characters shows 4 UTF-16 units at each end, which can
	// cut through a character at both.
	if err := os.WriteFile(apiKey, []byte("abc😀0123456789😀xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := New(Options{APIKeyPath: apiKey}).GetAPIKey(true); !ok || got != "abc\uFFFD****\uFFFDxyz" {
		t.Errorf("GetAPIKey(true) = %q, want both ends cut through a character", got)
	}
}

// TestGetAPIKeyMasksShortKeys masks every key that is not empty: at most
// the first and last 4 characters show, and at most a quarter of the key
// at each end (BUGS SI4: a key of 8 characters or fewer went out whole).
func TestGetAPIKeyMasksShortKeys(t *testing.T) {
	apiKey := filepath.Join(t.TempDir(), ".api_key")
	for key, want := range map[string]string{
		"":                  "",
		"a":                 "****",
		"abc":               "****",
		"abcd":              "a****d",
		"abcd1234":          "ab****34",
		"abcd12345":         "ab****45",
		"abcdefghijkl":      "abc****jkl",
		"0123456789abcdef":  "0123****cdef",
		"0123456789abcdefg": "0123****defg",
	} {
		if err := os.WriteFile(apiKey, []byte(key+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, ok := New(Options{APIKeyPath: apiKey}).GetAPIKey(true); !ok || got != want {
			t.Errorf("GetAPIKey(true) of %q = %q, %v, want %q", key, got, ok, want)
		}
		if got, _ := New(Options{APIKeyPath: apiKey}).GetAPIKey(false); got != key {
			t.Errorf("GetAPIKey(false) of %q = %q", key, got)
		}
	}
}

func TestKeyRunsAgainstNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	atLeast(t, "runs", len(v.Runs), 200)
	dir := t.TempDir()
	pubKey, apiKey := filepath.Join(dir, "id_ed25519.pub"), filepath.Join(dir, ".api_key")
	m := &mismatches{t: t}
	fetched, cached := 0, 0
	for r, run := range v.Runs {
		api := &stubAPI{}
		clock := time.UnixMilli(1_700_000_000_000)
		qrFails := false
		var qrTexts []string
		opts := run.Cfg.options()
		opts.PubKeyPath, opts.APIKeyPath, opts.DBPath = pubKey, apiKey, "/opt/betterdesk/db_v2.sqlite3"
		opts.GoAPI = api.client()
		opts.Now = func() time.Time { return clock }
		opts.Logger = &recordingLogger{}
		opts.QR = func(text string) (string, error) {
			qrTexts = append(qrTexts, text)
			if qrFails {
				return "", errors.New("The amount of data is too big to be stored in a QR Code")
			}
			return "QR:" + text, nil
		}
		s := New(opts)

		for i, step := range run.Steps {
			clock = clock.Add(time.Duration(step.AdvanceMs) * time.Millisecond)
			putKeyFile(t, pubKey, step.FileKey)
			putKeyFile(t, apiKey, step.APIKeyFile)
			answer := step.Go
			api.set("GET", &answer)
			qrFails, qrTexts = step.QRFails, nil

			ctx := context.Background()
			var got any
			switch step.Call {
			case "resolve":
				got = s.ResolvePublicKey(ctx)
			case "clientConfig":
				got = s.GetClientConfig(ctx, step.Arg.Endpoints)
			case "configQR":
				got = s.GetServerConfigQR(ctx, step.Arg.Endpoints)
			case "keyQR":
				got = s.GetPublicKeyQR(ctx)
			case "serverConfig":
				cfg := s.GetServerConfig(ctx)
				cfg.PubKeyPath, cfg.APIKeyPath = "{PUB}", "{API}"
				got = cfg
			case "payloadAsync":
				got = s.ResolveRustDeskConfigPayload(ctx, step.Arg.Endpoints, step.Opts.options())
			default:
				t.Fatalf("unknown call %q", step.Call)
			}
			label := "run " + strconv.Itoa(r) + " step " + strconv.Itoa(i) + " " + step.Call
			if text := jsonText(t, got); text != fixedKeyRunAnswer(string(step.Out)) {
				m.errorf("%s: answer\n%s\nNode answers\n%s", label, text, step.Out)
			}

			calls := api.seen()
			if len(calls) != len(step.GoCalls) {
				m.errorf("%s: %d calls to the server, Node made %d", label, len(calls), len(step.GoCalls))
			}
			for _, call := range calls {
				if call.Method != "GET" || call.Path != "/api/server-key" {
					m.errorf("%s: called %s %s", label, call.Method, call.Path)
				}
			}
			if len(calls) > 0 {
				fetched++
			} else if step.FileKey == nil {
				cached++
			}
			if strings.Join(qrTexts, "\n") != strings.Join(step.QRTexts, "\n") {
				m.errorf("%s: QR codes of %q, Node drew %q", label, qrTexts, step.QRTexts)
			}
		}
	}
	if fetched < 100 || cached < 20 {
		t.Fatalf("%d steps asked the server and %d used a kept answer: the runs do not test both", fetched, cached)
	}
}

// The lead passes qr.PNGDataURL with these settings: the ones the Node
// console drew its codes with.
func TestQROptionsOfNode(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	if len(v.QROptions) != 2 {
		t.Fatalf("%d recorded option sets, want 2", len(v.QROptions))
	}
	const want = `{"errorCorrectionLevel":"M","type":"image/png","width":256,"margin":2,"color":{"dark":"#000000","light":"#ffffff"}}`
	for _, o := range v.QROptions {
		if string(o) != want {
			t.Errorf("Node drew with %s, want %s", o, want)
		}
	}
}

// The key the server reports is asked for with a limit of five seconds,
// as the Node console did.
func TestGoKeyTimeout(t *testing.T) {
	var v keyVectors
	loadVectors(t, "keys", &v)
	seen := 0
	for _, run := range v.Runs {
		for _, step := range run.Steps {
			for _, call := range step.GoCalls {
				seen++
				if call.URL != "/server-key" || time.Duration(call.Timeout)*time.Millisecond != goKeyTimeout {
					t.Fatalf("Node called %s with a limit of %d ms, the port uses /server-key and %v", call.URL, call.Timeout, goKeyTimeout)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no recorded call")
	}
}

// The link decodes back to the payload the way a RustDesk client reads
// it: the text after the prefix reversed, base64, JSON.
func TestConfigURIDecodesLikeAClient(t *testing.T) {
	payload := ConfigPayload{
		Host:  "203.0.113.10",
		Relay: "203.0.113.10",
		API:   "http://203.0.113.10:21114",
		Key:   "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/=",
	}
	uri := EncodeRustDeskConfigURI(payload)
	if !strings.HasPrefix(uri, "rustdesk://config/") {
		t.Fatalf("link %q has the wrong prefix", uri)
	}
	if strings.ContainsAny(uri, "=") {
		t.Fatalf("link %q carries padding", uri)
	}
	if got := decodeConfigString(t, strings.TrimPrefix(uri, "rustdesk://config/")); got != payload {
		t.Fatalf("decoded %+v, want %+v", got, payload)
	}
	if cli := EncodeRustDeskCliConfigString(payload); "rustdesk://config/"+cli != uri {
		t.Fatalf("the link's path %q is not the config string %q", uri, cli)
	}
}

// decodeConfigString reads a config string the way RustDesk does.
func decodeConfigString(t testing.TB, s string) ConfigPayload {
	t.Helper()
	reversed := []byte(s)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	raw, err := base64.RawStdEncoding.DecodeString(string(reversed))
	if err != nil {
		t.Fatalf("config string %q: %v", s, err)
	}
	var p ConfigPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("config string %q holds %q: %v", s, raw, err)
	}
	return p
}

func TestNullStringJSON(t *testing.T) {
	type answer struct {
		QR NullString `json:"qr"`
	}
	if got := jsonText(t, answer{}); got != `{"qr":null}` {
		t.Errorf("empty: %s", got)
	}
	if got := jsonText(t, answer{QR: "data:image/png;base64,AAAA"}); got != `{"qr":"data:image/png;base64,AAAA"}` {
		t.Errorf("set: %s", got)
	}
}

// Without a renderer a QR code is missing, not an error.
func TestQRWithoutRenderer(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &recordingLogger{}
	s := New(Options{PubKeyPath: path, Logger: log})
	if got := s.GetPublicKeyQR(context.Background()); got != "" {
		t.Errorf("QR code %q without a renderer", got)
	}
	if len(log.all()) != 1 {
		t.Errorf("logged %q, want one warning", log.all())
	}
}

// A key file that holds no key is reported once per read, with its length
// and without its content.
func TestInvalidKeyFileIsLoggedWithoutContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte("secret-looking-garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &recordingLogger{}
	s := New(Options{PubKeyPath: path, Logger: log})
	if got := s.GetPublicKey(); got != "" {
		t.Fatalf("GetPublicKey = %q", got)
	}
	lines := log.all()
	want := "warn: Public key at " + path + " is not a valid Ed25519 key (length=22); ignoring for client deploy/config."
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("logged %q, want %q", lines, want)
	}
}
