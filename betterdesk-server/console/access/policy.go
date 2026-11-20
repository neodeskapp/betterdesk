package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

// The two kinds of policy subject.
const (
	SubjectUser  = "user"
	SubjectGroup = "group"
)

// Where a resolved value came from.
const (
	SourceUser    = "user"
	SourceGroup   = "group"
	SourceRole    = "role"
	SourceUnset   = "unset"
	SourceDefault = "default"
)

// groupKeyPrefix starts the resource key of a row that sets a whole catalog
// group.
const groupKeyPrefix = "group:"

// StatusError is an error with the HTTP status the routes answer it with.
// The message is meant for the client.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

// ErrorStatus returns the status a route answers the error with: the
// status of a StatusError, 500 for everything else. For 500 the routes send
// their own general message instead of the error's text.
func ErrorStatus(err error) int {
	var se *StatusError
	if errors.As(err, &se) && se.Status != 0 {
		return se.Status
	}
	return 500
}

var policySubjectID = regexp.MustCompile(`^[A-Za-z0-9_.:@-]+$`)

// ValidateSubject checks a policy subject and returns its id, trimmed.
func ValidateSubject(subjectType, subjectID string) (string, error) {
	if subjectType != SubjectUser && subjectType != SubjectGroup {
		return "", &StatusError{Status: 400, Message: "Invalid policy subject type"}
	}
	value := jsTrim(subjectID)
	if value == "" || utf16Len(value) > 120 || !policySubjectID.MatchString(value) {
		return "", &StatusError{Status: 400, Message: "Invalid policy subject"}
	}
	return value, nil
}

// utf16Len is the length of s in UTF-16 code units, the unit the length
// limits are counted in.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// SectionRow is one stored section policy: the level of a subject for a
// section key or for "group:<catalog group>".
type SectionRow struct {
	ResourceKey string `json:"resource_key"`
	AccessLevel string `json:"access_level"`
}

// FeatureRow is one stored client feature policy.
type FeatureRow struct {
	FeatureKey string `json:"feature_key"`
	Enabled    bool   `json:"enabled"`
}

// NormalizeSectionPolicies reads the "sections" list of a submitted policy.
// Each row names its resource as resource_key or key and its level as
// access_level or access. Rows for unknown keys and repeats of a key are
// dropped; a level that is not one becomes none.
func NormalizeSectionPolicies(policies any) []SectionRow {
	allowed := make(map[string]struct{}, len(Catalog)+len(SectionGroups))
	for _, entry := range Catalog {
		allowed[entry.Key] = struct{}{}
	}
	for _, group := range SectionGroups {
		allowed[groupKeyPrefix+group.Key] = struct{}{}
	}
	rows, _ := policies.([]any)
	result := make([]SectionRow, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		key := jsTrim(jsStringOrEmpty(firstSet(row, "resource_key", "key")))
		if _, ok := allowed[key]; !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		level, given := row["access_level"]
		if !given || level == nil {
			level = row["access"]
		}
		result = append(result, SectionRow{ResourceKey: key, AccessLevel: string(NormalizeLevel(level))})
	}
	return result
}

// NormalizeFeaturePolicies reads the "features" list of a submitted policy.
// Each row names its feature as feature_key or key. A feature is on only
// for enabled true, 1 or "true".
func NormalizeFeaturePolicies(policies any) []FeatureRow {
	allowed := make(map[string]struct{}, len(ClientFeatures))
	for _, feature := range ClientFeatures {
		allowed[feature.Key] = struct{}{}
	}
	rows, _ := policies.([]any)
	result := make([]FeatureRow, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		key := jsTrim(jsStringOrEmpty(firstSet(row, "feature_key", "key")))
		if _, ok := allowed[key]; !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, FeatureRow{FeatureKey: key, Enabled: enabledFlag(row["enabled"])})
	}
	return result
}

// firstSet returns the first of the named values that counts as set.
func firstSet(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if v := row[key]; jsTruthy(v) {
			return v
		}
	}
	return nil
}

func enabledFlag(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	}
	f, ok := jsNumber(v)
	return ok && f == 1
}

// SectionAccess is an account's or group's resolved level in one section.
type SectionAccess struct {
	Access Level  `json:"access"`
	Source string `json:"source"`
	// RoleCeiling is the level the role alone gives. Group policies have
	// none and leave the key out.
	RoleCeiling Level `json:"role_ceiling,omitempty"`
}

// FeatureAccess is the resolved state of one client feature.
type FeatureAccess struct {
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
	HardLock bool   `json:"hard_lock"`
}

// GroupRef names a user group an account belongs to.
type GroupRef struct {
	GUID string `json:"guid"`
	Name string `json:"name"`
}

// Sections holds resolved sections by key and keeps the order they were
// first set in, which is the order of the JSON object.
type Sections struct {
	keys   []string
	values map[string]SectionAccess
}

// Set stores the entry for a key. A key set again keeps its place.
func (s *Sections) Set(key string, entry SectionAccess) {
	if s.values == nil {
		s.values = make(map[string]SectionAccess)
	}
	if _, ok := s.values[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.values[key] = entry
}

// Get returns the entry for a key.
func (s Sections) Get(key string) (SectionAccess, bool) {
	entry, ok := s.values[key]
	return entry, ok
}

// Keys returns the section keys in order.
func (s Sections) Keys() []string { return append([]string(nil), s.keys...) }

// MarshalJSON writes the sections as an object in their order.
func (s Sections) MarshalJSON() ([]byte, error) {
	return marshalOrdered(s.keys, func(key string) any { return s.values[key] })
}

// Features holds resolved client features by key, in catalog order.
type Features struct {
	keys   []string
	values map[string]FeatureAccess
}

// Set stores the entry for a key. A key set again keeps its place.
func (f *Features) Set(key string, entry FeatureAccess) {
	if f.values == nil {
		f.values = make(map[string]FeatureAccess)
	}
	if _, ok := f.values[key]; !ok {
		f.keys = append(f.keys, key)
	}
	f.values[key] = entry
}

// Get returns the entry for a key.
func (f Features) Get(key string) (FeatureAccess, bool) {
	entry, ok := f.values[key]
	return entry, ok
}

// Keys returns the feature keys in order.
func (f Features) Keys() []string { return append([]string(nil), f.keys...) }

// MarshalJSON writes the features as an object in their order.
func (f Features) MarshalJSON() ([]byte, error) {
	return marshalOrdered(f.keys, func(key string) any { return f.values[key] })
}

func marshalOrdered(keys []string, value func(string) any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		body, err := json.Marshal(value(key))
		if err != nil {
			return nil, err
		}
		buf.Write(body)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Policy is the effective panel access policy of an account, or the stored
// policy of a user group filled up with defaults.
type Policy struct {
	SubjectType string
	SubjectID   string
	Sections    Sections
	Features    Features
	// Groups lists the account's user groups. Group policies have none.
	Groups []GroupRef
}

// MarshalJSON writes the policy as the routes send it. An account's policy
// has "groups", a group's policy does not.
func (p Policy) MarshalJSON() ([]byte, error) {
	type base struct {
		SubjectType string   `json:"subject_type"`
		SubjectID   string   `json:"subject_id"`
		Sections    Sections `json:"sections"`
		Features    Features `json:"features"`
	}
	b := base{p.SubjectType, p.SubjectID, p.Sections, p.Features}
	if p.SubjectType != SubjectUser {
		return json.Marshal(b)
	}
	groups := p.Groups
	if groups == nil {
		groups = []GroupRef{}
	}
	return json.Marshal(struct {
		base
		Groups []GroupRef `json:"groups"`
	}{b, groups})
}

// AccessAllows reports whether the policy gives at least the required level
// in the section. Without a policy, without the section or without a level
// there, nothing is allowed.
func AccessAllows(policy *Policy, sectionKey string, required Level) bool {
	if policy == nil {
		return false
	}
	entry, ok := policy.Sections.Get(sectionKey)
	if !ok || entry.Access == "" {
		return false
	}
	return rank(NormalizeLevel(string(entry.Access))) >= rank(NormalizeLevel(string(required)))
}

// SavedPolicy is what a policy write stored: the normalised rows.
type SavedPolicy struct {
	Sections []SectionRow `json:"sections"`
	Features []FeatureRow `json:"features"`
}

// SavePolicy replaces every stored row of a subject with the normalised
// "sections" and "features" of a submitted policy (decoded JSON). Each
// section row is capped at the subject's role ceiling first (see
// subjectCeiling and rowCeiling); when the ceiling cannot be worked out
// nothing is written. A policy without section rows, an account's or a
// user group's, needs no ceiling: deleting a user group empties its policy
// that way. The two lists are written one after the other, each in its
// own transaction, and the second is written even when the first fails.
func (s *Service) SavePolicy(ctx context.Context, subjectType, subjectID string, sections, features any) (SavedPolicy, error) {
	id, err := ValidateSubject(subjectType, subjectID)
	if err != nil {
		return SavedPolicy{}, err
	}
	saved := SavedPolicy{
		Sections: NormalizeSectionPolicies(sections),
		Features: NormalizeFeaturePolicies(features),
	}
	if len(saved.Sections) > 0 {
		ceiling, err := s.subjectCeiling(ctx, subjectType, id)
		if err != nil {
			return SavedPolicy{}, err
		}
		for i, row := range saved.Sections {
			saved.Sections[i].AccessLevel = string(clampToCeiling(row.AccessLevel, rowCeiling(ceiling, row.ResourceKey)))
		}
	}
	sectionErr := s.setPanelAccessPolicies(ctx, subjectType, id, saved.Sections)
	featureErr := s.setClientFeaturePolicies(ctx, subjectType, id, saved.Features)
	if err := errors.Join(sectionErr, featureErr); err != nil {
		return SavedPolicy{}, err
	}
	return saved, nil
}
