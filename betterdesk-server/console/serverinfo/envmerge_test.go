package serverinfo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// pairs is a list of [key, value] entries in the order JavaScript walked
// the object.
type pairs []Substitution

func (p *pairs) UnmarshalJSON(data []byte) error {
	var raw [][2]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = nil
	for _, kv := range raw {
		*p = append(*p, Substitution{Key: kv[0], Value: kv[1]})
	}
	return nil
}

type envMergeVectors struct {
	ParseEnvLines []struct {
		Text string `json:"text"`
		Out  struct {
			Entries []struct {
				Type  string  `json:"type"`
				Key   *string `json:"key"`
				Value *string `json:"value"`
				Raw   string  `json:"raw"`
			} `json:"entries"`
			Keys   []string `json:"keys"`
			Values []string `json:"values"`
		} `json:"out"`
	} `json:"parse_env_lines"`
	ParseEnvFile []struct {
		Text string            `json:"text"`
		Out  map[string]string `json:"out"`
	} `json:"parse_env_file"`
	BuildFresh []struct {
		Text string `json:"text"`
		Out  string `json:"out"`
	} `json:"build_fresh"`
	Upsert []struct {
		Text    string `json:"text"`
		Updates []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
			Out   string `json:"out"`
		} `json:"updates"`
	} `json:"upsert"`
	Merge []struct {
		Existing string   `json:"existing"`
		Template string   `json:"template"`
		Content  string   `json:"content"`
		Added    []string `json:"added"`
	} `json:"merge"`
	Substitute []struct {
		Text string `json:"text"`
		Subs pairs  `json:"subs"`
		Out  string `json:"out"`
	} `json:"substitute"`
	AssertResolved []struct {
		Subs pairs   `json:"subs"`
		Err  *string `json:"err"`
	} `json:"assert_resolved"`
	BuildSubstitutions []struct {
		ExistingContent string            `json:"existingContent"`
		UseMap          bool              `json:"useMap"`
		Config          map[string]string `json:"config"`
		RandomByte      byte              `json:"randomByte"`
		Out             pairs             `json:"out"`
	} `json:"build_substitutions"`
	MergeFile []struct {
		Template     *string         `json:"template"`
		Target       *string         `json:"target"`
		Subs         pairs           `json:"subs"`
		FreshInstall bool            `json:"freshInstall"`
		Out          *MergeEnvResult `json:"out"`
		Err          *string         `json:"err"`
		After        *string         `json:"after"`
		Mode         *int            `json:"mode"`
		Name         string          `json:"name"`
		TemplateName string          `json:"templateName"`
	} `json:"merge_file"`
}

func TestParseEnvLinesAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "parse_env_lines", len(v.ParseEnvLines), 700)
	m := &mismatches{t: t}
	kinds := map[string]int{}
	for _, c := range v.ParseEnvLines {
		got := ParseEnvLines(c.Text)
		if len(got.Entries) != len(c.Out.Entries) {
			m.errorf("ParseEnvLines(%q): %d lines, Node has %d", c.Text, len(got.Entries), len(c.Out.Entries))
			continue
		}
		for i, want := range c.Out.Entries {
			e := got.Entries[i]
			kinds[e.Type]++
			wantKey, wantValue := "", ""
			if want.Key != nil {
				wantKey, wantValue = *want.Key, *want.Value
			}
			if e.Type != want.Type || e.Raw != want.Raw || e.Key != wantKey || e.Value != wantValue {
				m.errorf("ParseEnvLines(%q) line %d: %+v, Node has type %q key %q value %q raw %q", c.Text, i, e, want.Type, wantKey, wantValue, want.Raw)
			}
		}
		keys := got.Keys()
		if keys == nil {
			keys = []string{}
		}
		if !reflect.DeepEqual(keys, c.Out.Keys) {
			m.errorf("ParseEnvLines(%q): keys %q, Node has %q", c.Text, keys, c.Out.Keys)
			continue
		}
		for i, key := range keys {
			if value, ok := got.Get(key); !ok || value != c.Out.Values[i] {
				m.errorf("ParseEnvLines(%q): %s = %q, Node has %q", c.Text, key, value, c.Out.Values[i])
			}
		}
	}
	for _, kind := range []string{EnvComment, EnvBlank, EnvPair} {
		if kinds[kind] < 300 {
			t.Errorf("only %d lines of kind %q", kinds[kind], kind)
		}
	}
}

func TestParseEnvFileAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "parse_env_file", len(v.ParseEnvFile), 700)
	m := &mismatches{t: t}
	for _, c := range v.ParseEnvFile {
		if got := ParseEnvFile(c.Text); !reflect.DeepEqual(got, c.Out) {
			m.errorf("ParseEnvFile(%q) = %q, Node answers %q", c.Text, got, c.Out)
		}
	}
}

func TestUpsertEnvKeyAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "upsert", len(v.Upsert), 2500)
	m := &mismatches{t: t}
	updates, crlf := 0, 0
	for _, c := range v.Upsert {
		content := c.Text
		if strings.Contains(content, "\r\n") {
			crlf++
		}
		for _, u := range c.Updates {
			updates++
			before := content
			content = UpsertEnvKey(content, u.Key, u.Value)
			if content != u.Out {
				m.errorf("UpsertEnvKey(%q, %q, %q) =\n%q\nNode answers\n%q", before, u.Key, u.Value, content, u.Out)
				content = u.Out
			}
		}
	}
	if updates < 6000 || crlf < 200 {
		t.Fatalf("%d updates, %d files with CRLF: too few", updates, crlf)
	}
}

func TestMergeEnvAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "merge", len(v.Merge), 1500)
	atLeast(t, "build_fresh", len(v.BuildFresh), 100)
	m := &mismatches{t: t}
	added := 0
	dropped := 0
	for _, c := range v.Merge {
		content, keys := MergeEnv(c.Existing, c.Template)
		added += len(keys)
		if content != c.Content {
			if withoutCarriedComments(c.Content, content) {
				dropped++
			} else {
				m.errorf("MergeEnv(%q, %q) =\n%q\nNode answers\n%q", c.Existing, c.Template, content, c.Content)
			}
		}
		if !reflect.DeepEqual(keys, c.Added) {
			m.errorf("MergeEnv(%q, %q) added %q, Node added %q", c.Existing, c.Template, keys, c.Added)
		}
	}
	if added < 1500 {
		t.Fatalf("only %d keys were added over all cases", added)
	}
	if dropped == 0 {
		t.Error("no recorded merge carried a comment along (SI6): the vectors do not test it")
	}
	t.Logf("%d merges leave out comments Node carried along (SI6)", dropped)
	for _, c := range v.BuildFresh {
		if got := BuildFreshEnv(c.Text); got != c.Out {
			m.errorf("BuildFreshEnv(%q) = %q, Node answers %q", c.Text, got, c.Out)
		}
	}
}

func TestSubstitutionsAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "substitute", len(v.Substitute), 600)
	atLeast(t, "assert_resolved", len(v.AssertResolved), 300)
	m := &mismatches{t: t}
	changed := 0
	for _, c := range v.Substitute {
		got := ApplySubstitutions(c.Text, c.Subs)
		if got != c.Out {
			m.errorf("ApplySubstitutions(%q, %q) = %q, Node answers %q", c.Text, c.Subs, got, c.Out)
		}
		if got != c.Text {
			changed++
		}
	}
	if changed < 50 {
		t.Fatalf("only %d texts changed", changed)
	}
	refused := 0
	for _, c := range v.AssertResolved {
		err := AssertResolvedSubstitutions(c.Subs)
		switch {
		case c.Err == nil && err != nil:
			m.errorf("AssertResolvedSubstitutions(%q) = %v, Node accepts", c.Subs, err)
		case c.Err != nil && (err == nil || err.Error() != *c.Err):
			m.errorf("AssertResolvedSubstitutions(%q) = %v, Node throws %q", c.Subs, err, *c.Err)
		}
		if c.Err != nil {
			refused++
		}
	}
	if refused < 30 {
		t.Fatalf("only %d refused sets", refused)
	}
}

func TestBuildEnvSubstitutionsAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "build_substitutions", len(v.BuildSubstitutions), 400)
	m := &mismatches{t: t}
	for _, c := range v.BuildSubstitutions {
		in := EnvSubstitutionInput{
			ExistingContent: c.ExistingContent,
			Config: EnvConfig{
				RustdeskDir:          c.Config["rustdeskDir"],
				KeysPath:             c.Config["keysPath"],
				GoAPIPort:            c.Config["goApiPort"],
				APIPort:              c.Config["apiPort"],
				DBType:               c.Config["dbType"],
				SSLCertPath:          c.Config["sslCertPath"],
				SSLKeyPath:           c.Config["sslKeyPath"],
				RemoteTargetVaultKey: c.Config["remoteTargetVaultKey"],
				PubKeyPath:           c.Config["pubKeyPath"],
				APIKeyPath:           c.Config["apiKeyPath"],
				DBPath:               c.Config["dbPath"],
				DatabaseURL:          c.Config["databaseUrl"],
				DataDir:              c.Config["dataDir"],
				SessionSecret:        c.Config["sessionSecret"],
			},
			Random: bytes.NewReader(bytes.Repeat([]byte{c.RandomByte}, 32)),
		}
		if c.UseMap {
			in.EnvMap, in.ExistingContent = ParseEnvLines(c.ExistingContent), "IGNORED=1\nRUSTDESK_DIR=/ignored\n"
		}
		got, err := BuildEnvSubstitutions(in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pairs(got), c.Out) {
			m.errorf("BuildEnvSubstitutions(%q, %+v) =\n%q\nNode answers\n%q", c.ExistingContent, c.Config, got, c.Out)
		}
	}
}

// The source of random bytes is read on every call and its failure is an
// error, not a weak key.
func TestBuildEnvSubstitutionsRandomFailure(t *testing.T) {
	if _, err := BuildEnvSubstitutions(EnvSubstitutionInput{Random: bytes.NewReader(make([]byte, 31))}); err == nil {
		t.Fatal("31 random bytes were enough")
	}
	subs, err := BuildEnvSubstitutions(EnvSubstitutionInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range subs {
		if s.Key == "REMOTE_TARGET_VAULT_KEY" && len(s.Value) != 43 {
			t.Fatalf("vault key %q is not 32 bytes in base64url", s.Value)
		}
	}
}

func TestMergeEnvFileAgainstNode(t *testing.T) {
	var v envMergeVectors
	loadVectors(t, "envmerge", &v)
	atLeast(t, "merge_file", len(v.MergeFile), 400)
	dir := t.TempDir()
	m := &mismatches{t: t}
	modes := map[string]int{}
	dropped := 0
	for i, c := range v.MergeFile {
		label := "case " + strconv.Itoa(i)
		target, template := filepath.Join(dir, c.Name), filepath.Join(dir, c.TemplateName)
		if c.Template != nil {
			if err := os.WriteFile(template, []byte(*c.Template), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if c.Target != nil {
			if err := os.WriteFile(target, []byte(*c.Target), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := MergeEnvFile(OSFS{}, MergeEnvFileOptions{TargetPath: target, TemplatePath: template, FreshInstall: c.FreshInstall, Substitutions: c.Subs})
		switch {
		case c.Err != nil:
			want := strings.ReplaceAll(*c.Err, "{DIR}", dir)
			if err == nil || err.Error() != want {
				m.errorf("%s: error %v, Node throws %q", label, err, want)
			}
			modes["error"]++
		case err != nil:
			m.errorf("%s: error %v, Node answers %+v", label, err, *c.Out)
		default:
			if !reflect.DeepEqual(got, *c.Out) {
				m.errorf("%s: %+v, Node answers %+v", label, got, *c.Out)
			}
			modes[got.Mode]++
		}

		var want *fileState
		if c.After != nil {
			want = &fileState{Bytes: c.After, Mode: c.Mode}
			// Go leaves out comments Node carried along (SI6)
			node := string(decodeBase64(t, *c.After))
			if raw, err := os.ReadFile(target); err == nil && string(raw) != node && withoutCarriedComments(node, string(raw)) {
				fixed := base64.StdEncoding.EncodeToString(raw)
				want.Bytes = &fixed
				dropped++
			}
		}
		checkFile(t, m, label, target, want)
	}
	if dropped == 0 {
		t.Error("no recorded merge carried a comment along (SI6): the vectors do not test it")
	}
	for _, mode := range []string{"fresh", "merge", "error"} {
		if modes[mode] < 40 {
			t.Errorf("only %d cases ended as %q", modes[mode], mode)
		}
	}
}

// The update from the Jest test of lib/envMerge.js: existing keys stay,
// missing ones are appended.
func TestMergeEnvKeepsExistingKeys(t *testing.T) {
	existing := "# custom\nMY_FEATURE=true\nPORT=5000\nSESSION_SECRET=keep-me\n"
	template := ApplySubstitutions("PORT=5000\nHOST=0.0.0.0\nTRUST_PROXY=false\nSESSION_SECRET=__SESSION_SECRET__\n",
		[]Substitution{{"SESSION_SECRET", "new-should-not-apply"}})
	content, added := MergeEnv(existing, template)
	want := existing + "HOST=0.0.0.0\nTRUST_PROXY=false\n"
	if content != want {
		t.Errorf("content\n%q\nwant\n%q", content, want)
	}
	if !reflect.DeepEqual(added, []string{"HOST", "TRUST_PROXY"}) {
		t.Errorf("added %q", added)
	}
}

// TestMergeEnvDropsCommentsOfKeysTheFileHas adds a missing key with the
// comments that stand right above it in the template, not with those of
// the keys the file has already (BUGS SI6: they were carried along).
func TestMergeEnvDropsCommentsOfKeysTheFileHas(t *testing.T) {
	existing := "A=1\nC=3\n"
	template := "# about A\nA=x\n\n# about B\nB=2\n# about C\nC=x\n# about D\nD=4\n"
	content, added := MergeEnv(existing, template)
	want := "A=1\nC=3\n\n# about B\nB=2\n# about D\nD=4\n"
	if content != want {
		t.Errorf("content\n%q\nwant\n%q", content, want)
	}
	if !reflect.DeepEqual(added, []string{"B", "D"}) {
		t.Errorf("added %q", added)
	}
}

// The placeholders from the Jest test of buildEnvSubstitutions: values come
// from the existing file first, then from the settings.
func TestBuildEnvSubstitutionsFromExistingAndConfig(t *testing.T) {
	existing := "PORT=5000\nRUSTDESK_DIR=/opt/custom\nSESSION_SECRET=keep-me\n"
	subs, err := BuildEnvSubstitutions(EnvSubstitutionInput{
		ExistingContent: existing,
		Config:          EnvConfig{GoAPIPort: "21114", APIPort: "21121", DataDir: "/app/data"},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]string{}
	for _, s := range subs {
		value[s.Key] = s.Value
	}
	for key, want := range map[string]string{
		"RUSTDESK_DIR":   "/opt/custom",
		"SESSION_SECRET": "keep-me",
		"GO_API_PORT":    "21114",
		"HBBS_API_URL":   "http://localhost:21114/api",
		"PUB_KEY_PATH":   "/opt/custom/id_ed25519.pub",
		"DATA_DIR":       "/app/data",
	} {
		if value[key] != want {
			t.Errorf("%s = %q, want %q", key, value[key], want)
		}
	}
	if err := AssertResolvedSubstitutions(subs); err != nil {
		t.Errorf("unresolved: %v", err)
	}
	template := ApplySubstitutions("TRUST_PROXY=false\nHOST=0.0.0.0\nSESSION_SECRET=__SESSION_SECRET__\n", subs)
	content, added := MergeEnv(existing, template)
	if content != existing+"TRUST_PROXY=false\nHOST=0.0.0.0\n" || !reflect.DeepEqual(added, []string{"TRUST_PROXY", "HOST"}) {
		t.Errorf("content %q, added %q", content, added)
	}
}

func TestProtectedEnvKeys(t *testing.T) {
	want := []string{"SESSION_SECRET", "DEFAULT_ADMIN_PASSWORD", "DEFAULT_ADMIN_USERNAME", "DATABASE_URL", "DB_TYPE", "DB_PATH",
		"ORG_PEER_VAULT_KEY", "REMOTE_TARGET_VAULT_KEY", "JWT_SECRET"}
	if !reflect.DeepEqual(ProtectedEnvKeys, want) {
		t.Errorf("ProtectedEnvKeys = %q", ProtectedEnvKeys)
	}
}

// A file with bytes that are not UTF-8 is read the way Node read it: each
// broken sequence is one U+FFFD, and that is what is written back.
func TestMergeEnvFileBrokenUTF8(t *testing.T) {
	dir := t.TempDir()
	target, template := filepath.Join(dir, ".env"), filepath.Join(dir, ".env.example")
	if err := os.WriteFile(target, []byte("A=\xff\xfe\nB=\xe2\x82\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte("C=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeEnvFile(OSFS{}, MergeEnvFileOptions{TargetPath: target, TemplatePath: template}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if want := "A=\ufffd\ufffd\nB=\ufffd\nC=1\n"; string(got) != want {
		t.Errorf("file holds %q, want %q", got, want)
	}
}
