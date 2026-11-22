package access

import (
	"context"
	"errors"
	"strconv"
)

// ClientFeaturesOff returns the keys of the client features the account
// may not use in remote sessions, in catalog order and hard locked ones
// included: the features ResolveForUser reports as off. They come from
// the account's own rows and those of its user groups; the role plays no
// part, so they apply to super admins too. The signal server asks this
// for the account behind every connection it passes on.
func (s *Service) ClientFeaturesOff(ctx context.Context, userID int64) ([]string, error) {
	if userID <= 0 {
		return nil, errors.New("client features: no account")
	}
	own, err := s.ClientFeatureRows(ctx, SubjectUser, strconv.FormatInt(userID, 10))
	if err != nil {
		return nil, err
	}
	groups, err := s.data.UserGroupsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var inherited []FeatureRow
	for _, group := range groups {
		rows, err := s.ClientFeatureRows(ctx, SubjectGroup, group.GUID)
		if err != nil {
			return nil, err
		}
		inherited = append(inherited, rows...)
	}
	features := mergeUserFeatures(own, inherited)
	off := []string{}
	for _, key := range features.Keys() {
		if feature, _ := features.Get(key); !feature.Enabled {
			off = append(off, key)
		}
	}
	return off, nil
}

// ClientFeatureRows returns a subject's stored client feature rows as the
// rows of the features there are (currentFeatureRows): what the policy
// editors show and what resolving a policy reads.
func (s *Service) ClientFeatureRows(ctx context.Context, subjectType, subjectID string) ([]FeatureRow, error) {
	rows, err := s.data.ClientFeaturePolicies(ctx, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	return currentFeatureRows(rows), nil
}
