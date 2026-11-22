package db

// providerFieldLimit is the longest email address or display name stored
// from a directory or single sign-on, in characters: the length the web
// console's profile keeps.
const providerFieldLimit = 200

// providerProfile is what UpdateUserFromProvider writes: email and
// first_name (the whole display name, with last_name emptied, so the
// console shows the name as the provider has it), each only when the
// provider gave a value and the column is there.
func providerProfile(email, displayName string, has func(column string) bool) (sets []string, values []any) {
	cut := func(s string) string {
		if r := []rune(s); len(r) > providerFieldLimit {
			return string(r[:providerFieldLimit])
		}
		return s
	}
	if email != "" && has("email") {
		sets, values = append(sets, "email"), append(values, cut(email))
	}
	if displayName != "" && has("first_name") && has("last_name") {
		sets, values = append(sets, "first_name", "last_name"), append(values, cut(displayName), "")
	}
	return sets, values
}
