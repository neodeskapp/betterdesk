package restart

import (
	"context"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// SupervisorRestart is the restart of a process that a supervisor starts
// again (DECISIONS A30): no command runs, the restart is the process's
// own exit after the answer (Options.Exit). The result names the
// supervisor and the method "exit", and never fails.
func SupervisorRestart(supervisor string) func(context.Context) (serverinfo.RestartResult, error) {
	return func(context.Context) (serverinfo.RestartResult, error) {
		return serverinfo.RestartResult{
			Source: "supervisor",
			Restarts: []*goapi.Object{goapi.NewObject(
				"component", "server", "service", supervisor, "success", true, "method", "exit",
			)},
		}, nil
	}
}
