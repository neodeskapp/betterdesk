// Package branding is the console's own look: the appearance settings and
// their defaults, the rules that clean every value before it is stored,
// the stylesheet /css/branding.css made from the settings, saved profiles
// and built-in themes, custom fonts, uploaded logo and background
// pictures, and the supporters list of the Help panel.
//
// The settings live in the table branding_config, one row per setting,
// and are served from a cache that LoadBranding fills at start and after
// every change. Profiles live in branding_profiles. Neither table is
// shared with the signal server; storage.md says how these settings relate
// to the "client branding" the server keeps for desktop clients.
//
// Settings arrive as JSON and may hold anything. The cleaning rules treat
// values the way the console's JavaScript did (a number where text was
// expected becomes its text, a list its items joined by commas, and so
// on), so the package works on JavaScript-shaped values: see Undefined for
// the list, ParseJSON to read them and Stringify to write them. What the
// functions return can go into a JSON answer as it is: the objects keep
// the key order the web console has always seen.
//
// routes.md lists the routes that call this package.
package branding
