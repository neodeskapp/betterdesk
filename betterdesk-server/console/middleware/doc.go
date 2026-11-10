// Package middleware holds the console's request plumbing: what every
// request passes before and around its route.
//
// The console's pipeline runs in a fixed order, and what the client sees
// depends on it. The pieces of this package belong at these steps:
//
//	 1  SecurityHeaders          every answer, also errors and files
//	 3  CORS                     /api/ only; answers OPTIONS /api/... itself
//	 4  Bodies                   JSON, then forms, 2 MB each
//	 9  StaticFolders            public files, /protos, /uploads, /wallpapers
//	15  NoStoreForNonFiles       after the client and device API routers
//	17  MarkAPICompression       after the CSRF gate
//	18  RequireJSON              first thing in front of the console's routes
//
// Steps 2, 5 to 8, 10 to 14 and 16 (error pages, cookies, session, CSRF,
// permissions, rate limits, client and device API, language) live in other
// packages. The rate limiters of step 11 are limits.Set.AppLevel.
//
// AttachmentBody is the parser of POST /api/tickets/{id}/attachments. It
// goes on that route after its sign-in and permission checks, so a request
// that may not attach anything is never read past its headers. RouteJSON
// is the same for any other limit.
//
// A parser error ends the request: the middleware calls OnError and the
// rest of the pipeline does not run. The handler behind OnError is the
// console's error handler (console/webui), which answers with the error's
// status and, in production, without its text:
//
//	400 {"success":false,"error":"Internal Server Error"}   malformed JSON
//	413 {"success":false,"error":"Internal Server Error"}   body over the limit
//
// # What handlers get
//
// Body(r) is the request body as an object whose members are still the
// bytes the client sent, with accessors named after what a JavaScript
// handler would have written (see JSONObject). Bind(r, &v) decodes it into
// a struct. ParseUpload reads a multipart upload with one file field.
//
// # What the wiring passes in
//
//   - SecurityOptions.HTTPS from HTTPS_ENABLED=true, SecurityOptions.HSTS
//     from HSTSEnabled(https, HSTS_ENABLED, ALLOW_SELF_SIGNED_CERTS=true).
//   - BodyOptions.OnError and StaticFoldersOptions.OnError: the console's
//     error handler.
//   - StaticFoldersOptions: the four folders and Production
//     (NODE_ENV=production). The uploads folder is DATA_DIR/uploads and
//     has to exist; the console creates it at start.
//   - MarkAPICompression: a function that is true for requests the
//     RustDesk client API routes and the device API router under /api/bd
//     answer. Those routers sit before step 17, so their answers were
//     never compressed. Admin routes that are also mounted under /api/bd
//     come later and are compressed.
package middleware

import "net/http"

// ErrorHandler answers a request that failed in the plumbing. status is
// the HTTP status the error asks for.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)

func (h ErrorHandler) handle(w http.ResponseWriter, r *http.Request, status int, err error) {
	if h != nil {
		h(w, r, status, err)
		return
	}
	http.Error(w, http.StatusText(status), status)
}
