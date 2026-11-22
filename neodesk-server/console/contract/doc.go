// Package contract is the black-box contract suite for the web console: it
// records what a console answers and checks that another console answers
// the same. The goldens are recorded against the Node console and are the
// specification for the Go console. Once the Go console answers a route, a
// recording leaves its goldens as they are (README.md, "Frozen goldens").
//
// The suite talks to a base URL (BD_URL) and imports nothing from the
// console. It has two layers. The matrix (RunMatrix) sends one placeholder
// request per route in testdata/routes.json for every role and signed out.
// The cases (RunCases) are hand-written requests, registered per route group
// with Group from files named cases_<group>_test.go.
//
// The tests that need a console are behind the build tag "contract"
// (suite_test.go and the cases files). Everything else, and its unit tests,
// builds without the tag. README.md says how to run, record and read the
// suite.
package contract

//go:generate go run ./gen -inventory /notes/inventory.json -out testdata/routes.json
