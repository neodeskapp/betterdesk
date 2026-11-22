package main

import (
	"os"
	"os/exec"
)

// terminate ends a process; Windows has no SIGTERM.
func terminate(p *os.Process) error { return p.Kill() }

// exitSignal is "": Windows processes do not end by signals.
func exitSignal(*exec.ExitError) string { return "" }
