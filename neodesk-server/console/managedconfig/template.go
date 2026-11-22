package managedconfig

import _ "embed"

// template is the console's env file template (.env.example), from which
// gen_allowlist.go writes the allowlist.
//
//go:embed env.example
var template string

// Template is the env file template (.env.example): what a fresh
// install's env file is made from and where the allowlist comes from.
// Its placeholders (__KEY__) are filled when the file is written.
func Template() string { return template }
