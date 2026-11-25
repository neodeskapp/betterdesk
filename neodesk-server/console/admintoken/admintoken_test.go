package admintoken

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The rule secret scanners use (.github/gitleaks.toml has the same).
var scannerRule = regexp.MustCompile(`\bndt_[a-km-z2-9]{12}_[A-Za-z0-9]{40}\b`)

func newService(t *testing.T, db *store.DB, now func() time.Time) *Service {
	t.Helper()
	s, err := New(context.Background(), Options{DB: db, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func holdsAll(string) bool { return true }

var known = []string{"device.view", "device.edit", "audit.view", "user.view"}

func TestTokensHaveTheirShapeAndAreKeptAsHashes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db, nil)
		value, tok, err := s.Create(context.Background(), Creator{ID: 7, Username: "ann", Holds: holdsAll},
			Input{Name: " build ", Permissions: []string{"device.view", "audit.view", "device.view"}, ExpiresAt: time.Now().Add(time.Hour)}, known)
		if err != nil {
			t.Fatal(err)
		}
		if !scannerRule.MatchString(value) || len(value) != tokenLength {
			t.Errorf("token %q does not fit the scanner rule", value)
		}
		if tok.Name != "build" || strings.Join(tok.Permissions, ",") != "audit.view,device.view" {
			t.Errorf("token = %+v", tok)
		}
		var hash string
		if err := db.QueryRow(context.Background(), `SELECT token_hash FROM admin_tokens WHERE public_id = ?`, tok.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if hash != hashToken(value) || strings.Contains(hash, value[len(Prefix)+idLength+1:]) {
			t.Errorf("stored %q", hash)
		}
		got, err := s.Authenticate(context.Background(), value, "10.0.0.1")
		if err != nil || got.ID != tok.ID || got.ReadOnly {
			t.Fatalf("Authenticate = %+v, %v", got, err)
		}
	})
}

func TestParseTakesOnlyTheTokenShape(t *testing.T) {
	good := Prefix + "abcdefghijkm" + "_" + strings.Repeat("A1", 20)
	if id, ok := Parse(good); !ok || id != "abcdefghijkm" {
		t.Errorf("Parse(good) = %q, %v", id, ok)
	}
	for _, bad := range []string{
		"", "ndt_", good + "x", good[:len(good)-1], strings.Replace(good, "ndt_", "ndx_", 1),
		strings.Replace(good, "_A1", "-A1", 1), Prefix + "abcdefghijk0" + good[len(Prefix)+idLength:],
		good[:len(good)-1] + "_", good[:len(good)-1] + "é",
	} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) took it", bad)
		}
	}
}

func TestFromHeaderTakesOnlyOurBearerTokens(t *testing.T) {
	for header, want := range map[string]bool{
		"Bearer ndt_x": true, "bearer ndt_x": true, "Bearer  ndt_x ": true,
		"Bearer abc": false, "Basic ndt_x": false, "ndt_x": false, "": false,
	} {
		r, _ := http.NewRequest("GET", "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if _, ok := FromHeader(r); ok != want {
			t.Errorf("FromHeader(%q) = %v", header, ok)
		}
	}
}

func TestAllowListsTakeAddressesAndNetworks(t *testing.T) {
	list, err := CleanAllowList([]string{" 192.0.2.10 ", "198.51.100.7/24", "::ffff:192.0.2.11", "2001:db8::/32", "192.0.2.10", ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(list, " ") != "192.0.2.10 198.51.100.0/24 192.0.2.11 2001:db8::/32" {
		t.Errorf("list = %v", list)
	}
	tok := &Token{AllowList: list}
	for ip, want := range map[string]bool{
		"192.0.2.10": true, "::ffff:192.0.2.10": true, "198.51.100.200": true, "192.0.2.12": false,
		"2001:db8:1::5": true, "2001:db9::1": false, "": false, "nonsense": false,
	} {
		if got := tok.Allows(ip); got != want {
			t.Errorf("Allows(%q) = %v", ip, got)
		}
	}
	if !(&Token{}).Allows("203.0.113.1") {
		t.Error("an empty list should allow every address")
	}
	if _, err := CleanAllowList([]string{"192.0.2.300"}); err == nil {
		t.Error("a bad address passed")
	}
	many := make([]string, MaxAllowList+1)
	for i := range many {
		many[i] = "10.0.0." + string(rune('0'+i%10)) + string(rune('0'+i/10))
	}
	if _, err := CleanAllowList(many); err == nil {
		t.Error("too long a list passed")
	}
}

func TestCreateChecksEveryField(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		s := newService(t, db, func() time.Time { return now })
		creator := Creator{ID: 3, Username: "srv", Holds: func(p string) bool { return p != "user.view" }}
		ok := Input{Name: "x", Permissions: []string{"device.view"}, ExpiresAt: now.Add(time.Hour)}
		for name, c := range map[string]struct {
			change func(*Input)
			field  string
		}{
			"not held":       {func(in *Input) { in.Permissions = []string{"user.view"} }, "permissions"},
			"unknown":        {func(in *Input) { in.Permissions = []string{"nope"} }, "permissions"},
			"none":           {func(in *Input) { in.Permissions = nil }, "permissions"},
			"no name":        {func(in *Input) { in.Name = "  " }, "name"},
			"long name":      {func(in *Input) { in.Name = strings.Repeat("a", MaxNameLength+1) }, "name"},
			"control":        {func(in *Input) { in.Name = "a\nb" }, "name"},
			"no expiry":      {func(in *Input) { in.ExpiresAt = time.Time{} }, "expires_at"},
			"past":           {func(in *Input) { in.ExpiresAt = now.Add(-time.Second) }, "expires_at"},
			"over a year":    {func(in *Input) { in.ExpiresAt = now.Add(MaxLifetime + time.Second) }, "expires_at"},
			"bad allow list": {func(in *Input) { in.AllowList = []string{"x"} }, "allow_list"},
		} {
			in := ok
			c.change(&in)
			_, _, err := s.Create(context.Background(), creator, in, known)
			bad, isInput := err.(*InputError)
			if !isInput || bad.Field != c.field {
				t.Errorf("%s: err = %v, want a %s error", name, err, c.field)
			}
		}
		if _, _, err := s.Create(context.Background(), creator, ok, known); err != nil {
			t.Errorf("good input: %v", err)
		}
	})
}

func TestAuthenticateRefusesWhatIsNotLive(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		clock := func() time.Time { return now }
		s := newService(t, db, clock)
		mint := func(in Input) string {
			in.Name, in.Permissions = "t", []string{"device.view"}
			if in.ExpiresAt.IsZero() {
				in.ExpiresAt = now.Add(time.Hour)
			}
			value, _, err := s.Create(context.Background(), Creator{ID: 1, Holds: holdsAll}, in, known)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		live := mint(Input{})
		revoked := mint(Input{})
		id, _ := Parse(revoked)
		if done, err := s.Revoke(context.Background(), id, "ann"); err != nil || done == nil {
			t.Fatalf("Revoke = %v, %v", done, err)
		}
		if done, _ := s.Revoke(context.Background(), id, "ann"); done != nil {
			t.Error("a second revoke reported a change")
		}
		soon := mint(Input{ExpiresAt: now.Add(time.Minute)})
		fenced := mint(Input{AllowList: []string{"10.1.0.0/16"}})
		last := "A"
		if live[len(live)-1] == 'A' {
			last = "B"
		}
		wrong := live[:len(live)-1] + last
		unknown := Prefix + "abcdefghijkm_" + live[len(Prefix)+idLength+1:]

		now = now.Add(2 * time.Minute)
		for value, reason := range map[string]string{
			revoked: RefusedRevoked, soon: RefusedExpired, fenced: RefusedAddress,
			wrong: RefusedSecret, unknown: RefusedUnknown, "ndt_short": RefusedMalformed,
		} {
			_, err := s.Authenticate(context.Background(), value, "10.2.0.1")
			refusal, ok := err.(*Refusal)
			if !ok || refusal.Reason != reason {
				t.Errorf("%q: err = %v, want %s", value, err, reason)
			}
		}
		if _, err := s.Authenticate(context.Background(), fenced, "10.1.2.3"); err != nil {
			t.Errorf("fenced token from inside its network: %v", err)
		}
		if _, err := s.Authenticate(context.Background(), live, "10.3.0.1"); err != nil {
			t.Errorf("live token: %v", err)
		}
	})
}

func TestWindowsLimitAndForget(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newWindow(2, time.Minute, func() time.Time { return now })
	if w.hit("a") != 0 || w.hit("a") != 0 {
		t.Fatal("first two hits refused")
	}
	if wait := w.hit("a"); wait <= 0 || wait > time.Minute {
		t.Errorf("third hit: wait %v", wait)
	}
	if w.hit("b") != 0 {
		t.Error("another key is limited too")
	}
	now = now.Add(time.Minute)
	if w.hit("a") != 0 {
		t.Error("a new window still refuses")
	}
	w.add("x")
	w.add("x")
	if w.blocked("x") == 0 || w.blocked("y") != 0 {
		t.Error("blocked after two failures, others not")
	}
}

func TestListAndTouch(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db, nil)
		for _, user := range []int64{1, 1, 2} {
			if _, _, err := s.Create(context.Background(), Creator{ID: user, Holds: holdsAll},
				Input{Name: "t", Permissions: []string{"device.view"}, ExpiresAt: time.Now().Add(time.Hour)}, known); err != nil {
				t.Fatal(err)
			}
		}
		mine, err := s.List(context.Background(), 1)
		if err != nil || len(mine) != 2 {
			t.Fatalf("List(1) = %d, %v", len(mine), err)
		}
		all, _ := s.List(context.Background(), 0)
		if len(all) != 3 {
			t.Errorf("List(0) = %d", len(all))
		}
		s.Touch(context.Background(), mine[0], "192.0.2.4")
		got, _ := s.Get(context.Background(), mine[0].ID)
		if got.LastUsedIP != "192.0.2.4" || got.LastUsedAt.IsZero() {
			t.Errorf("after Touch: %+v", got)
		}
	})
}
