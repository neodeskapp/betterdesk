package branding

import (
	"context"
	"strconv"
)

// The refusals of the profile functions, each with the text the routes
// answer with.
var (
	ErrProfileNameTaken          = &Error{Name: "Error", Message: "Profile name already exists"}
	ErrInvalidProfileName        = &Error{Name: "Error", Message: "Invalid profile name"}
	ErrInvalidProfileDescription = &Error{Name: "Error", Message: "Invalid profile description"}
	ErrUnreadableProfile         = &Error{Name: "Error", Message: "The stored settings of the profile cannot be read"}
)

// ListProfiles returns the saved profiles without their settings, sorted
// by name.
func (s *Service) ListProfiles(ctx context.Context) ([]Profile, error) {
	profiles, err := listBrandingProfiles(ctx, s.opts.DB)
	return profiles, wrap("list the branding profiles", err)
}

// profileData is the text stored for a profile: a text as it is, anything
// else as JSON.
func profileData(data any) string {
	if text, ok := data.(string); ok {
		return text
	}
	return Stringify(data)
}

// CreateProfile saves a profile and returns its id. The name is trimmed
// and cut at 80 characters, the description at 200. brandingData is the
// theme to store; when it is not a theme (type "betterdesk-theme") the
// current settings are stored. An empty name fails with "Profile name is
// required", a name that exists with ErrProfileNameTaken.
func (s *Service) CreateProfile(ctx context.Context, name, description string, brandingData any) (id int64, err error) {
	defer catch(&err)
	trimmed := substring(trim(name), 80)
	if trimmed == "" {
		throw("Profile name is required")
	}
	preset := brandingData
	if !Truthy(brandingData) || property(brandingData, "type") != "betterdesk-theme" {
		preset = s.exportPreset()
	}
	id, err = createBrandingProfile(ctx, s.opts.DB, trimmed, substring(description, 200), profileData(preset))
	return id, wrap("create the branding profile", err)
}

// ProfileUpdate is what UpdateProfile changes. A field that is nil or
// Undefined keeps the stored value; Branding also keeps it when it is
// empty in JavaScript's eyes (false, 0, "").
type ProfileUpdate struct {
	Name        any
	Description any
	Branding    any
}

func isNullish(v any) bool { return v == nil || v == Undefined }

// UpdateProfile changes the name, the description or the settings of a
// profile. It fails with "Profile not found", with ErrUnreadableProfile
// when the stored settings are kept and are not JSON, with
// ErrInvalidProfileName or ErrInvalidProfileDescription for one that
// cannot be turned into text, and with ErrProfileNameTaken.
func (s *Service) UpdateProfile(ctx context.Context, id int64, updates ProfileUpdate) (err error) {
	defer catch(&err)
	profile, found, err := getBrandingProfile(ctx, s.opts.DB, id)
	if err != nil {
		return wrap("read the branding profile", err)
	}
	if !found {
		throw("Profile not found")
	}
	name := profile.Name
	if !isNullish(updates.Name) {
		text, err := textOf(updates.Name, ErrInvalidProfileName)
		if err != nil {
			return err
		}
		name = substring(trim(text), 80)
	}
	description := profile.Description.String
	if !isNullish(updates.Description) {
		text, err := textOf(updates.Description, ErrInvalidProfileDescription)
		if err != nil {
			return err
		}
		description = substring(text, 200)
	}
	var data any
	if Truthy(updates.Branding) {
		data = object("version", "1.0", "type", "betterdesk-theme", "branding", updates.Branding)
	} else {
		parsed, err := parseJSON(profile.Data)
		if err != nil {
			return ErrUnreadableProfile
		}
		data = parsed
	}
	return wrap("update the branding profile", updateBrandingProfile(ctx, s.opts.DB, id, name, description, profileData(data)))
}

// ApplyProfile makes a saved profile the current settings and marks it
// active. It answers false when there is no such profile or its settings
// are not a theme.
func (s *Service) ApplyProfile(ctx context.Context, id int64) (applied bool, err error) {
	defer catch(&err)
	profile, found, err := getBrandingProfile(ctx, s.opts.DB, id)
	if err != nil {
		return false, wrap("read the branding profile", err)
	}
	if !found {
		return false, nil
	}
	preset, err := parseJSON(profile.Data)
	if err != nil {
		return false, nil
	}
	ok, err := s.ImportPreset(ctx, preset)
	if err != nil || !ok {
		return false, err
	}
	if err := setActiveBrandingProfile(ctx, s.opts.DB, id); err != nil {
		return false, wrap("mark the branding profile active", err)
	}
	return true, nil
}

// DeleteProfile deletes a profile. It answers false when there is none
// with that id and fails with "Cannot delete the active profile" for the
// active one.
func (s *Service) DeleteProfile(ctx context.Context, id int64) (deleted bool, err error) {
	defer catch(&err)
	profile, found, err := getBrandingProfile(ctx, s.opts.DB, id)
	if err != nil {
		return false, wrap("read the branding profile", err)
	}
	if !found {
		return false, nil
	}
	if profile.IsActive.True() {
		throw("Cannot delete the active profile")
	}
	if err := deleteBrandingProfile(ctx, s.opts.DB, id); err != nil {
		return false, wrap("delete the branding profile", err)
	}
	return true, nil
}

// textOf is String(value), and refusal for a value that cannot be turned
// into text.
func textOf(value any, refusal *Error) (text string, err error) {
	if try(func() { text = toString(value) }) != nil {
		return "", refusal
	}
	return text, nil
}

// DuplicateProfile copies a profile and returns the id of the copy.
// newName is the name for the copy; empty or Undefined means "<name>
// (copy)". A name that is taken gets " 2", " 3" ... appended. It fails
// with "Profile not found", and with ErrInvalidProfileName when newName
// is neither text nor Undefined.
func (s *Service) DuplicateProfile(ctx context.Context, id int64, newName any) (newID int64, err error) {
	defer catch(&err)
	profile, found, err := getBrandingProfile(ctx, s.opts.DB, id)
	if err != nil {
		return 0, wrap("read the branding profile", err)
	}
	if !found {
		throw("Profile not found")
	}
	baseName := ""
	switch given := newName.(type) {
	case string:
		baseName = trim(given)
	case undefinedType:
	default:
		return 0, ErrInvalidProfileName
	}
	if baseName == "" {
		baseName = profile.Name + " (copy)"
	}
	name := substring(baseName, 80)
	existing, err := listBrandingProfiles(ctx, s.opts.DB)
	if err != nil {
		return 0, wrap("list the branding profiles", err)
	}
	taken := func(name string) bool {
		for _, p := range existing {
			if p.Name == name {
				return true
			}
		}
		return false
	}
	for suffix := 2; taken(name); suffix++ {
		name = substring(baseName, 70) + " " + strconv.Itoa(suffix)
	}
	data, err := parseJSON(profile.Data)
	if err != nil {
		data = s.exportPreset()
	}
	newID, err = createBrandingProfile(ctx, s.opts.DB, name, profile.Description.String, profileData(data))
	return newID, wrap("create the branding profile", err)
}
