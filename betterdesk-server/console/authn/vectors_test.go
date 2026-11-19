package authn

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// The tests in this file replay the vectors under testdata/vectors, which
// testdata/nodeoracle/pure.js computed with the Node console's own code.

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
	return &Service{rand: bytes.NewReader(nil), bcryptCost: bcryptMinCost, dummy: dummyHash, log: discardLogger{}, now: time.Now, zone: time.UTC}
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
	for _, group := range []string{"bcrypt-2b", "bcrypt-2a", "bcrypt-2", "bcrypt-relabel", "pbkdf2-modern", "malformed"} {
		if groups[group] == 0 {
			t.Errorf("no vectors of group %s", group)
		}
	}
}

func TestStrengthVectors(t *testing.T) {
	var vectors []struct {
		Password string          `json:"password"`
		Result   json.RawMessage `json:"result"`
	}
	loadVectors(t, "strength.json", &vectors)
	if len(vectors) < 3000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for i, v := range vectors {
		got, err := json.Marshal(ValidatePasswordStrength(v.Password))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(v.Result) {
			t.Errorf("vector %d: strength of %q = %s, Node says %s", i, v.Password, got, v.Result)
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

	thrown, accepted := 0, 0
	for i, v := range vectors.Codes {
		got, err := totpCode(v.Secret, time.UnixMilli(v.At))
		if v.Thrown != nil {
			thrown++
			if err == nil || err.Error() != *v.Thrown {
				t.Errorf("code %d: secret %q: got %q, %v; Node throws %q", i, v.Secret, got, err, *v.Thrown)
			}
			continue
		}
		var want string
		if err := json.Unmarshal(v.Result, &want); err != nil {
			t.Fatal(err)
		}
		if err != nil || got != want {
			t.Errorf("code %d: totpCode(%q, %d) = %q, %v; Node says %q", i, v.Secret, v.At, got, err, want)
		}
	}
	if thrown == 0 {
		t.Error("no vector with a secret Node refuses")
	}

	for i, v := range vectors.Checks {
		got, err := totpVerify(v.Token, v.Secret, time.UnixMilli(v.At))
		if v.Thrown != nil {
			if err == nil || err.Error() != *v.Thrown {
				t.Errorf("check %d: secret %q: got %v, %v; Node throws %q", i, v.Secret, got, err, *v.Thrown)
			}
			continue
		}
		want := v.bool(t)
		if want {
			accepted++
		}
		if err != nil || got != want {
			t.Errorf("check %d: totpVerify(%q, %q, %d) = %v, %v; Node says %v", i, v.Token, v.Secret, v.At, got, err, want)
		}
	}
	if accepted < 100 {
		t.Errorf("only %d checks Node accepts", accepted)
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
