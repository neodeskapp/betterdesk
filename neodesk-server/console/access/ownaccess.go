package access

import (
	"context"
	"strconv"
)

// WidensOwn reports whether replacing the stored rows of a subject (the
// account itself, or one of its user groups) with the submitted sections
// and features would give the account more than it has now: a higher
// level in any section, or a client feature that is off now and would be
// on. The submitted rows are read and capped as SavePolicy stores them.
// An account that may not give its own role saves only what keeps or
// lowers its own access (L390).
func (s *Service) WidensOwn(ctx context.Context, user PolicyUser, subjectType, subjectID string, sections, features any) (bool, error) {
	if IsSuperAdminRole(user.Role) {
		return false, nil
	}
	now, err := s.ResolveForUser(ctx, user)
	if err != nil {
		return false, err
	}
	ceiling, err := s.roleCeiling(ctx, user.Role)
	if err != nil {
		return false, err
	}
	proposedSections := NormalizeSectionPolicies(sections)
	proposedFeatures := currentFeatureRows(NormalizeFeaturePolicies(features))
	if subjectType == SubjectUser {
		for i, row := range proposedSections {
			proposedSections[i].AccessLevel = string(clampToCeiling(row.AccessLevel, rowCeiling(ceiling, row.ResourceKey)))
		}
	}
	userID := strconv.FormatInt(user.ID, 10)
	var rows userPolicyRows
	if subjectType == SubjectUser && subjectID == userID {
		rows.sections, rows.features = proposedSections, proposedFeatures
	} else {
		if rows.sections, err = s.data.PanelAccessPolicies(ctx, SubjectUser, userID); err != nil {
			return false, err
		}
		if rows.features, err = s.ClientFeatureRows(ctx, SubjectUser, userID); err != nil {
			return false, err
		}
	}
	if rows.groups, err = s.data.UserGroupsForUser(ctx, user.ID); err != nil {
		return false, err
	}
	for _, group := range rows.groups {
		if subjectType == SubjectGroup && group.GUID == subjectID {
			rows.groupSections = append(rows.groupSections, proposedSections...)
			rows.groupFeatures = append(rows.groupFeatures, proposedFeatures...)
			continue
		}
		groupSections, err := s.data.PanelAccessPolicies(ctx, SubjectGroup, group.GUID)
		if err != nil {
			return false, err
		}
		groupFeatures, err := s.ClientFeatureRows(ctx, SubjectGroup, group.GUID)
		if err != nil {
			return false, err
		}
		rows.groupSections = append(rows.groupSections, groupSections...)
		rows.groupFeatures = append(rows.groupFeatures, groupFeatures...)
	}
	then := mergeUserPolicy(userID, ceiling, rows)
	for _, key := range now.Sections.Keys() {
		before, _ := now.Sections.Get(key)
		after, _ := then.Sections.Get(key)
		if rank(after.Access) > rank(before.Access) {
			return true, nil
		}
	}
	for _, key := range now.Features.Keys() {
		before, _ := now.Features.Get(key)
		after, _ := then.Features.Get(key)
		if after.Enabled && !before.Enabled {
			return true, nil
		}
	}
	return false, nil
}
