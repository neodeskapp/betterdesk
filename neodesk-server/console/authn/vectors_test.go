package authn

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The tests in this file replay the vectors under testdata/vectors, which
// were computed with the Node console's own code (testdata/README.md).

func readVectorFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join("testdata", "vectors", name))
}

func loadVectors(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := readVectorFile(name)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

// outcome is what a Node call gave: a result, or the message it threw.
type outcome struct {
	Result json.RawMessage `json:"result"`
	Thrown *string         `json:"thrown"`
}

func (o outcome) bool(t *testing.T) bool {
	t.Helper()
	var b bool
	if err := json.Unmarshal(o.Result, &b); err != nil {
		t.Fatalf("result %s is not a boolean", o.Result)
	}
	return b
}

// pureService is a service for the functions that need no database.
func pureService() *Service {
	return &Service{rand: bytes.NewReader(nil), hashIterations: testIterations, recoveryCost: testRecoveryCost, dummy: dummyHash, log: discardLogger{}, now: time.Now, zone: time.UTC}
}

func TestHashVectors(t *testing.T) {
	var vectors []struct {
		Group    string `json:"group"`
		Password string `json:"password"`
		Hash     string `json:"hash"`
		Produced bool   `json:"produced"`
		outcome
	}
	loadVectors(t, "hashes.json", &vectors)
	if len(vectors) < 4000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	s := pureService()
	ctx := context.Background()
	produced := regexp.MustCompile(`^\$2b\$12\$[./A-Za-z0-9]{53}$`)
	groups := map[string]int{}
	// AN5: for a "$2a$" hash Node counted the password's length plus its
	// closing zero byte in one byte, and for "$2$" the length alone, so of a
	// password of 255 (256) bytes or more only the length modulo 256
	// counted. The console reads such a password like any long one, by its
	// first 72 bytes: it gets the answer Node gave for those 72 bytes where
	// the vectors have it.
	node := map[[2]string]bool{}
	for _, v := range vectors {
		if v.Thrown == nil {
			node[[2]string{v.Password, v.Hash}] = v.bool(t)
		}
	}
	wrapped := func(password, hash string) bool {
		n := len(toWellFormed(password))
		return strings.HasPrefix(hash, "$2a$") && n+1 > 255 || strings.HasPrefix(hash, "$2$") && n > 255
	}
	changed := 0
	for i, v := range vectors {
		slow := v.Group == "node-hashed-cost12" || v.Group == "pbkdf2-legacy" || v.Group == "fixed"
		if slow && testing.Short() {
			continue
		}
		groups[v.Group]++
		if v.Thrown != nil {
			t.Fatalf("vector %d (%s): Node threw %q; no such case is expected", i, v.Group, *v.Thrown)
		}
		want := v.bool(t)
		if wrapped(v.Password, v.Hash) {
			first72 := string([]byte(toWellFormed(v.Password))[:bcryptKeyBytes])
			fixed, recorded := node[[2]string{first72, v.Hash}]
			if !recorded {
				fixed, _ = s.VerifyPassword(ctx, first72, v.Hash)
			}
			if fixed != want {
				changed++
			}
			want = fixed
		}
		got, err := s.VerifyPassword(ctx, v.Password, v.Hash)
		if err != nil {
			t.Errorf("vector %d (%s) hash %q: error %v", i, v.Group, v.Hash, err)
			continue
		}
		if got != want {
			t.Errorf("vector %d (%s): VerifyPassword(%q, %q) = %v, Node says %v", i, v.Group, v.Password, v.Hash, got, want)
		}
		if v.Produced && !produced.MatchString(v.Hash) {
			t.Errorf("vector %d: the console's hash %q has not the form $2b$12$ and 53 letters", i, v.Hash)
		}
	}
	if changed == 0 && !testing.Short() {
		t.Error("no vector changes with AN5")
	}
	for _, group := range []string{"bcrypt-2b", "bcrypt-2a", "bcrypt-2", "bcrypt-relabel", "pbkdf2-modern", "malformed"} {
		if groups[group] == 0 {
			t.Errorf("no vectors of group %s", group)
		}
	}
}

func TestTOTPVectors(t *testing.T) {
	var vectors struct {
		Codes []struct {
			Secret string `json:"secret"`
			At     int64  `json:"at"`
			outcome
		} `json:"codes"`
		Checks []struct {
			Secret string `json:"secret"`
			At     int64  `json:"at"`
			Token  string `json:"token"`
			outcome
		} `json:"checks"`
		URLs []struct {
			Account string  `json:"account"`
			Secret  string  `json:"secret"`
			Issuer  *string `json:"issuer"`
			URL     string  `json:"url"`
		} `json:"urls"`
		Generated []struct {
			Random string `json:"random"`
			Secret string `json:"secret"`
		} `json:"generated"`
	}
	loadVectors(t, "totp.json", &vectors)
	if len(vectors.Codes) < 3000 || len(vectors.Checks) < 1500 || len(vectors.URLs) < 300 || len(vectors.Generated) < 60 {
		t.Fatalf("too few vectors: %d codes, %d checks, %d urls, %d secrets",
			len(vectors.Codes), len(vectors.Checks), len(vectors.URLs), len(vectors.Generated))
	}

	// AN8: the console reads a secret as the API server does and takes its
	// bytes as they are. Where Node read a secret leniently, threw on one,
	// or stretched a short one, the code is the API server's
	// (auth.ComputeTOTP), and a secret the API server cannot read has none
	// and no token passes for it.
	reference := func(secret string, at time.Time) (string, bool) {
		code, err := auth.ComputeTOTP(secret, at)
		return code, err == nil
	}
	thrown, accepted, changed := 0, 0, 0
	for i, v := range vectors.Codes {
		at := time.UnixMilli(v.At)
		got, err := totpCode(v.Secret, at)
		ref, readable := reference(v.Secret, at)
		var node string
		if v.Thrown != nil {
			thrown++
		} else if err := json.Unmarshal(v.Result, &node); err != nil {
			t.Fatal(err)
		}
		if v.Thrown != nil || !readable || node != ref {
			changed++
		}
		switch {
		case readable && (err != nil || got != ref):
			t.Errorf("code %d: totpCode(%q, %d) = %q, %v; want %q (Node: %q, %v)", i, v.Secret, v.At, got, err, ref, node, v.Thrown)
		case !readable && err == nil:
			t.Errorf("code %d: totpCode(%q, %d) = %q for a secret that is not base32", i, v.Secret, v.At, got)
		}
	}
	if thrown == 0 || changed == 0 || changed == len(vectors.Codes) {
		t.Errorf("%d secrets Node refuses, %d of %d codes change with AN8; want some of each", thrown, changed, len(vectors.Codes))
	}

	changed = 0
	for i, v := range vectors.Checks {
		at := time.UnixMilli(v.At)
		got, err := totpVerify(v.Token, v.Secret, at)
		ref, readable := reference(v.Secret, at)
		want := readable && v.Token == ref
		if v.Thrown != nil || v.bool(t) != want {
			changed++
		} else if want {
			accepted++
		}
		if err != nil || got != want {
			t.Errorf("check %d: totpVerify(%q, %q, %d) = %v, %v; want %v (Node: %s, %v)", i, v.Token, v.Secret, v.At, got, err, want, v.Result, v.Thrown)
		}
	}
	if accepted < 100 || changed == 0 {
		t.Errorf("%d checks accepted as in Node, %d changed by AN8", accepted, changed)
	}

	for i, v := range vectors.URLs {
		got := OtpauthURL(v.Account, v.Secret)
		if v.Issuer != nil {
			got = otpauthURL(v.Account, v.Secret, *v.Issuer)
		}
		if got != v.URL {
			t.Errorf("url %d: account %q: %q, Node says %q", i, v.Account, got, v.URL)
		}
	}

	// AN1: Node cleared the top bit of every random byte; the console keeps
	// it. Where a byte had it set, Node's secret is the one of the cleared
	// bytes and the console's the one of the bytes as they are.
	encode := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString
	newSecrets := 0
	for i, v := range vectors.Generated {
		random, err := hex.DecodeString(v.Random)
		if err != nil {
			t.Fatal(err)
		}
		cleared := bytes.Clone(random)
		for j := range cleared {
			cleared[j] &= 0x7f
		}
		if encode(cleared) != v.Secret {
			t.Errorf("secret %d: Node's %q is not the one of the bytes %s with their top bit cleared", i, v.Secret, v.Random)
		}
		want := v.Secret
		if !bytes.Equal(cleared, random) {
			want = encode(random)
			newSecrets++
		}
		s := pureService()
		s.rand = bytes.NewReader(random)
		got, err := s.generateSecret()
		if err != nil || got != want {
			t.Errorf("secret %d: from %s: %q, %v; want %q (Node says %q)", i, v.Random, got, err, want, v.Secret)
		}
	}
	if newSecrets == 0 {
		t.Errorf("none of %d generated secrets changes with AN1", len(vectors.Generated))
	}
}

func parseJSONValue(t *testing.T, raw json.RawMessage) (value any, present bool) {
	t.Helper()
	if raw == nil {
		return nil, false
	}
	v, err := goapi.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return v, true
}

// refusedMatches are the match vectors that Node let through and a bug fix
// of BUGS.md refuses as another device, with the bug: only text names a
// device, where Node compared the text String() made of a number, true or
// false, or a list (DV4).
var refusedMatches = map[int]string{
	30: "DV4", 32: "DV4", 60: "DV4", 62: "DV4",
	78: "DV4", 79: "DV4", 80: "DV4", 81: "DV4", 82: "DV4", 83: "DV4",
	96: "DV4", 97: "DV4", 98: "DV4", 99: "DV4", 100: "DV4", 101: "DV4",
	156: "DV4", 157: "DV4", 158: "DV4", 159: "DV4", 160: "DV4", 161: "DV4",
	186: "DV4", 197: "DV4", 212: "DV4", 218: "DV4", 221: "DV4", 233: "DV4",
	236: "DV4", 240: "DV4", 241: "DV4", 246: "DV4", 247: "DV4", 259: "DV4",
	263: "DV4", 264: "DV4", 277: "DV4", 284: "DV4", 297: "DV4", 318: "DV4",
	320: "DV4", 344: "DV4", 346: "DV4", 365: "DV4", 367: "DV4", 376: "DV4",
	387: "DV4", 390: "DV4", 397: "DV4", 430: "DV4", 433: "DV4", 435: "DV4",
	448: "DV4", 452: "DV4", 454: "DV4", 475: "DV4",
}

func TestTokenVectors(t *testing.T) {
	var vectors struct {
		Hashes []struct {
			Token string `json:"token"`
			Hash  string `json:"hash"`
		} `json:"hashes"`
		Bearer []struct {
			Header string  `json:"header"`
			Token  *string `json:"token"`
		} `json:"bearer"`
		DeviceIDs []struct {
			ID string `json:"id"`
			OK bool   `json:"ok"`
		} `json:"deviceIds"`
		Match []struct {
			DeviceID string          `json:"deviceId"`
			Body     json.RawMessage `json:"body"`
			Query    json.RawMessage `json:"query"`
			Header   *string         `json:"header"`
			Answer   struct {
				Next   bool `json:"next"`
				Status int  `json:"status"`
				Body   struct {
					Error string `json:"error"`
				} `json:"body"`
			} `json:"answer"`
		} `json:"match"`
	}
	loadVectors(t, "tokens.json", &vectors)
	if len(vectors.Hashes) < 200 || len(vectors.Bearer) < 200 || len(vectors.DeviceIDs) < 190 || len(vectors.Match) < 400 {
		t.Fatalf("too few vectors: %d hashes, %d headers, %d ids, %d matches",
			len(vectors.Hashes), len(vectors.Bearer), len(vectors.DeviceIDs), len(vectors.Match))
	}
	for i, v := range vectors.Hashes {
		if got := HashAccessToken(v.Token); got != v.Hash {
			t.Errorf("hash %d: HashAccessToken(%q) = %s, Node says %s", i, v.Token, got, v.Hash)
		}
		if got := HashDeviceToken(v.Token); got != v.Hash {
			t.Errorf("hash %d: HashDeviceToken(%q) = %s, Node says %s", i, v.Token, got, v.Hash)
		}
	}
	found := 0
	for i, v := range vectors.Bearer {
		got, ok := ExtractBearerToken(v.Header)
		switch {
		case v.Token == nil && ok:
			t.Errorf("header %d: %q gave the token %q, Node finds none", i, v.Header, got)
		case v.Token != nil && (!ok || got != *v.Token):
			t.Errorf("header %d: %q gave %q, %v; Node says %q", i, v.Header, got, ok, *v.Token)
		}
		if v.Token != nil {
			found++
		}
	}
	if found < 20 {
		t.Errorf("only %d headers with a token", found)
	}
	for i, v := range vectors.DeviceIDs {
		if got := IsDeviceID(v.ID); got != v.OK {
			t.Errorf("id %d: IsDeviceID(%q) = %v, Node says %v", i, v.ID, got, v.OK)
		}
	}
	refused := 0
	for i, v := range vectors.Match {
		body, hasBody := parseJSONValue(t, v.Body)
		query, hasQuery := parseJSONValue(t, v.Query)
		header := ClaimFromText("", false)
		if v.Header != nil {
			header = ClaimFromText(*v.Header, true)
		}
		err := RequireTokenDeviceMatch(v.DeviceID, ClaimFromJSON(body, hasBody), ClaimFromJSON(query, hasQuery), header)
		if bug, fixed := refusedMatches[i]; fixed {
			if !v.Answer.Next || !errors.Is(err, ErrDeviceMismatch) {
				t.Errorf("match %d (%s): device %q body %s query %s header %v: got %v, Node passes %v; want the mismatch where Node let it pass",
					i, bug, v.DeviceID, v.Body, v.Query, v.Header, err, v.Answer.Next)
			}
			continue
		}
		if v.Answer.Next {
			if err != nil {
				t.Errorf("match %d: device %q body %s query %s header %v: refused, Node lets it pass", i, v.DeviceID, v.Body, v.Query, v.Header)
			}
			continue
		}
		refused++
		refusal, isRefusal := err.(*DeviceAuthError)
		if !isRefusal || refusal.Status != v.Answer.Status || refusal.Text != v.Answer.Body.Error {
			t.Errorf("match %d: device %q body %s query %s header %v: got %v, Node answers %d %q",
				i, v.DeviceID, v.Body, v.Query, v.Header, err, v.Answer.Status, v.Answer.Body.Error)
		}
	}
	if refused < 100 || refused > len(vectors.Match)-50 {
		t.Errorf("%d of %d matches refused: the vectors are lopsided", refused, len(vectors.Match))
	}
}

func TestOIDCVectors(t *testing.T) {
	var vectors struct {
		Parsed []struct {
			Raw      string `json:"raw"`
			OK       bool   `json:"ok"`
			Valid    bool   `json:"valid"`
			Reason   string `json:"reason"`
			Pathname string `json:"pathname"`
			Origin   string `json:"origin"`
		} `json:"parsed"`
		Suggested []struct {
			Origin string `json:"origin"`
			Result string `json:"result"`
		} `json:"suggested"`
		PrivateIPs []struct {
			IP      string `json:"ip"`
			Private bool   `json:"private"`
		} `json:"privateIps"`
	}
	loadVectors(t, "oidc.json", &vectors)
	if len(vectors.Parsed) < 2500 || len(vectors.Suggested) < 2500 || len(vectors.PrivateIPs) < 300 {
		t.Fatalf("too few vectors: %d parsed, %d suggested, %d addresses", len(vectors.Parsed), len(vectors.Suggested), len(vectors.PrivateIPs))
	}
	reasons := map[string]int{}
	for i, v := range vectors.Parsed {
		got := ParseOIDCRedirectURL(v.Raw)
		want := OIDCRedirectURL{OK: v.OK, Reason: v.Reason, Pathname: v.Pathname, Origin: v.Origin}
		if got != want {
			t.Errorf("parsed %d: %q = %+v, Node says %+v", i, v.Raw, got, want)
		}
		if IsValidOIDCRedirectURL(v.Raw) != v.Valid {
			t.Errorf("parsed %d: IsValidOIDCRedirectURL(%q) is not %v", i, v.Raw, v.Valid)
		}
		reasons[v.Reason]++
	}
	for _, reason := range []string{"", "empty", "invalid", "scheme", "path"} {
		if reasons[reason] < 3 {
			t.Errorf("only %d vectors with reason %q", reasons[reason], reason)
		}
	}
	suggestions := 0
	for i, v := range vectors.Suggested {
		if got := SuggestOIDCRedirectURL(v.Origin); got != v.Result {
			t.Errorf("suggested %d: %q = %q, Node says %q", i, v.Origin, got, v.Result)
		}
		if v.Result != "" {
			suggestions++
		}
	}
	if suggestions < 100 {
		t.Errorf("only %d origins with a suggestion", suggestions)
	}
	private := 0
	for i, v := range vectors.PrivateIPs {
		if got := IsPrivateClientIP(v.IP); got != v.Private {
			t.Errorf("address %d: IsPrivateClientIP(%q) = %v, Node says %v", i, v.IP, got, v.Private)
		}
		if v.Private {
			private++
		}
	}
	if private < 20 {
		t.Errorf("only %d private addresses", private)
	}
}

func TestProviderVectors(t *testing.T) {
	var vectors struct {
		Normalize []struct {
			Name     string `json:"name"`
			Result   string `json:"result"`
			External bool   `json:"external"`
		} `json:"normalize"`
		Infer []struct {
			Go *struct {
				Provider *string `json:"auth_provider"`
				Role     string  `json:"role"`
			} `json:"go"`
			SSO struct {
				LDAP bool `json:"ldap"`
				OIDC bool `json:"oidc"`
				Any  bool `json:"any"`
			} `json:"sso"`
			Result   string `json:"result"`
			External bool   `json:"external"`
		} `json:"infer"`
	}
	loadVectors(t, "providers.json", &vectors)
	if len(vectors.Normalize) < 20 || len(vectors.Infer) < 90 {
		t.Fatalf("too few vectors: %d names, %d inferences", len(vectors.Normalize), len(vectors.Infer))
	}
	for i, v := range vectors.Normalize {
		if got := normalizeAuthProvider(v.Name); got != v.Result {
			t.Errorf("name %d: normalizeAuthProvider(%q) = %q, Node says %q", i, v.Name, got, v.Result)
		}
		if got := isExternalAuthProvider(v.Name); got != v.External {
			t.Errorf("name %d: isExternalAuthProvider(%q) = %v, Node says %v", i, v.Name, got, v.External)
		}
	}
	for i, v := range vectors.Infer {
		var accept *goAccept
		if v.Go != nil {
			accept = &goAccept{role: v.Go.Role}
			if v.Go.Provider != nil {
				accept.provider = *v.Go.Provider
			}
		}
		sso := ssoStatus{ldap: v.SSO.LDAP, oidc: v.SSO.OIDC, any: v.SSO.Any}
		if got := inferAuthProvider(accept, sso); got != v.Result {
			t.Errorf("inference %d: %+v with %+v = %q, Node says %q", i, v.Go, v.SSO, got, v.Result)
		}
		external := accept != nil && isExternalAuthProvider(accept.provider)
		if external != v.External {
			t.Errorf("inference %d: %+v: external = %v, Node says %v", i, v.Go, external, v.External)
		}
	}
}
