// Package goapi is the console's client for the API server that runs in
// the same process. It calls the API server's handler directly, without a
// socket, and returns what the Node console's client code returned for the
// same call, so that a console route can be written as a direct translation
// of its Node version.
//
// # Who the API server sees
//
// Every call acts as the console's API key: user "apikey:console-bridge",
// role "admin", unless the call names another identity with As (the device
// group writes do so while the panel trust bridge is on). The caller
// address is loopback and the Host is the API
// address the console is configured with (WithHost). The API server puts
// these into audit entries, pairing codes and its per-address limits, so
// they are the same as for a call over the network.
//
// # Three layers
//
// The methods named after the functions of services/betterdeskApi.js
// (GetPeer for getPeer, ChangePeerID for changePeerId, GetCDAPStatus for
// getCDAPStatus) return what those functions returned: see wrap.go for the
// shapes. The methods never return an error, because the functions never
// threw; the one exception is GetPeerConnectPassword. A failed call shows
// in the result, and for some methods it looks like an empty list or a
// missing device.
//
// Backend is services/serverBackend.js: the device list and the device
// calls most routes use, with the console's own tables behind a Store.
//
// Get, Post, Put, Patch, Delete and Request are the axios client the Node
// code exported as apiClient, for the routes that called it directly. Paths
// are relative to /api. Errors follow axios: see Get.
//
// The sign-in calls of services/authService.js are in auth.go, and Events
// gives the frames of the API server's event websocket from the event bus.
//
// # Values
//
// JSON from the API server is handed on as Parse returns it: nil, bool,
// float64, string, []any and *Object, an object that keeps its keys in
// order. Methods that return an object return an *Object; nil stands for
// null. A method that returns a list never returns a nil list. All of them
// encode with encoding/json to the JSON the Node function's result encoded
// to. Truthy, Dig and the accessors of Object stand in for JavaScript's
// "a || b", "a?.b" and property reads when a route looks into a result.
//
// # Requests that need the caller's connection
//
// A websocket upgrade cannot go through a recorder. Handler returns the
// API server's handler with the client's identity, Host and caller address
// applied, for a route that passes the browser's connection on.
package goapi
