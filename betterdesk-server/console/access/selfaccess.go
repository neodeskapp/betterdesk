package access

import (
	"context"
	"strconv"
)

// KeepsUsersWrite reports whether an account still has write access to
// the Users section, where panel access is saved, once the section rows of
// a subject are replaced by sections: the account's own rows, or those of
// a user group it is a member of. The rows of other subjects do not change
// the account's access. A super admin role always keeps it, because no
// section row applies to it (ResolveForUser).
func (s *Service) KeepsUsersWrite(ctx context.Context, user PolicyUser, subjectType, subjectID string, sections any) (bool, error) {
	if IsSuperAdminRole(user.Role) {
		return true, nil
	}
	ceiling, err := s.roleCeiling(ctx, user.Role)
	if err != nil {
		return false, err
	}
	replaced := NormalizeSectionPolicies(sections)
	own := strconv.FormatInt(user.ID, 10)
	var rows userPolicyRows
	if subjectType == SubjectUser && subjectID == own {
		rows.sections = replaced
	} else if rows.sections, err = s.data.PanelAccessPolicies(ctx, SubjectUser, own); err != nil {
		return false, err
	}
	if rows.groups, err = s.data.UserGroupsForUser(ctx, user.ID); err != nil {
		return false, err
	}
	for _, group := range rows.groups {
		if subjectType == SubjectGroup && group.GUID == subjectID {
			rows.groupSections = append(rows.groupSections, replaced...)
			continue
		}
		groupRows, err := s.data.PanelAccessPolicies(ctx, SubjectGroup, group.GUID)
		if err != nil {
			return false, err
		}
		rows.groupSections = append(rows.groupSections, groupRows...)
	}
	users, _ := mergeUserPolicy(own, ceiling, rows).Sections.Get("users")
	return users.Access == LevelWrite, nil
}
