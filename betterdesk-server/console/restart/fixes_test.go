package restart

import (
	"os"
	"path/filepath"
)

// The bug fixes of BUGS.md that change answers of the Node vectors
// (testdata/vectors.json.gz). TestNodeVectors checks such a step against
// the fixed behaviour here instead of Node's answer, and then puts Node's
// transaction file in place, so the steps after it start where Node's did.

// fixedStep checks a step whose answer a fix changes on purpose. It
// returns the bug id and what is wrong with Go's answer, or "" for a step
// that Go answers as Node did.
func (w *oracleWorld) fixedStep(step vectorStep, out *string, err error) (bug string, problems []string) {
	// RC2: a restart that fails with an error undoes the changes as one
	// that reports success false does. Node kept them.
	if step.Op == "confirm" && step.Script.Restart.Throw != nil && len(step.Calls) > 0 && string(step.Calls[0]) == `["restartServer"]` {
		if err == nil || step.Err == nil || err.Error() != step.Err.Message {
			problems = append(problems, "the error of the restart is not the answer: "+describe(out))
		}
		if len(w.calls) == 0 || w.calls[0] != `["restartServer"]` || w.exit {
			problems = append(problems, "the restart was not tried once, or an exit was scheduled")
		}
		state := w.svc.load()
		if state == nil {
			return "RC2", append(problems, "no transaction after the failed restart")
		}
		failures, _ := state.Get("rollbackFailures").([]any)
		want := "service_restart_failed_rolled_back"
		if len(failures) > 0 {
			want = "service_restart_failed_rollback_failed"
		}
		if phaseOf(state) != PhaseFailed || !isText(state.Get("error"), want) {
			problems = append(problems, "the transaction is not failed with "+want)
		}
		if changes, _ := state.Get("changes").([]any); len(w.calls)-1+len(failures) < len(changes) {
			problems = append(problems, "not every change was undone or reported as failed")
		}
		return "RC2", problems
	}
	return "", nil
}

// resync writes Node's transaction file after step, or removes it when
// Node had none.
func (w *oracleWorld) resync(step vectorStep) {
	w.t.Helper()
	path := filepath.Join(w.dir, stateFile)
	if step.File == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			w.t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(*step.File), 0o600); err != nil {
		w.t.Fatal(err)
	}
}
