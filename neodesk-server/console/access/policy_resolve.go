package access

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// PolicyUser is the account a policy is resolved for.
type PolicyUser struct {
	ID   int64
	Role string
}

// ceilingFor returns the level a role reaches in each section through its
// permissions alone.
func ceilingFor(permissions PermissionSet) map[string]Level {
	ceiling := make(map[string]Level, len(Catalog))
	for _, entry := range Catalog {
		ceiling[entry.Key] = LegacySectionCeiling(entry.Key, permissions.Has, false)
	}
	return ceiling
}

// superAdminCeiling is write in every section.
func superAdminCeiling() map[string]Level {
	ceiling := make(map[string]Level, len(Catalog))
	for _, entry := range Catalog {
		ceiling[entry.Key] = LevelWrite
	}
	return ceiling
}

// roleCeiling returns the level the role alone gives in each section: write
// everywhere for super admins, else what the role's effective permissions
// reach. An account without a role counts as a viewer.
func (s *Service) roleCeiling(ctx context.Context, role string) (map[string]Level, error) {
	if IsSuperAdminRole(role) {
		return superAdminCeiling(), nil
	}
	if role == "" {
		role = "viewer"
	}
	permissions, err := s.EffectivePermissions(ctx, role)
	if err != nil {
		return nil, err
	}
	return ceilingFor(permissions), nil
}

// subjectCeiling returns the ceiling an account's saved policy is capped
// at: its role's (an id that names no account counts as a viewer); a role
// the server does not know reaches none anywhere.
func (s *Service) subjectCeiling(ctx context.Context, subjectID string) (map[string]Level, error) {
	user, err := s.subjectUser(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	role := ""
	if user != nil {
		role = user.Role
	}
	ceiling, err := s.roleCeiling(ctx, role)
	var unknown *RoleError
	if errors.As(err, &unknown) && unknown.Status == http.StatusBadRequest {
		return ceilingFor(PermissionSet{}), nil
	}
	return ceiling, err
}

// rowCeiling is the ceiling a stored row is capped at: its section's, or
// for a "group:<key>" row the highest among the sections of that catalog
// group, so the row keeps what the role reaches in any of them. Resolving
// the policy caps each section at its own ceiling again.
func rowCeiling(ceiling map[string]Level, resourceKey string) Level {
	group, isGroup := strings.CutPrefix(resourceKey, groupKeyPrefix)
	if !isGroup {
		return ceiling[resourceKey]
	}
	highest := LevelNone
	for _, entry := range Catalog {
		if entry.Group == group && rank(ceiling[entry.Key]) > rank(highest) {
			highest = ceiling[entry.Key]
		}
	}
	return highest
}

// clampToCeiling returns the lower of a level and a ceiling, both read with
// NormalizeLevel.
func clampToCeiling(level, ceiling any) Level {
	l, c := NormalizeLevel(level), NormalizeLevel(ceiling)
	if rank(l) <= rank(c) {
		return l
	}
	return c
}

// highestGroupLevel returns the highest level the group rows give for a
// section key or for its catalog group, and whether any row applies.
func highestGroupLevel(rows []SectionRow, sectionKey, groupKey string) (Level, bool) {
	var selected Level
	found := false
	for _, row := range rows {
		if row.ResourceKey != sectionKey && row.ResourceKey != groupKeyPrefix+groupKey {
			continue
		}
		level := NormalizeLevel(row.AccessLevel)
		if !found || rank(level) > rank(selected) {
			selected, found = level, true
		}
	}
	return selected, found
}

// userPolicyRows is everything stored that decides an account's policy.
type userPolicyRows struct {
	sections []SectionRow
	features []FeatureRow
	// groups are the account's user groups; groupSections and
	// groupFeatures are the rows of all of them together.
	groups        []UserGroup
	groupSections []SectionRow
	groupFeatures []FeatureRow
}

// mergeUserPolicy applies the precedence rules of ResolveForUser to the
// stored rows and the role's ceiling.
func mergeUserPolicy(subjectID string, ceiling map[string]Level, rows userPolicyRows) *Policy {
	direct := make(map[string]string, len(rows.sections))
	for _, row := range rows.sections {
		direct[row.ResourceKey] = row.AccessLevel
	}
	policy := &Policy{SubjectType: SubjectUser, SubjectID: subjectID, Groups: make([]GroupRef, 0, len(rows.groups))}
	for _, entry := range Catalog {
		roleLevel := ceiling[entry.Key]
		if roleLevel == "" {
			roleLevel = LevelNone
		}
		own, hasOwn := direct[entry.Key]
		ownGroup, hasOwnGroup := direct[groupKeyPrefix+entry.Group]
		inherited, hasInherited := highestGroupLevel(rows.groupSections, entry.Key, entry.Group)
		resolved := SectionAccess{Access: roleLevel, Source: SourceRole, RoleCeiling: roleLevel}
		switch {
		case hasOwn:
			resolved.Access, resolved.Source = NormalizeLevel(own), SourceUser
		case hasOwnGroup:
			resolved.Access, resolved.Source = NormalizeLevel(ownGroup), SourceUser
		case hasInherited:
			resolved.Access, resolved.Source = inherited, SourceGroup
		}
		resolved.Access = clampToCeiling(resolved.Access, roleLevel)
		policy.Sections.Set(entry.Key, resolved)
	}
	policy.Features = mergeUserFeatures(rows.features, rows.groupFeatures)
	for _, group := range rows.groups {
		policy.Groups = append(policy.Groups, GroupRef{GUID: group.GUID, Name: group.Name})
	}
	return policy
}

// mergeUserFeatures resolves an account's client features from its own
// rows and the rows of all its groups together: the account's row, else
// on when any of its groups turns the feature on, else on. A hard locked
// feature is always off.
func mergeUserFeatures(own, inherited []FeatureRow) Features {
	direct := make(map[string]bool, len(own))
	for _, row := range own {
		direct[row.FeatureKey] = row.Enabled
	}
	fromGroups := make(map[string]bool, len(inherited))
	for _, row := range inherited {
		fromGroups[row.FeatureKey] = fromGroups[row.FeatureKey] || row.Enabled
	}
	var features Features
	for _, feature := range ClientFeatures {
		value, hasOwn := direct[feature.Key]
		group, hasGroup := fromGroups[feature.Key]
		resolved := FeatureAccess{Enabled: true, Source: SourceDefault, HardLock: feature.HardLock}
		switch {
		case hasOwn:
			resolved.Enabled, resolved.Source = value, SourceUser
		case hasGroup:
			resolved.Enabled, resolved.Source = group, SourceGroup
		}
		if feature.HardLock {
			resolved.Enabled = false
		}
		features.Set(feature.Key, resolved)
	}
	return features
}

// mergeGroupPolicy fills a group's stored rows up with defaults.
func mergeGroupPolicy(groupGUID string, sectionRows []SectionRow, featureRows []FeatureRow) *Policy {
	stored := make(map[string]string, len(sectionRows))
	for _, row := range sectionRows {
		stored[row.ResourceKey] = row.AccessLevel
	}
	storedFeatures := make(map[string]bool, len(featureRows))
	for _, row := range featureRows {
		storedFeatures[row.FeatureKey] = row.Enabled
	}
	policy := &Policy{SubjectType: SubjectGroup, SubjectID: groupGUID}
	for _, entry := range Catalog {
		resolved := SectionAccess{Access: LevelNone, Source: SourceUnset}
		if own, ok := stored[entry.Key]; ok {
			resolved = SectionAccess{Access: NormalizeLevel(own), Source: SourceGroup}
		} else if byGroup, ok := stored[groupKeyPrefix+entry.Group]; ok {
			resolved = SectionAccess{Access: NormalizeLevel(byGroup), Source: SourceGroup}
		}
		policy.Sections.Set(entry.Key, resolved)
	}
	for _, feature := range ClientFeatures {
		resolved := FeatureAccess{Enabled: true, Source: SourceDefault, HardLock: feature.HardLock}
		if enabled, ok := storedFeatures[feature.Key]; ok {
			resolved.Enabled, resolved.Source = enabled, SourceGroup
		}
		if feature.HardLock {
			resolved.Enabled = false
		}
		policy.Features.Set(feature.Key, resolved)
	}
	return policy
}

// ResolveForUser returns the effective policy of an account. For each
// section, the first of these that exists decides: the account's own row
// for the section, its row for the section's catalog group, the highest row
// among its user groups (for the section or its catalog group), the role's
// ceiling. The level is then capped at the role's ceiling: a row can lower
// what the role gives, never raise it. A super admin role
// (auth.IsSuperAdminRole) gets no section rows at all: it has write in
// every section, so it can always repair the access of others.
// Client features: the account's row, else on when any of its groups turns
// the feature on, else on; a hard locked feature is always off.
func (s *Service) ResolveForUser(ctx context.Context, user PolicyUser) (*Policy, error) {
	return s.resolveForUser(ctx, user, "")
}

// resolveForUser is ResolveForUser as if the account were not in the user
// group with the guid without ("" for none).
func (s *Service) resolveForUser(ctx context.Context, user PolicyUser, without string) (*Policy, error) {
	if user.ID == 0 {
		return nil, &StatusError{Status: 404, Message: "User not found"}
	}
	ceiling, err := s.roleCeiling(ctx, user.Role)
	if err != nil {
		return nil, err
	}
	subjectID := strconv.FormatInt(user.ID, 10)
	var rows userPolicyRows
	if rows.sections, err = s.data.PanelAccessPolicies(ctx, SubjectUser, subjectID); err != nil {
		return nil, err
	}
	if rows.features, err = s.ClientFeatureRows(ctx, SubjectUser, subjectID); err != nil {
		return nil, err
	}
	if rows.groups, err = s.data.UserGroupsForUser(ctx, user.ID); err != nil {
		return nil, err
	}
	if without != "" {
		kept := rows.groups[:0:0]
		for _, group := range rows.groups {
			if group.GUID != without {
				kept = append(kept, group)
			}
		}
		rows.groups = kept
	}
	for _, group := range rows.groups {
		sections, err := s.data.PanelAccessPolicies(ctx, SubjectGroup, group.GUID)
		if err != nil {
			return nil, err
		}
		features, err := s.ClientFeatureRows(ctx, SubjectGroup, group.GUID)
		if err != nil {
			return nil, err
		}
		rows.groupSections = append(rows.groupSections, sections...)
		rows.groupFeatures = append(rows.groupFeatures, features...)
	}
	if IsSuperAdminRole(user.Role) {
		rows.sections, rows.groupSections = nil, nil
	}
	return mergeUserPolicy(subjectID, ceiling, rows), nil
}

// ResolveForGroup returns a user group's stored policy with defaults for
// what it does not set: none for sections, on for client features.
func (s *Service) ResolveForGroup(ctx context.Context, groupGUID string) (*Policy, error) {
	if groupGUID == "" {
		return nil, &StatusError{Status: 404, Message: "Group not found"}
	}
	sectionRows, err := s.data.PanelAccessPolicies(ctx, SubjectGroup, groupGUID)
	if err != nil {
		return nil, err
	}
	featureRows, err := s.ClientFeatureRows(ctx, SubjectGroup, groupGUID)
	if err != nil {
		return nil, err
	}
	return mergeGroupPolicy(groupGUID, sectionRows, featureRows), nil
}

// KeepGroupLimits hands the limits a user group puts on its members to
// the members themselves, before the group goes: a section a member
// reaches at a lower level with the group than without it gets that level
// as the member's own row, and a client feature the group turns off for a
// member gets an own row that keeps it off. Deleting a group then lifts no
// limit: a limit that pointed at the group stays a limit (L391).
func (s *Service) KeepGroupLimits(ctx context.Context, groupGUID string) error {
	group, err := s.UserGroupByGUID(ctx, groupGUID)
	if err != nil || group == nil {
		return err
	}
	members, err := s.UserGroupMembers(ctx, group.ID)
	if err != nil {
		return err
	}
	for _, member := range members {
		if err := s.keepLimitsOf(ctx, member, groupGUID); err != nil {
			return err
		}
	}
	return nil
}

// keepLimitsOf is KeepGroupLimits for one member.
func (s *Service) keepLimitsOf(ctx context.Context, member PolicyUser, groupGUID string) error {
	with, err := s.resolveForUser(ctx, member, "")
	if err != nil {
		return err
	}
	without, err := s.resolveForUser(ctx, member, groupGUID)
	if err != nil {
		return err
	}
	subjectID := strconv.FormatInt(member.ID, 10)
	sections, err := s.data.PanelAccessPolicies(ctx, SubjectUser, subjectID)
	if err != nil {
		return err
	}
	features, err := s.ClientFeatureRows(ctx, SubjectUser, subjectID)
	if err != nil {
		return err
	}
	sectionsChanged, featuresChanged := false, false
	for _, key := range with.Sections.Keys() {
		now, _ := with.Sections.Get(key)
		then, _ := without.Sections.Get(key)
		if rank(then.Access) <= rank(now.Access) {
			continue
		}
		sections = setSectionRow(sections, key, string(now.Access))
		sectionsChanged = true
	}
	for _, key := range with.Features.Keys() {
		now, _ := with.Features.Get(key)
		then, _ := without.Features.Get(key)
		if now.Enabled || !then.Enabled {
			continue
		}
		features = setFeatureRow(features, key, false)
		featuresChanged = true
	}
	if sectionsChanged {
		if err := s.setPanelAccessPolicies(ctx, SubjectUser, subjectID, sections); err != nil {
			return err
		}
	}
	if featuresChanged {
		if err := s.setClientFeaturePolicies(ctx, SubjectUser, subjectID, features); err != nil {
			return err
		}
	}
	return nil
}

// setSectionRow sets the level of a section key in rows, adding the row
// when there is none.
func setSectionRow(rows []SectionRow, key, level string) []SectionRow {
	for i := range rows {
		if rows[i].ResourceKey == key {
			rows[i].AccessLevel = level
			return rows
		}
	}
	return append(rows, SectionRow{ResourceKey: key, AccessLevel: level})
}

// setFeatureRow sets a client feature in rows, adding the row when there
// is none.
func setFeatureRow(rows []FeatureRow, key string, enabled bool) []FeatureRow {
	for i := range rows {
		if rows[i].FeatureKey == key {
			rows[i].Enabled = enabled
			return rows
		}
	}
	return append(rows, FeatureRow{FeatureKey: key, Enabled: enabled})
}
