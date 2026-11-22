// Package addressparser reads the address list of a mail header field
// ("Name <user@example.com>, other@example.com, Team: a@example.com;") the
// way nodemailer 10.4.0 reads it, with the same result for every input.
//
// The parser is lenient: it never fails, and malformed input comes back as
// entries with an empty name or an empty address. Its running time grows
// linearly with the length of the input.
//
// Input is read as UTF-8. Every byte that is no valid UTF-8 reads as U+FFFD,
// and the result is always valid UTF-8.
package addressparser

import "strings"

// Address is one entry of a parsed address list: a mailbox (Name, Address),
// or a group (Name, Group) when IsGroup is set.
type Address struct {
	Name    string
	Address string
	// IsGroup marks a group, also an empty one. A group has no Address.
	IsGroup bool
	// Group holds the members of a group, which are always mailboxes. It is
	// empty and not nil for a group without members, and nil for a mailbox.
	Group []Address
}

// Parse reads an address list. The entries come in the order of the input.
// The result is never nil.
//
// A ',' or a ';' ends an entry. "Name: a, b;" is a group; a group inside a
// group loses its name and gives its members to the outer one. Control
// characters are left out of names and addresses, and a line feed reads as a
// space, so no value in the result holds a line break.
func Parse(str string) []Address {
	tokens := tokenize([]rune(str))
	list := make([]Address, 0, 1)
	var group *Address
	var members []Address

	// Every entry is handed on as the range of tokens it covers, as soon as
	// the separator after it is seen.
	from := 0
	count := len(tokens.tokens)
	for i := 0; i <= count; i++ {
		var operator rune
		if i < count {
			operator = tokens.operatorAt(i)
			if operator != ',' && operator != ';' && operator != ':' {
				continue
			}
		}
		if operator == ':' {
			if group == nil {
				group = &Address{Name: groupName(tokens, from, i), IsGroup: true}
				members = nil
			}
			from = i + 1
			continue
		}
		if i > from {
			address := handleAddress(tokens, from, i)
			if group != nil {
				members = append(members, address)
			} else {
				list = append(list, address)
			}
		}
		from = i + 1
		if group != nil && (operator == ';' || i == count) {
			group.Group = mergeNameFragments(members)
			list = append(list, *group)
			group = nil
		}
	}
	return mergeNameFragments(list)
}

// Flatten returns the mailboxes of a parsed list in order, with every group
// replaced by its members. The result is never nil.
func Flatten(list []Address) []Address {
	flat := make([]Address, 0, len(list))
	return appendFlat(flat, list)
}

// appendFlat appends the mailboxes of list to flat, walking into groups.
func appendFlat(flat, list []Address) []Address {
	for _, entry := range list {
		if entry.IsGroup {
			flat = appendFlat(flat, entry.Group)
			continue
		}
		flat = append(flat, entry)
	}
	return flat
}

// mergeNameFragments folds the fragments an unquoted display name with a
// comma is split into back together. "Joe Foo, PhD <joe@example.com>" comes
// out of the walk as a name-only entry "Joe Foo" and the mailbox "PhD"
// <joe@example.com>. Every name-only mailbox in front of a mailbox that has
// both a name and an address joins the name of that mailbox, with ", " in
// between. Groups take no part in this.
func mergeNameFragments(list []Address) []Address {
	merged := make([]Address, 0, len(list))
	// Names of the fragments that go in front of the name of the last entry
	// of merged, the nearest one first. They are joined once the run of
	// fragments ends, so a long run costs its length and not its square.
	var fragments []string
	fold := func() {
		if len(fragments) == 0 {
			return
		}
		next := &merged[len(merged)-1]
		var name strings.Builder
		for i := len(fragments) - 1; i >= 0; i-- {
			name.WriteString(fragments[i])
			name.WriteString(", ")
		}
		name.WriteString(next.Name)
		next.Name = name.String()
		fragments = fragments[:0]
	}
	for i := len(list) - 1; i >= 0; i-- {
		current := list[i]
		if len(merged) > 0 {
			next := merged[len(merged)-1]
			if !current.IsGroup && current.Address == "" && current.Name != "" && !next.IsGroup && next.Address != "" && next.Name != "" {
				fragments = append(fragments, current.Name)
				continue
			}
		}
		fold()
		merged = append(merged, current)
	}
	fold()
	for i, j := 0, len(merged)-1; i < j; i, j = i+1, j-1 {
		merged[i], merged[j] = merged[j], merged[i]
	}
	return merged
}
