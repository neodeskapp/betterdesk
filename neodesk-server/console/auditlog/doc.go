// Package auditlog writes the console's audit trail, the audit_log table,
// and holds the helpers that keep names and secrets out of it and out of
// log lines.
//
// # Writing an entry
//
// A route that called db.logAction(userId, action, details, ip) in the Node
// console calls Write or WriteEntry. The table shows what the database
// holds for each kind of argument the Node routes passed; testdata/rows.json
// has every line of it as Node left it on SQLite and on PostgreSQL.
//
//	userId    a number              that number
//	          null, undefined       NULL (req.session?.userId without a session)
//	          0                     0 on SQLite. PostgreSQL refuses the row:
//	                                user_id points at users(id). It refuses
//	                                the id of a deleted account the same way.
//	details   a string              the text after RedactDetails, "" stays ""
//	          null, undefined       NULL
//	          anything else         the text JavaScript makes of it: 42 is
//	                                "42", false "false", an object
//	                                "[object Object]", a list its items
//	                                joined with commas
//	ip        a string              that text, also "" and "unknown"
//	          null, undefined       NULL (req.ip || null with no address, or
//	                                the argument left out)
//	action    a string              that text; it is never redacted
//
// Write is for the common call, a user id from the session with a text and
// an address: Write(ctx, userID, action, details, ip) stores NULL for user
// 0 and the two texts as given. WriteEntry is for the rest: a nil Details
// or IP for the calls that passed null or left the argument out, and a
// pointer to 0 for the calls that passed "id || 0" and so stored 0. A value
// that was not a string in Node is passed as the text from the table.
//
// The database sets id and created_at: the text "YYYY-MM-DD HH:MM:SS" in
// UTC on SQLite, a timestamptz on PostgreSQL.
//
// PostgreSQL refuses a text with a NUL character in it; SQLite stores it.
// Bytes that are not UTF-8 are stored as U+FFFD on both, which is what a
// JavaScript string holds in their place.
//
// A failed write is an error for the caller. Whether it fails the request
// is the route's choice: most Node routes ignore it, some answer 500.
//
// # Redaction
//
// RedactDetails, RedactUsername, SanitizeLogValue, RedactURL and
// SensitiveDetailKey answer what the functions of the Node console's
// lib/logRedact.js answer for a string. testdata/vectors.json holds
// Node's answers for a few thousand inputs and the tests replay all of
// them; RedactURL documents the one kind of URL where the answers differ.
package auditlog
