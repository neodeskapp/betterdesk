//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// serviceLogLimit is the size at which the service's log moves to
// server.log.1.
const serviceLogLimit = 10 << 20

// runAsWindowsService runs server as the Windows service NeoDeskServer
// when the service manager started the process, and reports whether it
// did. A service has no console: the log goes to logs\server.log next to
// the binary, which moves to server.log.1 at 10 MB, and so does the
// output of a crash.
func runAsWindowsService(server func(stop <-chan struct{}) int) (int, bool) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return 0, false
	}
	// The service manager starts it in System32; the server's folder is
	// where NSSM and the scheduled task started it, for relative paths
	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}
	logFile, err := openServiceLog()
	if err == nil {
		log.SetOutput(logFile)
		logging.SetDestination(logFile)
		defer logFile.Close()
	}
	handler := &windowsService{server: server}
	if err := svc.Run(windowsServiceName, handler); err != nil {
		log.Printf("ERROR: the Windows service could not start: %v", err)
		return 1, true
	}
	return handler.exitCode, true
}

// openServiceLog opens logs\server.log in the binary's folder.
func openServiceLog() (*logging.RotatingFile, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(filepath.Dir(exe), "logs", "server.log")
	return logging.OpenRotatingFile(path, serviceLogLimit, func(f *os.File) {
		// A crash's output goes to the current file; nil lets go of the
		// file before it moves aside
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	})
}

// windowsService is the service's handler for golang.org/x/sys/windows/svc.
type windowsService struct {
	server   func(stop <-chan struct{}) int
	exitCode int
}

// Execute runs the server until the service is stopped or Windows shuts
// down. A non-zero exit status (a restart the console asked for) is
// reported as the service's own error code, which the recovery settings
// the installer makes (sc.exe failureflag) answer with a new start.
func (w *windowsService) Execute(_ []string, changes <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	requests := make(chan serviceRequest)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		for {
			select {
			case <-quit:
				return
			case c := <-changes:
				var req serviceRequest
				switch c.Cmd {
				case svc.Stop, svc.Shutdown:
					req = requestStop
				case svc.Interrogate:
					req = requestInterrogate
				default:
					continue
				}
				select {
				case requests <- req:
				case <-quit:
					return
				}
			}
		}
	}()
	report := func(r serviceReport) {
		s := svc.Status{CheckPoint: r.CheckPoint, WaitHint: uint32(r.WaitHint / time.Millisecond)}
		switch r.State {
		case serviceRunning:
			s.State, s.Accepts = svc.Running, svc.AcceptStop|svc.AcceptShutdown
		case serviceStopPending:
			s.State = svc.StopPending
		}
		status <- s
	}
	code := superviseServer(w.server, requests, report, 2*time.Second, serviceStopLimit)
	w.exitCode = code
	log.Printf("Windows service %s stopped (exit status %d)", windowsServiceName, code)
	if code != 0 {
		return true, uint32(code)
	}
	return false, 0
}

var _ svc.Handler = (*windowsService)(nil)
