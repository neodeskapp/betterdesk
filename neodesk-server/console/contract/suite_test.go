//go:build contract

package contract_test

import (
	"os"
	"testing"

	"github.com/unitronix/betterdesk-server/console/contract"
)

func TestMain(m *testing.M) { os.Exit(contract.Main(m)) }

// TestMatrix sends one placeholder request per console route for every role.
func TestMatrix(t *testing.T) { contract.RunMatrix(t) }

// TestCases runs the hand-written cases of the cases_*_test.go files.
func TestCases(t *testing.T) { contract.RunCases(t) }

// TestEveryRouteIsCovered fails when a route gets no request from the suite.
func TestEveryRouteIsCovered(t *testing.T) { contract.CheckCoverage(t) }
