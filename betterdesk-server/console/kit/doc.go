// Package kit is what every console route group builds on: the route
// table, the guards, the request accessors and Deps, the bundle of
// services a group gets.
//
// # A route group
//
// A group is one package under console/routes, named after the Node route
// file it replaces, with a Register function. Register lists every route
// with its guards in the order of the Node file, so the two read alike.
// The group below is complete: copy it. The same code is compiled and run
// in example_test.go.
//
//	// Package notes holds the notes people leave on a device.
//	package notes
//
//	type handlers struct {
//		d   *kit.Deps
//		log *kit.Logger
//	}
//
//	// Register adds the group's routes.
//	func Register(d *kit.Deps) {
//		h := &handlers{d: d, log: d.Log.Child("NOTES")}
//		r := d.Router
//		r.Get("/api/notes/{device}", h.list, d.RequireAuth, d.RequirePermission("device.view"))
//		r.Post("/api/notes/{device}", h.add, d.Limits.Upload.Middleware, d.RequireAuth, d.RequirePermission("device.edit"))
//	}
//
//	// add answers POST /api/notes/{device} with the body {"text":"..."}.
//	func (h *handlers) add(w http.ResponseWriter, r *http.Request) {
//		device := r.PathValue("device")              // req.params.device, decoded
//		text := strings.TrimSpace(kit.Body(r).Text("text", "")) // req.body.text
//		if text == "" {
//			kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "notes.text_required", nil))
//			return
//		}
//		// A call into the API server, as betterdeskApi.getPeer(id) was
//		if h.d.GoAPI.GetPeer(r.Context(), device) == nil {
//			kit.Fail(w, r, http.StatusNotFound, "Device not found")
//			return
//		}
//		user := kit.User(r) // req.session.user; never nil behind RequireAuth
//		id, err := insertNote(r.Context(), h.d.Store, device, user.Username, text)
//		if err != nil {
//			h.log.Error("Add note error:", err)
//			kit.Fail(w, r, http.StatusInternalServerError, "Failed to add note")
//			return
//		}
//		// db.logAction(req.session.userId, action, details, req.ip); this
//		// route ignored a failure, as most do
//		_ = h.d.LogAction(r, "note_added", "Note added to device "+device)
//		kit.JSON(w, r, http.StatusCreated, added{Success: true, Data: addedData{ID: id}})
//	}
//
//	// added is the answer to a new note. A struct keeps the keys in the
//	// order the old route sent them; a map would sort them.
//	type added struct {
//		Success bool      `json:"success"`
//		Data    addedData `json:"data"`
//	}
//
//	type addedData struct {
//		ID int64 `json:"id"`
//	}
//
// Queries only the group uses live in its store.go as functions that take
// the *store.DB; they are written once with ? placeholders:
//
//	func insertNote(ctx context.Context, db *store.DB, device, author, text string) (int64, error) {
//		id, err := db.Insert(ctx, "INSERT INTO device_notes (device_id, author, text) VALUES (?, ?, ?)", device, author, text)
//		if err != nil {
//			return 0, fmt.Errorf("insert note for device %s: %w", device, err)
//		}
//		return id, nil
//	}
//
// The group is switched on by one line in console/routes.go, at its place
// in the order of web-nodejs/routes/index.js.
//
// # Routes
//
// Patterns are written as for http.ServeMux: {name} for one path segment,
// {name...} at the end for the rest. The table matches like Express did:
// in any letter case, with or without a slash at the end, and the first
// route added that fits answers, not the most specific one. So the order
// of the Node file is the order here, and a route Express never reached
// because an earlier one shadowed it is not ported. See Router.
//
// A Node pattern with a rule on a parameter, /rules/:id(\d+), is the plain
// pattern with a middleware that passes on when the rule fails:
//
//	r.Get("/rules/{id}", h.rule, kit.Digits("id"), d.RequireAuth)
//
// # Two stages
//
// d.Router is where nearly every group registers: its routes sit behind
// the whole pipeline. d.Early is for the RustDesk client API and the
// device API under /api/bd, which Node mounted before the request
// language, the CSRF gate, the no-store cache header, compression and the
// JSON content-type rule. A handler in the early stage has no kit.Lang, is
// never CSRF-checked, and its answers are never compressed.
//
// # Passing on and stepping aside
//
// kit.PassOn(w, r) declines a request before anything is written. The
// table then tries the next route that fits; when none of the stage does,
// the request goes on down the pipeline. Two uses.
//
// An early route that only answers bearer tokens leaves other requests to
// the console route with the same address (Express's next()):
//
//	func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
//		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
//			kit.PassOn(w, r)
//			return
//		}
//		...
//	}
//
// A route of the later stage steps aside for a later route
// (next('route')), usually because its group is mounted twice and the
// route belongs to one mount only. kit.OnMount does that as a middleware,
// and it goes first in the list, where Node had it:
//
//	r.Get("/rules", h.rules, kit.OnMount("/api/automation"), d.RequireAuth)
//
// While the Node console still serves routes, a request that every Go
// route passed on goes to Node as it arrived, body included.
//
// # Mounts
//
// A group that Node mounted at a prefix registers below it, and a group
// mounted twice is registered twice (every address is kept):
//
//	inventory.Register(d.At("/api/bd"))        // router.use('/api/bd', inventoryRoutes)
//	inventory.Register(d.At("/api/inventory")) // router.use('/api/inventory', inventoryRoutes)
//
// Inside, the group writes its patterns as the Node router did ("/",
// "/{id}"). kit.MountPath(r) is req.baseUrl and kit.RoutePath(r) is
// req.path below the mount. Everything else sees the whole address:
// r.URL, kit.OriginalURL(r), and the guards, which answer JSON whenever
// the address the client asked for starts with /api/.
//
// # Guards
//
// The guards are methods of Deps with the names of middleware/auth.js:
// RequireAuth, RequireAdmin, RequireRole(role), RequirePermission(p),
// RequireAnyPermission(a, b), RequireRdClientAuth(p). They answer what the
// Node middleware answered (guards.go lists the answers). Rate limiters go
// in the same list, at the place they had in the Node route:
// d.Limits.Upload.Middleware, d.Limits.FileAccess.Middleware,
// d.Limits.PasswordChange.Middleware, d.Limits.Login.Middleware,
// d.Limits.RDClientPage.Middleware.
//
// Inside a handler, kit.HasPermission(r, "device.edit") is
// requestHasPermission(req, 'device.edit').
//
// A middleware Node mounted with router.use(path, fn) goes into the table
// with Router.Use at the same place: it runs for every method and every
// path below its pattern, in front of the routes added after it, of its
// own group and of later ones, and lets the request go on by calling the
// handler it wraps. The device scope check is one:
//
//	r.Use("/api/devices/{id}", d.RequireDeviceInScope) // router.use('/api/devices/:id', requireDeviceInScope)
//
// # Device routes
//
// Routes that devices call (agents, the desktop client) take a device
// token instead of a session: an access token bound to the device's id,
// sent as "Authorization: Bearer <token>". The guards are the middleware
// of middleware/deviceAuth.js:
//
//   - d.RequireDeviceToken is requireDeviceToken. It answers 401
//     {"error":"Bearer access token required"} without a Bearer token,
//     {"error":"Invalid or unbound access token"} for a token that is
//     unknown, revoked, expired or bound to no device id, and
//     {"error":"Invalid or expired access token"} when the lookup fails.
//     It notes the use on the token's row and ignores a failure to.
//   - d.RequireTokenDeviceMatch is requireTokenDeviceMatch, behind it. A
//     request may name the device it speaks for in the body's device_id,
//     the query's device_id or the header X-Device-Id; the first that is
//     not the empty text or null counts. Another device than the token's,
//     or a value that is no text (a number, a list, an object), is 403
//     {"error":"Device ID mismatch"}.
//
// Neither looks at the session. They go where Node had them in the list:
//
//	r.Post("/api/bd/fleet/task-result", h.taskResult, d.RequireDeviceToken, d.RequireTokenDeviceMatch)
//
//	func (h *handlers) taskResult(w http.ResponseWriter, r *http.Request) {
//		device := kit.Device(r) // never nil behind RequireDeviceToken
//		id := device.DeviceID   // req.deviceId
//		_ = device.Token        // req.deviceToken, the token's row
//		...
//	}
//
// kit.DeviceIDHeader(r) is req.headers['x-device-id'] for a handler that
// reads the header itself. The CSRF gate in front of Router lets a write
// through without a token when nobody is signed in and its path starts
// with /api/bd/ or it carries a bearer token, so a device reaches its
// routes mounted elsewhere too (/api/tickets/bd, /api/dataguard/dlp-events).
//
// # Websockets
//
// A socket is registered on d.Sockets (package console/ws), not on a
// route table: upgrades never pass the pipeline, as Node's upgrade
// listener ran outside Express. The handler runs the socket's checks in
// the old order and answers the handshake:
//
//	d.Sockets.Handle("/ws/notes/{device}", func(u *ws.Upgrade) {
//		if !u.CheckOrigin("notes") { // "HTTP/1.1 403 Forbidden", then close
//			return
//		}
//		if !u.Session().SignedIn() {
//			u.Refuse(http.StatusUnauthorized)
//			return
//		}
//		conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: 64 << 10})
//		if !ok {
//			return
//		}
//		defer conn.Wait()
//		...
//	})
//
// console/sockets.go lists the services whose sockets are switched on.
// Upgrades to a path nobody registered go to the Node console while it
// runs behind this one.
//
// # What a handler reads and writes
//
//	kit.Session(r)            req.session        Get, Set, Delete, Destroy, Regenerate, Establish
//	kit.User(r), kit.UserID(r) req.session.user, req.session.userId
//	kit.Access(r)             the role's permissions, the panel access policy, the section
//	kit.Device(r)             req.deviceId and req.deviceToken behind RequireDeviceToken
//	kit.Lang(r), kit.T(r, key, kit.Vars("n", 3))   req.lang, req.t(key, {n: 3})
//	kit.ClientIP(r)           req.ip
//	kit.Secure(r)             req.secure
//	kit.OriginalURL(r)        req.originalUrl
//	kit.Body(r), kit.Bind(r, &v)   req.body: Text, Int, Flag, String, Has, Raw
//	kit.Cookie(r, name)       req.cookies[name]
//	kit.CSRFToken(r)          res.locals.csrfToken
//	r.PathValue("id")         req.params.id
//	r.URL.Query()             req.query, for plain values
//
//	kit.JSON(w, r, status, v)             res.status(status).json(v)
//	kit.OK(w, r, data)                    res.json({ success: true, data })
//	kit.Fail(w, r, status, message)       res.status(status).json({ success: false, error: message })
//	kit.FailCode(w, r, status, message, code)
//	kit.Redirect(w, r, target)            res.redirect(target)
//	kit.SetCookie(w, name, value, kit.CookieOptions{...})   res.cookie(name, value, {...})
//
// JSON answers must have the bytes Node sent: the same keys in the same
// order, null where Node sent null and no key where Node left it out. Use
// a struct with json tags for a fixed shape. kit.JSON writes the ETag,
// answers 304 and compresses as every JSON answer of the console does.
//
// # Testing a group
//
// A group's tests build a Deps with what the group uses and call Register
// on a NewRouter, or run the whole console with console.New on a
// storetest database, which is what the tests of the first groups do
// (console/routes_test.go). kit.Begin, kit.SetAccess and kit.SetLanguage
// stand in for the pipeline in a test that calls a handler directly.
package kit
