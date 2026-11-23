package servicedef

import (
	"bytes"
	"encoding/json"
)

// The recorded Node answers that the Go console gives otherwise on
// purpose: Node's answer was a bug that is fixed here. Each names its row
// of BUGS.md.

// nodeUnitHint is the hint Node gave for a unit it may not read or write:
// a script that went with the Node console (NL4). Go names the installer's
// update, which writes the unit.
const nodeUnitHint = "Run once as root: sudo node web-nodejs/scripts/linux-ensure-console-user.js"

// goUnitHint is the hint Go gives instead (NL4).
const goUnitHint = "Run once as root: sudo ./neodesk.sh --update"

// withUnitHint is a recorded result with the hint Go gives (NL4).
func withUnitHint(node json.RawMessage) json.RawMessage {
	return bytes.ReplaceAll(node, []byte(nodeUnitHint), []byte(goUnitHint))
}
