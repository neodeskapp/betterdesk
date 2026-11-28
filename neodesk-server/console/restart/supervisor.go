package restart

import (
	"context"
	"errors"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
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

// ErrClusterRestart is what a restart asked of one instance of several
// answers: the pods restart together, through the cluster.
var ErrClusterRestart = errors.New("this server is one of several instances: restart them with `kubectl rollout restart deployment/neodesk`")

// ClusterRestart is the restart of an instance that shares its
// installation: it restarts nothing and says how to.
func ClusterRestart(context.Context) (serverinfo.RestartResult, error) {
	return serverinfo.RestartResult{}, ErrClusterRestart
}
