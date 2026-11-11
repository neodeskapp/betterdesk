package attestation

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// badgeVectors is what testdata/nodeoracle/badge.js recorded from the Node
// code: the stored test result and badge texts, the brand, the texts of
// the request language, and the "attestation" the sign-in page sent.
type badgeVectors struct {
	Translations map[string]map[string]string `json:"translations"`
	Vectors      []struct {
		Result  *string `json:"result"`
		Config  *string `json:"config"`
		Failing string  `json:"failing"`
		Brand   string  `json:"brand"`
		Texts   string  `json:"texts"`
		Out     string  `json:"out"`
	} `json:"vectors"`
}

func readGzipJSON(t *testing.T, name string, v any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(zr).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// mapSettings is a settings table in memory. A key in failing cannot be
// read; failWrite makes every write fail.
type mapSettings struct {
	rows      map[string]*string
	failing   map[string]bool
	failWrite error
	writes    [][2]string
}

func newMapSettings() *mapSettings {
	return &mapSettings{rows: map[string]*string{}, failing: map[string]bool{}}
}

func (m *mapSettings) Get(_ context.Context, key string) (string, bool, error) {
	if m.failing[key] {
		return "", false, errors.New("database is locked")
	}
	v := m.rows[key]
	if v == nil {
		return "", false, nil
	}
	return *v, true, nil
}

func (m *mapSettings) Set(_ context.Context, key, value string) error {
	if m.failWrite != nil {
		return m.failWrite
	}
	m.writes = append(m.writes, [2]string{key, value})
	m.rows[key] = &value
	return nil
}

func translator(texts map[string]string) Translate {
	return func(key string) string {
		if text, ok := texts[key]; ok {
			return text
		}
		return key
	}
}

// The sign-in badge is the one the Node code made from the same settings,
// for every recorded combination of test result, badge texts, brand and
// translations.
func TestSignInBadgeAsNodeMadeIt(t *testing.T) {
	var v badgeVectors
	readGzipJSON(t, "badge.json.gz", &v)
	if len(v.Vectors) < 1000 {
		t.Fatalf("only %d vectors", len(v.Vectors))
	}
	failed := 0
	for i, c := range v.Vectors {
		settings := newMapSettings()
		settings.rows[ResultKey] = c.Result
		settings.rows[BadgeConfigKey] = c.Config
		if c.Failing != "" {
			settings.failing[c.Failing] = true
		}
		got := "null"
		if badge := SignInBadge(context.Background(), settings, c.Brand, translator(v.Translations[c.Texts])); badge != nil {
			got = branding.Stringify(badge)
		}
		if got != c.Out {
			if failed++; failed <= 20 {
				t.Errorf("vector %d: result %s, config %s, brand %q, texts %s:\n  node: %s\n    go: %s", i, show(c.Result), show(c.Config), c.Brand, c.Texts, c.Out, got)
			}
		}
	}
	if failed > 20 {
		t.Errorf("%d more vectors differ", failed-20)
	}
	t.Logf("%d vectors", len(v.Vectors))
}

func show(s *string) string {
	if s == nil {
		return "(no row)"
	}
	b, _ := json.Marshal(*s)
	return string(b)
}

// The two settings come from the settings table, read at every call.
func TestSignInBadgeReadsTheSettings(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		settings := StoreSettings(db)
		same := func(key string) string { return key }
		if got := SignInBadge(ctx, settings, "BetterDesk", same); got != nil {
			t.Errorf("badge without settings = %s", branding.Stringify(got))
		}
		put := func(key, value string) {
			if _, err := db.Exec(ctx, "DELETE FROM settings WHERE key = ?", key); err != nil {
				t.Fatal(err)
			}
			if value == "" {
				return
			}
			if err := settings.Set(ctx, key, value); err != nil {
				t.Fatal(err)
			}
		}
		put(ResultKey, `{"tier":"platinum","maxConnections":120,"valid":true}`)
		put(BadgeConfigKey, `{"badgeTitle":"Measured","tierLabels":{"platinum":"Platin"}}`)
		want := `{"tier":"platinum","brand":"Contract","badgeTitle":"Measured","tierLabel":"Platin"}`
		if got := branding.Stringify(SignInBadge(ctx, settings, "Contract", same)); got != want {
			t.Errorf("badge = %s, want %s", got, want)
		}
		// Without badge texts the request language names the badge
		put(BadgeConfigKey, "")
		marked := func(key string) string { return key + "!" }
		want = `{"tier":"platinum","brand":"Contract","badgeTitle":"server_attestation.badge_tooltip!","tierLabel":"server_attestation.tier_platinum!"}`
		if got := branding.Stringify(SignInBadge(ctx, settings, "Contract", marked)); got != want {
			t.Errorf("badge without texts = %s, want %s", got, want)
		}
		put(ResultKey, "")
		if got := SignInBadge(ctx, settings, "Contract", same); got != nil {
			t.Errorf("badge without a result = %s", branding.Stringify(got))
		}
	})
}

// A NULL value is no row, and a row is written in place of the one there.
func TestStoreSettings(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		settings := StoreSettings(db)
		if _, found, err := settings.Get(ctx, ResultKey); found || err != nil {
			t.Fatalf("missing row: found %v, %v", found, err)
		}
		for _, value := range []string{`{"tier":"iron"}`, "second", ""} {
			if err := settings.Set(ctx, ResultKey, value); err != nil {
				t.Fatal(err)
			}
			got, found, err := settings.Get(ctx, ResultKey)
			if err != nil || !found || got != value {
				t.Fatalf("after writing %q: %q %v %v", value, got, found, err)
			}
		}
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM settings WHERE key = ?", ResultKey).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%d rows, %v", n, err)
		}
		if _, err := db.Exec(ctx, "UPDATE settings SET value = NULL WHERE key = ?", ResultKey); err == nil {
			if _, found, err := settings.Get(ctx, ResultKey); found || err != nil {
				t.Errorf("NULL value: found %v, %v", found, err)
			}
		}
	})
}

// The cut at a limit counts UTF-16 units and keeps half of a character
// outside the basic plane, which JSON then writes as an escape.
func TestNormalizeBadgeText(t *testing.T) {
	emoji := string(rune(0x1F600))
	cases := []struct {
		in    any
		limit int
		want  string
	}{
		{nil, 5, ""},
		{branding.Undefined, 5, ""},
		{"  a\x00b\x1fc\x7fd  ", 10, "abcd"},
		{"\x01 x \x02", 10, "x"},
		{"a\x7fb", 10, "ab"},
		{float64(12.5), 10, "12.5"},
		{true, 10, "true"},
		{[]any{"a", []any{"b", nil}}, 10, "a,b,"},
		{branding.NewObject(), 20, "[object Object]"},
		{"abc" + emoji, 4, "abc\xed\xa0\xbd"},
		{"abc" + emoji, 5, "abc" + emoji},
		{"abc" + emoji + "d", 3, "abc"},
		{strings.Repeat("é", 40), 32, strings.Repeat("é", 32)},
		{"x" + string(rune(0x3000)) + " ", 32, "x"},
	}
	for _, c := range cases {
		got, err := normalizeBadgeText(c.in, c.limit)
		if err != nil || got != c.want {
			t.Errorf("normalizeBadgeText(%#v, %d) = %q, %v; want %q", c.in, c.limit, got, err, c.want)
		}
	}
	if half, _ := normalizeBadgeText("abc"+emoji, 4); branding.Stringify(half) != `"abc\ud83d"` {
		t.Errorf("half a character is written as %s", branding.Stringify(half))
	}
	withToString := branding.NewObject()
	withToString.Set("toString", float64(1))
	if _, err := normalizeBadgeText(withToString, 5); err == nil || branding.Message(err) != "Cannot convert object to primitive value" {
		t.Errorf("an object with toString: %v", err)
	}
}

// The tiers by the number of connections.
func TestDetermineTier(t *testing.T) {
	cases := map[float64]any{
		0: nil, -3: nil, 0.9: nil, 1: "bronze", 25.9: "bronze", 26: "iron", 75: "iron", 76: "platinum",
		200: "platinum", 201: "titanium", 400: "titanium", 401: "obsidian", 506: "obsidian",
	}
	for n, want := range cases {
		if got := DetermineTier(n); got != want {
			t.Errorf("DetermineTier(%v) = %v, want %v", n, got, want)
		}
	}
}

// The public summary gives out the tier, the connections, the time and
// whether the result is valid, with the badge texts, and nothing else of
// the stored result.
func TestBuildPublicSummaryKeepsTheResultPrivate(t *testing.T) {
	stored := `{"valid":true,"mode":"loadtest","tier":"bronze","maxConnections":12,"testedAt":"2026-09-25T00:00:00.000Z",` +
		`"completedAt":"x","metricsHost":"secret-host","rampSteps":[{"cpu":99}],"baseline":{"secret":"nope","goStats":{"peers":3}},` +
		`"targets":{"signal":"ws://10.0.0.5:21118"},"probeLog":[{"url":"ws://10.0.0.5:21118"}],"errorRate":0.5,"finalMetrics":{"cpu":1}}`
	result, err := branding.ParseJSON([]byte(stored))
	if err != nil {
		t.Fatal(err)
	}
	config := BadgeConfig{BadgeTitle: "Acme Attestation"}
	config.TierLabels[0] = "Starter"
	got := branding.Stringify(publicBadge(result, config, nil, "Acme Desk"))
	want := `{"tier":"bronze","maxConnections":12,"testedAt":"2026-09-25T00:00:00.000Z","valid":true,"brand":"Acme Desk",` +
		`"badgeTitle":"Acme Attestation","tierLabel":"Starter","tierLabels":{"bronze":"Starter","iron":"IRON","platinum":"PLATINUM","titanium":"TITANIUM","obsidian":"OBSIDIAN"}}`
	if got != want {
		t.Errorf("public summary:\n got %s\nwant %s", got, want)
	}
	for _, secret := range []string{"secret", "10.0.0.5", "rampSteps", "baseline", "probeLog", "errorRate", "mode", "completedAt", "finalMetrics", "goStats"} {
		if strings.Contains(got, secret) {
			t.Errorf("the public summary gives out %q: %s", secret, got)
		}
	}
	empty := branding.Stringify(publicBadge(nil, config, nil, "Acme Desk"))
	if empty != `{"tier":null,"maxConnections":0,"testedAt":null,"valid":false,"brand":"Acme Desk","badgeTitle":"Acme Attestation",`+
		`"tierLabels":{"bronze":"Starter","iron":"IRON","platinum":"PLATINUM","titanium":"TITANIUM","obsidian":"OBSIDIAN"}}` {
		t.Errorf("summary without a result: %s", empty)
	}
	throwing, _ := branding.ParseJSON([]byte(`{"tier":{"toString":1},"maxConnections":3}`))
	if got := branding.Stringify(publicBadge(throwing, config, nil, "x")); got != `{"tier":null,"maxConnections":0,"testedAt":null,"valid":false}` {
		t.Errorf("summary of a tier that cannot be made text: %s", got)
	}
}
