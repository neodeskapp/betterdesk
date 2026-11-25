// Package session keeps the console's sign-in sessions: the session cookie,
// the panel_sessions table, the idle and absolute lifetimes and the limit
// of sessions per account. Cookie and rows have the format the Node console
// wrote, so both servers accept each other's sessions and a browser stays
// signed in when one replaces the other.
//
// # The cookie
//
// The cookie is called nd.sid, or neodesk.sid when the console is set up
// for HTTPS. Its value is
//
//	s:<id>.<mac>
//
// percent-encoded (":" as %3A, "+" as %2B, "/" as %2F). The id is 24 random
// bytes in URL-safe base64, 32 characters. The mac is the HMAC-SHA256 of
// the id in standard base64 without padding, keyed with the session secret
// taken as text. A value with another prefix or a wrong mac counts as no
// cookie: the request goes on signed out, and nothing clears the cookie.
//
// The attributes are Path=/, Expires, HttpOnly, Secure when the console
// serves HTTPS (not in an HTTPS setup that fell back to plain HTTP for
// want of a certificate), and SameSite=Lax. Every answer to a request that came with a live
// session sends the cookie again with a later Expires. A visitor without a
// session gets a cookie only once a handler has written to the session. A
// Secure cookie is never sent over a plain connection.
//
// # The row
//
// panel_sessions has one row per session id. session_json holds the
// session as JSON: a "cookie" object and the fields handlers set.
//
//	{"cookie":{"originalMaxAge":1800000,"expires":"2026-11-10T01:34:49.646Z",
//	           "secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},
//	 "userId":7,
//	 "user":{"id":7,"username":"alice","role":"operator","preferred_language":null},
//	 "createdAt":1794272689642,"lastActivityAt":1794272689642,
//	 "loginIp":"127.0.0.1","loginUserAgent":"...","clientFingerprint":"..."}
//
// originalMaxAge is the lifetime in milliseconds that each answer renews,
// expires the resulting time. A session without a "cookie" object cannot
// be read by either server. Fields keep their order and the exact text of
// their values through a load and a save, so a field one server does not
// know survives the other's writes. Field names the Node console cannot
// keep are those of its session object's own members: id, req, save,
// touch, reload, destroy, regenerate, resetMaxAge.
//
// The other columns repeat what the limits and the session list need:
// user_id, username, ip_address, user_agent, client_fingerprint,
// created_at, last_activity_at, expires_at, and is_active, ended_at and
// end_reason once the session ended. A session that ends keeps its row;
// sign-out and destroy replace session_json with {}.
//
// # Lifetimes
//
// A signed-in session ends when it was idle for the idle timeout or when
// the absolute lifetime since sign-in is over. The check runs twice. When
// the row is loaded, a row past a limit is marked ended (reason "idle" or
// "max_age") and the request goes on signed out. This is what normally
// happens, and the guards then answer as for any signed-out request. After
// that the same limits are checked against the times in the session
// itself; a session that fails there is ended and answered at once, with
// 401 and the code session_idle or session_expired, or a redirect to the
// sign-in page.
//
// A request with the header X-ND-Background: 1 (polls the person did not
// start) is checked but does not count as activity. Its answer still
// renews the cookie.
//
// # Several sessions per account
//
// An account may be signed in on several browsers at once (DECISIONS
// A239). The limit is SESSION_MAX_PER_USER when it is set, else 1 with
// the setting "One session per account" (server_config key
// console_single_session), else DefaultMaxPerUser. A sign-in beyond it
// ends the least recently used sessions, with the reason "replaced" at a
// limit of one and "limit" above. A session another browser of the
// account signed out ("signed_out"), an admin ended ("ended_by_admin"), or
// the limit or a change to the account ended ("revoked") answers its
// browser's next request once: 401 with a code for the API, the sign-in
// page with ?reason= for a page (Notice). The lists name a session by its
// Handle, never by its id, and OnEnd tells the sockets of a session that
// it ended.
//
// # Use
//
// Manager.Middleware gives every request a session; handlers get it with
// From. Sign-in calls Regenerate and Establish, sign-out End, Destroy and
// ClearCookies. Websocket upgrades call Manager.Load, which checks the
// cookie and the row and renews nothing.
package session
