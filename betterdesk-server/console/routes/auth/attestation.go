package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The capacity badge of the sign-in page comes from two settings the
// server attestation writes: the result of the last capacity test and the
// badge's own texts. Reading them is all the sign-in page needs of the
// attestation; the test itself is the business of the server attestation
// routes.
const (
	attestationResultKey = "server_attestation_result"
	attestationBadgeKey  = "server_attestation_badge_config"
	badgeTitleMax        = 96
	badgeLabelMax        = 32
)

// badgeTiers are the tiers of the capacity badge, lowest first.
var badgeTiers = []string{"bronze", "iron", "platinum", "titanium", "obsidian"}

// inheritedNames are the keys every JavaScript object has. Looking one of
// them up in the tier labels finds a function, or for __proto__ the
// object every object inherits from.
var inheritedNames = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "toLocaleString": true,
}

// attestationBadge returns the "attestation" of the sign-in page: the tier
// of the last capacity test that passed, with the brand, the badge's title
// and the tier's label, or nil (null) without such a test. A setting that
// cannot be read counts as missing. t translates a key of the request
// language and gives the key back when there is no text.
func attestationBadge(ctx context.Context, db *store.DB, brandName string, t func(string) string) *branding.Object {
	result, err := readSetting(ctx, db, attestationResultKey)
	if err != nil {
		result = nil
	}
	config, err := readSetting(ctx, db, attestationBadgeKey)
	return badgeFor(result, config, err == nil, brandName, t)
}

// readSetting is the value of a row of the settings table, nil when there
// is no row or its value is NULL.
func readSetting(ctx context.Context, db *store.DB, key string) (*string, error) {
	var value sql.NullString
	err := db.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !value.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value.String, nil
}

// badgeFor makes the badge from the stored test result and badge texts
// (nil for no row). configRead is false when the badge texts could not be
// read, which gives the default texts.
func badgeFor(result, config *string, configRead bool, brandName string, t func(string) string) *branding.Object {
	// The result is JSON; one that is empty or cannot be read is no result
	var last any
	if result != nil && *result != "" {
		if parsed, err := branding.ParseJSON([]byte(*result)); err == nil {
			last = parsed
		}
	}
	var tier any = last
	if branding.Truthy(last) {
		tier = branding.Field(last, "tier")
	}

	labels, title := defaultBadgeTexts()
	if configRead {
		if l, tt, err := normalizeBadgeConfig(config); err == nil {
			labels, title = l, tt
		}
	}
	// The texts are cleaned a second time when the badge is made. That is
	// not the same as once: a text cut at its limit can end in a space,
	// which the second trim removes.
	for id, label := range labels {
		labels[id] = normalizeBadgeText(label, badgeLabelMax)
	}
	title = normalizeBadgeText(title, badgeTitleMax)
	brand := normalizeBadgeText(brandName, badgeTitleMax)
	if brand == "" {
		brand = "BetterDesk"
	}
	translated := func(key, fallback string) string {
		if value := t(key); value != "" && value != key {
			return value
		}
		return fallback
	}
	tierLabels := map[string]string{}
	for _, id := range badgeTiers {
		tierLabels[id] = labels[id]
		if tierLabels[id] == "" {
			tierLabels[id] = translated("server_attestation.tier_"+id, strings.ToUpper(id))
		}
	}
	if title == "" {
		title = translated("server_attestation.badge_tooltip", brand+" Server Attestation")
	}

	// The label is looked up with the tier as a key, which is the tier's
	// text. A tier that cannot be made text was an error JavaScript threw
	// here, and the page then had no badge.
	var key string
	if branding.Truthy(tier) {
		text, err := branding.ToString(tier)
		if err != nil {
			return nil
		}
		key = text
	}
	if !branding.Truthy(last) || !branding.Truthy(tier) || branding.Field(last, "valid") == false {
		return nil
	}
	badge := branding.NewObject()
	badge.Set("tier", key)
	badge.Set("brand", brand)
	badge.Set("badgeTitle", title)
	switch label, own := tierLabels[key]; {
	case own:
		badge.Set("tierLabel", label)
	case key == "__proto__":
		badge.Set("tierLabel", branding.NewObject())
	case inheritedNames[key]:
		// a function, which JSON leaves out
	default:
		badge.Set("tierLabel", "")
	}
	return badge
}

// defaultBadgeTexts are the badge texts of an install that set none:
// every label and the title empty, so the translations apply.
func defaultBadgeTexts() (map[string]string, string) {
	labels := map[string]string{}
	for _, id := range badgeTiers {
		labels[id] = ""
	}
	return labels, ""
}

// normalizeBadgeConfig reads the stored badge texts: JSON with a title and
// a label per tier, each cleaned by normalizeBadgeText. Text that is no
// JSON object counts as no texts. It fails where JavaScript threw: on a
// value that cannot be made text.
func normalizeBadgeConfig(raw *string) (labels map[string]string, title string, err error) {
	var source any = branding.NewObject()
	if raw != nil {
		if parsed, perr := branding.ParseJSON([]byte(*raw)); perr == nil {
			source = parsed
		}
	}
	if _, ok := source.(*branding.Object); !ok {
		source = branding.NewObject()
	}
	configured := branding.Field(source, "tierLabels")
	if !branding.Truthy(configured) {
		configured = branding.NewObject()
	}
	switch configured.(type) {
	case *branding.Object, []any:
	default:
		configured = branding.NewObject()
	}
	labels = map[string]string{}
	for _, id := range badgeTiers {
		if labels[id], err = badgeText(branding.Field(configured, id), badgeLabelMax); err != nil {
			return nil, "", err
		}
	}
	if title, err = badgeText(branding.Field(source, "badgeTitle"), badgeTitleMax); err != nil {
		return nil, "", err
	}
	return labels, title, nil
}

// badgeText is normalizeBadgeText for any value: null and a missing value
// are empty, anything else is made text first.
func badgeText(v any, limit int) (string, error) {
	if v == nil || v == branding.Undefined {
		return "", nil
	}
	text, err := branding.ToString(v)
	if err != nil {
		return "", err
	}
	return normalizeBadgeText(text, limit), nil
}

// normalizeBadgeText removes control characters, trims and cuts a text to
// limit UTF-16 units.
func normalizeBadgeText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if r <= 0x1f || r == 0x7f {
			return -1
		}
		return r
	}, text)
	return goapipath.Slice(branding.Trim(text), limit)
}
