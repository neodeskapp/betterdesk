// Package goapipath is what the console's thin route groups share: the
// routes that take a request, build a path on the API server from it and
// hand the API server's answer back.
//
// # Paths
//
// A value from a request goes into an API path only through
// AssertSafeAPIID or SafeSegment (lib/goApiPath.js, lib/goApiProxy.js):
// one path segment of letters, digits and . _ : @ % + -, never "." and
// never with ".." in it. The refusal is an error whose text starts with
// "Invalid ", which IsInvalid reports and the routes answer with 400.
// AssertSafeGoAPIRelativePath is the check on a whole path that the
// client in package goapi runs before every call.
//
// # Passing a call on
//
// ProxyToGo and ProxyBinaryToGo call the API server and answer with what
// it said (lib/goApiProxy.js). PathOf builds the path of such a call from
// the route's parameters, and RequestBody is req.body for the call.
//
// # What a route reads from the query
//
// Query is req.query[name] as Express built it with the qs package: text,
// a list or an object, depending on how often the name comes and whether
// it carries brackets. QueryValue.Text is String(value) and
// QueryValue.Truthy is the test of "if (req.query.name)".
//
// ParseInt, NumberText, Slice and FormEncode are the JavaScript
// functions the routes used on such values: parseInt, the text of a
// number, String.prototype.slice and URLSearchParams.
package goapipath
