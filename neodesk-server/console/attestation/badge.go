package attestation

import (
	"context"
	"strings"

	"github.com/unitronix/betterdesk-server/console/branding"
)

// tierNames are the tiers of the capacity badge, lowest first. Each has a
// label of its own in the badge texts.
var tierNames = [5]string{"bronze", "iron", "platinum", "titanium", "obsidian"}

// BadgeConfig is the badge's own texts: a title and a label per tier.
// An empty text means the translation of the request language applies.
type BadgeConfig struct {
	BadgeTitle string
	// TierLabels are the labels of bronze, iron, platinum, titanium and
	// obsidian, in that order.
	TierLabels [5]string
}

// labels is the tier labels as the object {bronze, iron, ...}.
func labels(l [5]string) *branding.Object {
	o := branding.NewObject()
	for i, id := range tierNames {
		o.Set(id, l[i])
	}
	return o
}

// Object is the texts as the routes send and store them:
// {badgeTitle, tierLabels: {bronze, iron, platinum, titanium, obsidian}}.
func (c BadgeConfig) Object() *branding.Object {
	o := branding.NewObject()
	o.Set("badgeTitle", c.BadgeTitle)
	o.Set("tierLabels", labels(c.TierLabels))
	return o
}

// MarshalJSON writes the texts as Object.
func (c BadgeConfig) MarshalJSON() ([]byte, error) {
	return []byte(branding.Stringify(c.Object())), nil
}

// NormalizeBadgeConfig reads badge texts from what a request or the
// settings table gave (normalizeBadgeConfig): text is read as JSON first,
// and anything that is no JSON object counts as no texts. The title and
// each label are cleaned by the rules of normalizeBadgeText; other keys
// are dropped. It fails where JavaScript threw, on a value that cannot be
// made text (an object with its own toString key).
func NormalizeBadgeConfig(input any) (BadgeConfig, error) {
	source := input
	if text, ok := source.(string); ok {
		parsed, err := branding.ParseJSON([]byte(text))
		if err != nil {
			parsed = branding.NewObject()
		}
		source = parsed
	}
	if _, ok := source.(*branding.Object); !ok {
		source = branding.NewObject()
	}
	configured := branding.Field(source, "tierLabels")
	switch configured.(type) {
	case *branding.Object, []any:
	default:
		configured = branding.NewObject()
	}
	var c BadgeConfig
	for i, id := range tierNames {
		label, err := normalizeBadgeText(branding.Field(configured, id), BadgeLabelMax)
		if err != nil {
			return BadgeConfig{}, err
		}
		c.TierLabels[i] = label
	}
	title, err := normalizeBadgeText(branding.Field(source, "badgeTitle"), BadgeTitleMax)
	if err != nil {
		return BadgeConfig{}, err
	}
	c.BadgeTitle = title
	return c, nil
}

// Translate is req.t without placeholders: the text of a key in the
// request language, or the key itself when there is none.
type Translate func(key string) string

// Presentation is how the badge reads (resolveBadgePresentation): the
// brand, the title and the label of every tier with the translations
// filled in, and the label of one tier.
type Presentation struct {
	Brand      string
	BadgeTitle string
	TierLabels [5]string
	// TierLabel is the label of the tier asked for: a string, or nil
	// (null) for no tier or one without a label. A tier named after a key
	// every JavaScript object has finds what is there: {} for __proto__,
	// a function (Function, which JSON leaves out) for the others.
	TierLabel any
}

// Function is a JavaScript function where a value was expected. JSON
// leaves a key with such a value out.
type Function struct{}

// value is v as package branding writes it: a function as undefined.
func value(v any) any {
	if _, ok := v.(Function); ok {
		return branding.Undefined
	}
	return v
}

// Object is the presentation as the badge routes send it:
// {brand, badgeTitle, tierLabels, tierLabel}.
func (p *Presentation) Object() *branding.Object {
	o := branding.NewObject()
	o.Set("brand", p.Brand)
	o.Set("badgeTitle", p.BadgeTitle)
	o.Set("tierLabels", labels(p.TierLabels))
	o.Set("tierLabel", value(p.TierLabel))
	return o
}

// MarshalJSON writes the presentation as Object.
func (p *Presentation) MarshalJSON() ([]byte, error) {
	return []byte(branding.Stringify(p.Object())), nil
}

// prototypeKeys are the keys every JavaScript object has from
// Object.prototype, other than __proto__. Their values are functions.
var prototypeKeys = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "toLocaleString": true,
}

// ResolveBadgePresentation fills the translations into badge texts
// (resolveBadgePresentation). config is read by NormalizeBadgeConfig.
// tier is the JSON value of a stored result's tier, nil for none. t gives
// the texts of the request language; nil uses the English fallbacks.
// brandName is the console's name from the branding (a missing one,
// branding.Undefined, means BetterDesk). It fails where JavaScript threw:
// on texts or a tier that cannot be made text.
func ResolveBadgePresentation(config any, tier any, t Translate, brandName any) (*Presentation, error) {
	if c, ok := config.(BadgeConfig); ok {
		config = c.Object()
	}
	normalized, err := NormalizeBadgeConfig(config)
	if err != nil {
		return nil, err
	}
	brand, err := normalizeBadgeText(brandName, BadgeTitleMax)
	if err != nil {
		return nil, err
	}
	if brand == "" {
		brand = "BetterDesk"
	}
	p := &Presentation{Brand: brand}
	for i, id := range tierNames {
		p.TierLabels[i] = normalized.TierLabels[i]
		if p.TierLabels[i] == "" {
			p.TierLabels[i] = translated(t, "server_attestation.tier_"+id, strings.ToUpper(id))
		}
	}
	p.BadgeTitle = normalized.BadgeTitle
	if p.BadgeTitle == "" {
		p.BadgeTitle = translated(t, "server_attestation.badge_tooltip", brand+" Server Attestation")
	}
	if branding.Truthy(tier) {
		// tierLabels[tier]: the tier is made text to be a key
		key, err := branding.ToString(tier)
		if err != nil {
			return nil, err
		}
		p.TierLabel = p.labelOf(key)
	}
	return p, nil
}

// labelOf is tierLabels[key] || null.
func (p *Presentation) labelOf(key string) any {
	for i, id := range tierNames {
		if id == key {
			return p.TierLabels[i]
		}
	}
	switch {
	case key == "__proto__":
		return branding.NewObject()
	case prototypeKeys[key]:
		return Function{}
	}
	return nil
}

// translated is defaultTranslation: the text of key, or fallback when
// there is none (empty, or the key itself).
func translated(t Translate, key, fallback string) string {
	if t == nil {
		return fallback
	}
	if v := t(key); v != "" && v != key {
		return v
	}
	return fallback
}

// BuildPublicSummary is the public badge for a stored result and the
// presentation of its tier (buildPublicSummary). Without a result that has
// a tier it is {tier: null, maxConnections: 0, testedAt: null, valid:
// false} with the brand, title and labels; with one, the tier as stored,
// the number of connections, the time of the test, whether it is valid,
// and the tier's label. Nothing else of the result is given out.
// p may be nil, which leaves the texts out.
func BuildPublicSummary(result any, p *Presentation) *branding.Object {
	o := branding.NewObject()
	tier := branding.Field(result, "tier")
	if !branding.Truthy(result) || !branding.Truthy(tier) {
		o.Set("tier", nil)
		o.Set("maxConnections", float64(0))
		o.Set("testedAt", nil)
		o.Set("valid", false)
		if p != nil {
			o.Set("brand", p.Brand)
			o.Set("badgeTitle", p.BadgeTitle)
			o.Set("tierLabels", labels(p.TierLabels))
		}
		return o
	}
	o.Set("tier", tier)
	o.Set("maxConnections", or(branding.Field(result, "maxConnections"), float64(0)))
	o.Set("testedAt", or(or(branding.Field(result, "testedAt"), branding.Field(result, "completedAt")), nil))
	o.Set("valid", branding.Field(result, "valid") != false)
	if p != nil {
		o.Set("brand", p.Brand)
		o.Set("badgeTitle", p.BadgeTitle)
		o.Set("tierLabel", value(p.TierLabel))
		o.Set("tierLabels", labels(p.TierLabels))
	}
	return o
}

// or is a || b.
func or(a, b any) any {
	if branding.Truthy(a) {
		return a
	}
	return b
}

// tierOf is result && result.tier: the tier a presentation is made for.
func tierOf(result any) any {
	if !branding.Truthy(result) {
		return result
	}
	return branding.Field(result, "tier")
}

// unavailable is the public badge when it could not be made.
func unavailable() *branding.Object {
	o := branding.NewObject()
	o.Set("tier", nil)
	o.Set("maxConnections", float64(0))
	o.Set("testedAt", nil)
	o.Set("valid", false)
	return o
}

// publicBadge is what GET /api/public/server-attestation answers for a
// result and badge texts: the summary, or the badge without a tier when
// the presentation failed.
func publicBadge(result any, config BadgeConfig, t Translate, brandName any) *branding.Object {
	p, err := ResolveBadgePresentation(config, tierOf(result), t, brandName)
	if err != nil {
		return unavailable()
	}
	return BuildPublicSummary(result, p)
}

// SignInBadge is the capacity badge of the sign-in page ("attestation" of
// GET /api/auth/login-page): {tier, brand, badgeTitle, tierLabel} for the
// last capacity test that passed, nil (null) without one. It reads both
// settings at every call; a setting that cannot be read counts as
// missing. brandName is the console's name from the branding.
func SignInBadge(ctx context.Context, settings Settings, brandName string, t Translate) *branding.Object {
	config, _ := readBadgeConfig(ctx, settings)
	return signInBadge(LastResult(ctx, settings), config, brandName, t)
}

// signInBadge picks the sign-in page's fields from the public badge.
func signInBadge(result any, config BadgeConfig, brandName string, t Translate) *branding.Object {
	p, err := ResolveBadgePresentation(config, tierOf(result), t, brandName)
	if err != nil {
		return nil
	}
	summary := BuildPublicSummary(result, p)
	tier := branding.Field(summary, "tier")
	if !branding.Truthy(branding.Field(summary, "valid")) || !branding.Truthy(tier) {
		return nil
	}
	text, err := branding.ToString(tier)
	if err != nil {
		return nil
	}
	badge := branding.NewObject()
	badge.Set("tier", text)
	badge.Set("brand", or(branding.Field(summary, "brand"), ""))
	badge.Set("badgeTitle", or(branding.Field(summary, "badgeTitle"), ""))
	// The summary's tierLabel is the presentation's; a function there
	// is truthy and is left out of the JSON
	badge.Set("tierLabel", value(or(p.TierLabel, "")))
	return badge
}

// defaultBadgeConfig is the texts of an install that saved none.
func defaultBadgeConfig() BadgeConfig { return BadgeConfig{} }

// readBadgeConfig reads the stored badge texts (getBadgeConfig without
// its cache). A row that cannot be read, or texts that cannot be made
// text, give the default texts; read is false when the row could not be
// read.
func readBadgeConfig(ctx context.Context, settings Settings) (c BadgeConfig, read bool) {
	raw, found, err := settings.Get(ctx, BadgeConfigKey)
	if err != nil {
		return defaultBadgeConfig(), false
	}
	var input any
	if found {
		input = raw
	}
	c, err = NormalizeBadgeConfig(input)
	if err != nil {
		return defaultBadgeConfig(), true
	}
	return c, true
}

// LastResult is the stored result of the last capacity test
// (getLastResult): the JSON value of the setting, nil when there is none,
// it is empty, it is no JSON or it cannot be read.
func LastResult(ctx context.Context, settings Settings) any {
	raw, found, err := settings.Get(ctx, ResultKey)
	if err != nil || !found || raw == "" {
		return nil
	}
	parsed, err := branding.ParseJSON([]byte(raw))
	if err != nil {
		return nil
	}
	return parsed
}
