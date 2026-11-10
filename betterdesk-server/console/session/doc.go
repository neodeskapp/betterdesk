// Package session keeps the console's sign-in sessions: the session cookie,
// the panel_sessions table, the idle and absolute lifetimes and the limit
// of sessions per account. Cookie and rows have the format the Node console
// wrote, so both servers accept each other's sessions and a browser stays
// signed in when one replaces the other.
//
// # The cookie
//
// The cookie is called bd.sid, or betterdesk.sid when the console is set up
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
// The attributes are Path=/, Expires, HttpOnly, Secure in an HTTPS setup,
// and SameSite=Lax. Every answer to a request that came with a live
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
// A request with the header X-BD-Background: 1 (polls the person did not
// start) is checked but does not count as activity. Its answer still
// renews the cookie.
//
// # Use
//
// Manager.Middleware gives every request a session; handlers get it with
// From. Sign-in calls Regenerate and Establish, sign-out End, Destroy and
// ClearCookies. Websocket upgrades call Manager.Load, which checks the
// cookie and the row and renews nothing.
package session
