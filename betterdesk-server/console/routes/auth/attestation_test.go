package auth

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func loadBadgeVectors(t *testing.T) badgeVectors {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "badge.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var v badgeVectors
	if err := json.NewDecoder(zr).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if len(v.Vectors) < 1000 {
		t.Fatalf("only %d vectors", len(v.Vectors))
	}
	return v
}

// The badge is the one the Node code made from the same settings, for
// every recorded combination of test result, badge texts, brand and
// translations.
func TestBadgeAsNodeMadeIt(t *testing.T) {
	v := loadBadgeVectors(t)
	failed := 0
	for i, c := range v.Vectors {
		texts := v.Translations[c.Texts]
		translate := func(key string) string {
			if text, ok := texts[key]; ok {
				return text
			}
			return key
		}
		result, config, configRead := c.Result, c.Config, true
		switch c.Failing {
		case attestationResultKey:
			result = nil
		case attestationBadgeKey:
			configRead = false
		}
		got := "null"
		if badge := badgeFor(result, config, configRead, c.Brand, translate); badge != nil {
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
	return strconvQuote(*s)
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The two settings come from the settings table, read at every request.
func TestBadgeReadsTheSettings(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		same := func(key string) string { return key }
		if got := attestationBadge(ctx, db, "BetterDesk", same); got != nil {
			t.Errorf("badge without settings = %s", branding.Stringify(got))
		}
		put := func(key, value string) {
			if _, err := db.Exec(ctx, "DELETE FROM settings WHERE key = ?", key); err != nil {
				t.Fatal(err)
			}
			if value == "" {
				return
			}
			if _, err := db.Exec(ctx, "INSERT INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
				t.Fatal(err)
			}
		}
		put(attestationResultKey, `{"tier":"platinum","maxConnections":120,"valid":true}`)
		put(attestationBadgeKey, `{"badgeTitle":"Measured","tierLabels":{"platinum":"Platin"}}`)
		want := `{"tier":"platinum","brand":"Contract","badgeTitle":"Measured","tierLabel":"Platin"}`
		if got := branding.Stringify(attestationBadge(ctx, db, "Contract", same)); got != want {
			t.Errorf("badge = %s, want %s", got, want)
		}
		// Without badge texts the request language names the badge
		put(attestationBadgeKey, "")
		marked := func(key string) string { return key + "!" }
		want = `{"tier":"platinum","brand":"Contract","badgeTitle":"server_attestation.badge_tooltip!","tierLabel":"server_attestation.tier_platinum!"}`
		if got := branding.Stringify(attestationBadge(ctx, db, "Contract", marked)); got != want {
			t.Errorf("badge without texts = %s, want %s", got, want)
		}
		put(attestationResultKey, "")
		if got := attestationBadge(ctx, db, "Contract", same); got != nil {
			t.Errorf("badge without a result = %s", branding.Stringify(got))
		}
	})
}
