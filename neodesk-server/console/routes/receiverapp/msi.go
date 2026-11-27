package receiverapp

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Properties are the MSI properties of a silent install. Their names are
// a contract with the client's MSI (res/msi in neodeskapp/neodesk-client):
//
//	SERVER  ID server, host[:port]           required for a silent install
//	KEY     the server's public key          required for a silent install
//	API     API address, https://SERVER when left out
//	TOKEN   enrollment token (managed and locked servers)
//	GROUP   folder (device group) id or name, optional; the token's folder wins
type Properties struct {
	Server Server
	Token  string
	// Group is a folder id, 0 for none.
	Group int64
}

// msiValue quotes a property value for msiexec: as it is when it has no
// space or quote, else in double quotes with each quote doubled.
func msiValue(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\"") {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
}

// Args are the properties as msiexec takes them, in the contract's order;
// empty ones are left out.
func (p Properties) Args() []string {
	var out []string
	add := func(name, value string) {
		if value != "" {
			out = append(out, name+"="+msiValue(value))
		}
	}
	add("SERVER", p.Server.IDServer)
	add("KEY", p.Server.Key)
	add("API", p.Server.APIServer)
	add("TOKEN", p.Token)
	if p.Group > 0 {
		add("GROUP", strconv.FormatInt(p.Group, 10))
	}
	return out
}

// Command is the silent install as an admin types it next to the MSI.
func Command(p Properties) string {
	return strings.Join(append([]string{"msiexec", "/i", MSIName, "/qn"}, p.Args()...), " ")
}

// InstallCmd is install.cmd: it runs the silent install with the MSI next
// to it and stops when it is not run as administrator. A percent sign is
// doubled, the only character a batch file reads in a quoted value.
func InstallCmd(p Properties) string {
	args := strings.ReplaceAll(strings.Join(p.Args(), " "), "%", "%%")
	lines := []string{
		"@echo off",
		"rem Installs the NeoDesk app for " + strings.ReplaceAll(p.Server.IDServer, "%", "%%") + " without questions.",
		"rem Run it as administrator, next to " + MSIName + ".",
		"net session >nul 2>&1",
		"if errorlevel 1 (",
		"  echo Run install.cmd as administrator.",
		"  exit /b 1",
		")",
		`msiexec /i "%~dp0` + MSIName + `" /qn ` + args,
		"exit /b %errorlevel%",
		"",
	}
	return strings.Join(lines, "\r\n")
}

// HashToken is how device_tokens keeps a token: its SHA-256 in hex.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
