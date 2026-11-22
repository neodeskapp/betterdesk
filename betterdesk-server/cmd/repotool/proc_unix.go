//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// terminate asks a process to end, as Node's child.kill() does.
func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

// signalNames are the names Node gives the signals a process ends by.
var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGILL: "SIGILL", syscall.SIGTRAP: "SIGTRAP", syscall.SIGABRT: "SIGABRT",
	syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL",
	syscall.SIGSEGV: "SIGSEGV", syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM",
	syscall.SIGTERM: "SIGTERM", syscall.SIGUSR1: "SIGUSR1", syscall.SIGUSR2: "SIGUSR2",
}

// exitSignal is the name of the signal a process ended by, or "".
func exitSignal(e *exec.ExitError) string {
	ws, ok := e.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	if name, ok := signalNames[ws.Signal()]; ok {
		return name
	}
	return ws.Signal().String()
}
