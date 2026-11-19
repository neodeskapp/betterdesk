package managedconfig

import _ "embed"

// template is the console's env file template, a copy of
// web-nodejs/.env.example that gen_allowlist.go writes with the allowlist.
//
//go:embed env.example
var template string

// Template is the env file template (.env.example): what a fresh
// install's env file is made from and where the allowlist comes from.
// Its placeholders (__KEY__) are filled when the file is written.
func Template() string { return template }
