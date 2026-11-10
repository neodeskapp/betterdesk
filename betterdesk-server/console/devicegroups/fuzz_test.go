package devicegroups

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

func units(s string) int { return len(utf16.Encode([]rune(s))) }

// cleanTags fails unless tags are what NormalizeTags promises: no empty
// tag, no repeat, no control character and at most 50 characters each. A
// tag may end in white space: it is trimmed before it is cut (BUGS DGR6).
func cleanTags(t *testing.T, what string, tags []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, tag := range tags {
		if tag == "" || seen[tag] || units(tag) > MaxTagLength || strings.IndexFunc(tag, removedControl) >= 0 {
			t.Fatalf("%s: tag %q in %q", what, tag, tags)
		}
		seen[tag] = true
	}
}

// removedControl reports the control characters SanitizeTag takes out:
// all below U+0020 but tab, line feed and carriage return.
func removedControl(r rune) bool {
	return r < 0x20 && r != 0x09 && r != 0x0a && r != 0x0d
}

func tagStrings(t *testing.T, what string, v any) []string {
	t.Helper()
	items, ok := v.([]any)
	if !ok {
		t.Fatalf("%s: tags are %T", what, v)
	}
	out := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("%s: a tag is %T", what, item)
		}
		out[i] = s
	}
	return out
}

// FuzzAddressBook reads arbitrary text as address book data and as a
// device list. Nothing may panic; a merged book is JSON with a list of
// peers that are objects with an id, none of them a banned device when
// only known peers are merged, every
// tag list is clean, and every tag of a peer is in the book's own list.
func FuzzAddressBook(f *testing.F) {
	for _, seed := range [][2]string{
		{`{"peers":[{"id":"dev-1","tags":["lab"," x "]},{"id":"typed"}],"tags":"a,b"}`, `[{"id":"dev-1","tags":"lab,office","banned":false},{"id":"dev-2","hostname":"h"}]`},
		{`{"peers":"x","tags":[1,null,{"a":2}]}`, `[{"id":"dev-1","banned":1},null,7,"x",[]]`},
		{`[1,2]`, `[]`}, {``, `null`}, {`not json`, `[{"id":5,"soft_deleted":true}]`}, {`{"peers":[null,[],{"id":""},{"id":" dev-1 "}]}`, `[{"id":"dev-1","banned":true}]`},
		{`{"tags":"[broken","peers":[{"id":1,"tags":"[\"a\",\"a\"]"}]}`, `[{"id":1,"tags":["` + "\x01" + `c"]}]`},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, data, deviceText string) {
		var devices []any
		if v, err := goapi.Parse([]byte(deviceText)); err == nil {
			devices, _ = v.([]any)
		}
		banned := map[string]bool{}
		for _, d := range devices {
			if device := asObject(d); device != nil && (truthy(device.Get("banned")) || truthy(device.Get("soft_deleted"))) {
				banned[idOf(device)] = true
			}
		}
		for _, knownOnly := range []bool{false, true} {
			out := MergeAddressBookData(data, MergeOptions{Devices: devices, KnownOnly: knownOnly})
			v, err := goapi.Parse([]byte(out))
			book, ok := v.(*goapi.Object)
			if err != nil || !ok {
				t.Fatalf("the merged book is no JSON object: %v: %s", err, out)
			}
			global := tagStrings(t, "the book", book.Get("tags"))
			cleanTags(t, "the book", global)
			inBook := map[string]bool{}
			for _, tag := range global {
				inBook[tag] = true
			}
			peers, ok := book.Get("peers").([]any)
			if !ok {
				t.Fatalf("peers are %T", book.Get("peers"))
			}
			for _, p := range peers {
				peer := asObject(p)
				id := idOf(peer)
				// With every device merged in, a banned device comes back as
				// a new peer (BUGS DGR1); only a merge of known peers keeps
				// it out.
				if id == "" || knownOnly && banned[id] {
					t.Fatalf("peer %q is in the merged book: %s", id, out)
				}
				tags := tagStrings(t, "peer "+id, peer.Get("tags"))
				cleanTags(t, "peer "+id, tags)
				for _, tag := range tags {
					if !inBook[tag] {
						t.Fatalf("tag %q of peer %q is not in the book's list: %s", tag, id, out)
					}
				}
			}
		}
		cleanTags(t, "NormalizeTags", NormalizeTags(data))
		for _, u := range CollectPeerTagUpdates(data) {
			if u.ID == "" {
				t.Fatal("an update without an id")
			}
			cleanTags(t, "an update", u.Tags)
		}
		_ = FilterAddressBookPeersByScope(data, func(string) bool { return false }, []any{"dev-1", 1.0, nil})
		cleanTags(t, "CollectVisibleTags", CollectVisibleTags(devices))
	})
}

// FuzzGroupPayload reads an arbitrary JSON body as a device group. The
// result keeps to its limits whatever the body holds.
func FuzzGroupPayload(f *testing.F) {
	f.Add(`{"name":" Lab ","source_type":"tag","tag_filter":"lab","allowed_users":"a,b","note":null}`)
	f.Add(`{"name":[1,[2,3]],"dynamic":true,"tag":{"a":1},"guid":1e21,"team_id":false,"allowed_groups":[{"guid":" g "},null,7]}`)
	f.Add(`{}`)
	f.Fuzz(func(t *testing.T, bodyText string) {
		var body map[string]any
		if json.Unmarshal([]byte(bodyText), &body) != nil {
			return
		}
		p := NormalizeGroupPayload(body)
		if p.SourceType != "tag" && p.SourceType != "manual" || p.SourceType == "manual" && p.TagFilter != "" {
			t.Fatalf("source %q with filter %q", p.SourceType, p.TagFilter)
		}
		if units(p.GUID) > 64 || units(p.Name) > 80 || units(p.TagFilter) > 50 || len(p.AllowedUsers) > 100 || len(p.AllowedGroups) > 100 {
			t.Fatalf("a limit does not hold: %+v", p)
		}
		if p.Note != nil && units(*p.Note) > 512 || p.TeamID != nil && units(*p.TeamID) > 64 {
			t.Fatalf("a limit does not hold: note or team id")
		}
		if _, has := body["note"]; has != (p.Note != nil) {
			t.Fatal("the note is set without the key, or the other way round")
		}
		create, update := p.CreateFields(), p.UpdateFields()
		if create.Note == nil || create.TeamID == nil || (update.Note == nil) != (p.Note == nil) {
			t.Fatal("create or update fields")
		}
	})
}
