// Package listen holds the console's listeners as the old console's
// server.js set them up: the TLS of the console and of the client API
// port, the plain port that redirects to HTTPS, and the client API port
// (API_PORT, 21121) that faces the internet. Package console wires them
// (listeners.go); testdata/node_listeners.json is what the Node console's
// listeners answered over real sockets, and the console's tests replay it.
//
// # Certificates
//
// ReadFiles reads SSL_CERT_PATH, SSL_KEY_PATH and SSL_CA_PATH and logs why
// there is no certificate (a path unset, a file missing, a file that
// cannot be read); the console then runs on plain HTTP. Files.Certificate
// parses them; files that are there but hold no certificate and key stop
// the start. A certificate file with only the server's certificate gets
// its issuers from the bundle, as OpenSSL built the chain. ServerTLS is
// TLS 1.2 and 1.3 with the TLS 1.2 suites Node's default list accepted
// that Go implements, and no protocol negotiation (HTTP/1.1 only).
// TLSListener closes a connection that starts with plain HTTP without an
// answer.
//
// # The client API port
//
// ClientAPI is limits.WANStack (path list, rate limits, body limits,
// headers, the 10 second request timeout) in front of either the server's
// API (proxy mode, RUSTDESK_API_PROXY, the default) or the console's client
// API routes behind a strict 64 KB JSON parser (local mode). The two
// registration routes under /api/bd go to the Node console until they
// move. Its http.Server reads headers and body within 10 seconds, keeps an
// idle connection 5, and takes 16 KB of headers. PlainListener answers a
// connection that times out before its first request with "408 Request
// Timeout", as Node did.
//
// # The redirect
//
// Redirect answers every request on the plain port of an HTTPS setup with
// 307 to https://<host>:<HTTPS_PORT><target>.
package listen
