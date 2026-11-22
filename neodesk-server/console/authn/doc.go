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
// New hashes are the API server's ("pbkdf2-sha256$600000$...", made by
// auth.HashPasswordFrom, also by the reset-password command), so the API
// server's sign-in keeps them. A stored hash can also be the API server's
// older PBKDF2 form or a bcrypt hash ("$2b$", "$2a$", "$2$") that older
// consoles wrote; the console checks every form and replaces none.
// bcrypt.go says exactly which bcrypt texts verify.
//
// # Sign-in
//
// Authenticate checks a user name and password. Directory (LDAP) accounts
// are checked by the signal server, through goapi. Authenticate neither
// checks nor records lockouts: a route calls CheckBruteForce before and
// RecordAttempt after, as routes.md says for each route.
//
// The second factor is a time-based code (totp.go) or one of 8 recovery
// codes, which are stored as bcrypt hashes, as the API server stores its
// own, and used once; codes an older console stored as they were shown
// keep working until they are used.
//
// # Tokens
//
// The RustDesk client API signs in with access tokens
// (GenerateSignInToken, IssueAccessToken, ValidateAccessToken). Devices
// prove themselves with a device token issued for them in the signal
// server's device_tokens, on behalf of no account (by the API server at an
// enrollment or a pairing, or by the console with IssueDeviceToken), or one
// an older console issued in access_tokens, both checked by
// RequireDeviceToken; on the device sockets also with an enrollment token
// of the signal server (VerifyDeviceWSAuth). Device tokens do not expire
// by time. Accounts' tokens and device tokens never stand in for each
// other: an account's token proves no device, and a device token is no
// account's sign-in.
//
// # The first account
//
// EnsureDefaultAdmin creates the admin on an empty database and writes its
// password to .admin_credentials when it had to make one up.
package authn
