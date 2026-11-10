package branding

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var reThemeID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ListThemes lists the built-in theme presets: every *.json file of
// ThemesDir that is a theme (type "betterdesk-theme" with a branding
// part), as {id, name, description, colors}. Files that cannot be read as
// such are left out. The order is by file name.
func (s *Service) ListThemes() ([]any, error) {
	themes := []any{}
	if !exists(s.opts.ThemesDir) {
		return themes, nil
	}
	list, err := os.ReadDir(s.opts.ThemesDir)
	if err != nil {
		return nil, fsError(err, "scandir", s.opts.ThemesDir)
	}
	for _, entry := range list {
		file := decodeUTF8([]byte(entry.Name()))
		if !strings.HasSuffix(file, ".json") {
			continue
		}
		_ = try(func() {
			raw, err := os.ReadFile(filepath.Join(s.opts.ThemesDir, entry.Name()))
			if err != nil {
				return
			}
			data, err := parseJSON(decodeUTF8(raw))
			if err != nil {
				return
			}
			if property(data, "type") != "betterdesk-theme" {
				return
			}
			branding := property(data, "branding")
			if !Truthy(branding) {
				return
			}
			id := strings.Replace(file, ".json", "", 1)
			themes = append(themes, object(
				"id", id,
				"name", or(or(property(data, "displayName"), property(branding, "appName")), id),
				"description", or(property(branding, "appDescription"), ""),
				"colors", or(property(branding, "colors"), NewObject()),
			))
		})
	}
	return themes, nil
}

// ThemeResult says how ApplyTheme went.
type ThemeResult int

// The outcomes of ApplyTheme, with the answer the route gave for each.
const (
	// ThemeApplied: the preset is now the current settings.
	ThemeApplied ThemeResult = iota
	// ThemeInvalidID: the id has characters other than letters, digits,
	// "_" and "-" (400 "Invalid theme id").
	ThemeInvalidID
	// ThemeNotFound: there is no such preset (404 "Theme not found").
	ThemeNotFound
	// ThemeInvalidFormat: the file is JSON but no theme (400 "Invalid
	// theme format").
	ThemeInvalidFormat
)

// ApplyTheme makes a built-in preset the current settings. An error means
// the file could not be read or holds no JSON, or the settings could not
// be saved (500 "Failed to apply theme").
func (s *Service) ApplyTheme(ctx context.Context, id string) (result ThemeResult, err error) {
	defer catch(&err)
	if !reThemeID.MatchString(id) {
		return ThemeInvalidID, nil
	}
	file := resolveChildPath(s.opts.ThemesDir, id+".json")
	if !exists(file) {
		return ThemeNotFound, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return ThemeInvalidFormat, fsError(err, "open", file)
	}
	preset, err := parseJSON(decodeUTF8(raw))
	if err != nil {
		return ThemeInvalidFormat, err
	}
	ok, err := s.ImportPreset(ctx, preset)
	if err != nil || !ok {
		return ThemeInvalidFormat, err
	}
	return ThemeApplied, nil
}
