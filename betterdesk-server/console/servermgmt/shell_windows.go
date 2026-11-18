package servermgmt

import (
	"os"
	"os/exec"
)

// isRoot is false on Windows: a shell never switches accounts there.
func isRoot() bool { return false }

// canExecute is true for a file that exists; Windows decides by name.
func canExecute(string) bool { return true }

// startPipe starts the shell with pipes and no arguments.
func (s *Service) startPipe(cols, rows int, a Account) (*shellProc, error) {
	cmd := exec.Command(firstSet(a.Shell, s.lastResortShell()))
	cmd.Dir = firstSet(a.Home, s.home())
	cmd.Env = s.shellEnv(a, cols, rows, false)
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
	}
	for i := 0; i < 3; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			closeAll()
			return nil, err
		}
		files = append(files, r, w)
	}
	inR, inW, outR, outW, errR, errW := files[0], files[1], files[2], files[3], files[4], files[5]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if err := cmd.Start(); err != nil {
		closeAll()
		return nil, err
	}
	inR.Close()
	outW.Close()
	errW.Close()
	return newShellProc("pipe", cmd, inW, []*os.File{outR, errR}), nil
}

// signalShell ends the shell; Windows has no hang-up or process groups.
func signalShell(p *shellProc, _ bool) {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// exitStatus is the shell's exit code and no signal.
func exitStatus(_ string, cmd *exec.Cmd, _ error) (code, signal any) {
	if cmd.ProcessState == nil {
		return -1, nil
	}
	return cmd.ProcessState.ExitCode(), nil
}
