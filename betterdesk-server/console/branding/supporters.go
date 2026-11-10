package branding

import "os"

// supporterLinks are the project links the Help panel shows when
// SupportersFile names no others.
var supporterLinks = [][2]string{
	{"repo", "https://github.com/UNITRONIX/BetterDesk"},
	{"issues", "https://github.com/UNITRONIX/BetterDesk/issues"},
	{"sponsors", "https://github.com/sponsors/UNITRONIX"},
	{"buyMeACoffee", "https://buymeacoffee.com/unitronix"},
	{"sponsorsMd", "https://github.com/UNITRONIX/BetterDesk/blob/main/SPONSORS.md"},
}

func defaultSupporterLinks() *Object {
	links := NewObject()
	for _, pair := range supporterLinks {
		links.Set(pair[0], pair[1])
	}
	return links
}

// LoadSupporters returns the project's supporters for the Help panel:
// {honorary, individuals, links}, read from SupportersFile once and kept.
// A file that cannot be read gives empty lists and the built-in links; so
// does every later call, until ResetSupportersCache. The object is shared
// and must not be changed.
func (s *Service) LoadSupporters() *Object {
	s.supportersMu.Lock()
	defer s.supportersMu.Unlock()
	if s.supporters != nil {
		return s.supporters
	}
	var loaded *Object
	err := try(func() {
		raw, err := os.ReadFile(s.opts.SupportersFile)
		if err != nil {
			panic(fsError(err, "open", s.opts.SupportersFile))
		}
		data, err := parseJSON(decodeUTF8(raw))
		if err != nil {
			panic(err)
		}
		list := func(key string) any {
			if items, ok := property(data, key).([]any); ok {
				return items
			}
			return []any{}
		}
		honorary, individuals := list("honorary"), list("individuals")
		links := defaultSupporterLinks()
		if own := property(data, "links"); Truthy(own) && isObjectLike(own) {
			spread(links, own)
		}
		loaded = object("honorary", honorary, "individuals", individuals, "links", links)
	})
	if err != nil {
		s.opts.Logf("[supportersService] Failed to load supporters.json: %v", err)
		loaded = object("honorary", []any{}, "individuals", []any{}, "links", defaultSupporterLinks())
	}
	s.supporters = loaded
	return loaded
}

// ResetSupportersCache makes the next LoadSupporters read the file again.
func (s *Service) ResetSupportersCache() {
	s.supportersMu.Lock()
	s.supporters = nil
	s.supportersMu.Unlock()
}
