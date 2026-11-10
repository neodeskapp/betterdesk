// Package limits holds the console's rate limiters: who may ask how often.
//
// A Limiter counts requests per key in fixed windows and answers 429 with
// the RateLimit headers once a key is over its maximum (see Limiter for
// the exact rules). Counts live in memory: they are per process and gone
// after a restart.
//
// Set is the console's ten limiters, built from the environment by
// NewSet. Six of their uses are in front of every route (Set.AppLevel,
// step 11 of the pipeline, after the permission checks and before the
// client and device API routers); the others go on single routes with
// Limiter.Middleware. limits.md next to this file lists every limiter
// with its window, maximum, key, what it skips and where it applies.
//
// WANStack is everything a request on the client API port passes: its own
// headers, path list, limiters and body limits.
//
// # What the wiring passes in
//
//   - Options.ClientIP: the client's address under TRUST_PROXY, which is
//     httpx.TrustProxy.ClientIP (req.ip in the Node console). The
//     fallbacks for an empty address are built in: the X-Forwarded-For
//     header as sent, then "unknown". On a dual-stack socket the Node
//     console sees an IPv4 client as ::ffff:1.2.3.4 where Go sees
//     1.2.3.4. The address is only ever a key, so nothing a client sees
//     depends on it.
//   - Options.UserKey: the id of the signed-in user as text, "" when
//     nobody is signed in: session.userId, else session.user.id. The
//     session service sets both together, so it does not matter that the
//     Node console's layout limiter looks at session.userId alone.
//   - Options.Getenv: os.Getenv, or the console's settings lookup. The
//     variables are listed at NewSet.
//   - WANOptions.ClientIP: the same address function. The listener of the
//     client API port also gets the timeouts WANRequestTimeout and
//     WANKeepAliveTimeout on its http.Server.
package limits
