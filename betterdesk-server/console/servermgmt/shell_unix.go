//go:build unix

package servermgmt

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"syscall"
)

// isRoot reports a console that runs as root (process.getuid() === 0).
func isRoot() bool { return os.Getuid() == 0 }

// canExecute is access(file, X_OK).
func canExecute(file string) bool { return syscall.Access(file, 1) == nil }

// errBadID is an account whose user or group id is no id the system takes.
var errBadID = errors.New("user or group id out of range")

// credential is the account a shell switches to: only a console that runs
// as root switches, and only to another account than root. The
// supplementary groups are dropped.
func (s *Service) credential(a Account) (*syscall.Credential, error) {
	if !s.opts.Terminal.IsRoot() || a.UID == 0 {
		return nil, nil
	}
	id := func(v float64) (uint32, bool) {
		return uint32(v), v >= 0 && v <= math.MaxUint32 && v == math.Trunc(v)
	}
	uid, uidOK := id(a.UID)
	gid, gidOK := id(a.GID)
	if !uidOK || !gidOK {
		return nil, errBadID
	}
	return &syscall.Credential{Uid: uid, Gid: gid}, nil
}

// startPipe starts the shell with pipes, interactive ("-i"), in its own
// process group.
func (s *Service) startPipe(cols, rows int, a Account) (*shellProc, error) {
	cred, err := s.credential(a)
	if err != nil {
		return nil, err
	}
	shell := firstSet(a.Shell, s.lastResortShell())
	cmd := exec.Command(shell, "-i")
	cmd.Dir = firstSet(a.Home, s.home())
	cmd.Env = s.shellEnv(a, cols, rows, false)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: cred}
	return startWithPipes(cmd)
}

// startWithPipes starts cmd with three pipes of its own.
func startWithPipes(cmd *exec.Cmd) (*shellProc, error) {
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
	}
	pipe := func() (r, w *os.File, err error) {
		r, w, err = os.Pipe()
		if err == nil {
			files = append(files, r, w)
		}
		return r, w, err
	}
	inR, inW, err := pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := pipe()
	if err != nil {
		closeAll()
		return nil, err
	}
	errR, errW, err := pipe()
	if err != nil {
		closeAll()
		return nil, err
	}
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

// signalShell sends the shell and its process group the hang-up (a
// terminal) or the termination (pipes), or the kill.
func signalShell(p *shellProc, kill bool) {
	if p.cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	switch {
	case kill:
		sig = syscall.SIGKILL
	case p.kind == "pty":
		sig = syscall.SIGHUP
	}
	pid := p.cmd.Process.Pid
	_ = syscall.Kill(pid, sig)
	_ = syscall.Kill(-pid, sig)
}

// exitStatus is how the shell ended, as the old console reported it. A
// terminal (node-pty) gave the exit code and the signal number, 0 for
// none of either; pipes gave the exit code, -1 after a signal, and the
// signal's name or null.
func exitStatus(kind string, cmd *exec.Cmd, _ error) (code, signal any) {
	ws, ok := syscall.WaitStatus(0), false
	if cmd.ProcessState != nil {
		ws, ok = cmd.ProcessState.Sys().(syscall.WaitStatus)
	}
	if kind == "pty" {
		c, sig := 0, 0
		if ok && ws.Exited() {
			c = ws.ExitStatus()
		}
		if ok && ws.Signaled() {
			sig = int(ws.Signal())
		}
		return c, sig
	}
	if ok && ws.Exited() {
		return ws.ExitStatus(), nil
	}
	if ok && ws.Signaled() {
		if name, known := signalNames[ws.Signal()]; known {
			return -1, name
		}
		return -1, nil
	}
	return -1, nil
}

// signalNames are the names Node gave the signals.
var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT", syscall.SIGILL: "SIGILL",
	syscall.SIGTRAP: "SIGTRAP", syscall.SIGABRT: "SIGABRT", syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE",
	syscall.SIGKILL: "SIGKILL", syscall.SIGUSR1: "SIGUSR1", syscall.SIGSEGV: "SIGSEGV", syscall.SIGUSR2: "SIGUSR2",
	syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM", syscall.SIGCHLD: "SIGCHLD",
	syscall.SIGCONT: "SIGCONT", syscall.SIGSTOP: "SIGSTOP", syscall.SIGTSTP: "SIGTSTP", syscall.SIGTTIN: "SIGTTIN",
	syscall.SIGTTOU: "SIGTTOU", syscall.SIGURG: "SIGURG", syscall.SIGXCPU: "SIGXCPU", syscall.SIGXFSZ: "SIGXFSZ",
	syscall.SIGVTALRM: "SIGVTALRM", syscall.SIGPROF: "SIGPROF", syscall.SIGWINCH: "SIGWINCH", syscall.SIGIO: "SIGIO",
	syscall.SIGSYS: "SIGSYS",
}
