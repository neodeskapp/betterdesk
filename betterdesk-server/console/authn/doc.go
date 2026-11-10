// Package authn is the console's sign-in service: passwords, the second
// factor, lockouts, the first account, access tokens and device tokens.
// It works on the tables and files existing installs have, in the formats
// they have, so every account keeps signing in.
//
// The package does no HTTP. A route takes what it needs from the request
// (user name, password, address) and calls the service; routes.md lists
// the calls of every route in order.
//
// # Passwords
//
// New hashes are bcrypt with cost 12 ("$2b$12$..."). A stored hash can
// also be an older bcrypt form ("$2a$", "$2$") or one of the signal
// server's PBKDF2 forms; a PBKDF2 hash is replaced by a bcrypt hash at the
// next sign-in with the right password. bcrypt.go says exactly which
// stored texts verify.
//
// # Sign-in
//
// Authenticate checks a user name and password. Directory (LDAP) accounts
// are checked by the signal server, through goapi. Authenticate neither
// checks nor records lockouts: a route calls CheckBruteForce before and
// RecordAttempt after, as routes.md says for each route.
//
// The second factor is a time-based code (totp.go) or one of 8 recovery
// codes, which are stored as they are and used once.
//
// # Tokens
//
// The RustDesk client API signs in with access tokens
// (GenerateAccessToken, ValidateAccessToken). Devices prove themselves
// with an access token bound to their id (RequireDeviceToken) or, on the
// device sockets, with an enrollment token of the signal server
// (VerifyDeviceWSAuth).
//
// # The first account
//
// EnsureDefaultAdmin creates the admin on an empty database and writes its
// password to .admin_credentials when it had to make one up.
package authn
