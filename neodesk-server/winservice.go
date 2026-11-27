package main

import (
	"log"
	"time"
)

// The Windows service's part that does not need Windows: what the server
// does with the service manager's requests and what it reports back.
// winservice_windows.go connects it to golang.org/x/sys/windows/svc.

// windowsServiceName is the name of the service the installer registers.
const windowsServiceName = "NeoDeskServer"

// serviceStopLimit is how long a stop may take before the service reports
// itself stopped anyway; the service manager ends the process then.
const serviceStopLimit = 60 * time.Second

// serviceState is a state the service reports to the service manager.
type serviceState int

const (
	serviceRunning serviceState = iota
	serviceStopPending
)

// serviceRequest is a request of the service manager.
type serviceRequest int

const (
	// requestStop is a stop of the service or the shutdown of Windows.
	requestStop serviceRequest = iota
	// requestInterrogate asks for the current state again.
	requestInterrogate
)

// serviceReport is one status the service sends: its state, and while it
// stops a checkpoint that grows and how long until the next one.
type serviceReport struct {
	State      serviceState
	CheckPoint uint32
	WaitHint   time.Duration
}

// superviseServer runs server, which runs until the channel it gets is
// closed, and answers the service manager's requests until it ends. It
// reports the service running at once, so the service manager does not
// wait for the database to be set up, and a stop as pending with a
// checkpoint every tick until the server has stopped all its parts or
// limit passed. It returns the server's exit status: 0 after a stop,
// updater.ExitRestart after a restart the console asked for, which the
// service's recovery settings answer with a new start; after limit it
// returns 1.
func superviseServer(server func(stop <-chan struct{}) int, requests <-chan serviceRequest, report func(serviceReport), tick, limit time.Duration) int {
	stop := make(chan struct{})
	done := make(chan int, 1)
	go func() { done <- server(stop) }()
	current := serviceReport{State: serviceRunning}
	report(current)

	var ticker *time.Ticker
	var tickC <-chan time.Time
	var deadline <-chan time.Time
	for {
		select {
		case code := <-done:
			if ticker != nil {
				ticker.Stop()
			}
			return code
		case req, ok := <-requests:
			if !ok {
				requests = nil
				continue
			}
			switch req {
			case requestInterrogate:
				report(current)
			case requestStop:
				if current.State == serviceStopPending {
					report(current)
					continue
				}
				current = serviceReport{State: serviceStopPending, CheckPoint: 1, WaitHint: 2 * tick}
				report(current)
				close(stop)
				ticker = time.NewTicker(tick)
				tickC = ticker.C
				deadline = time.After(limit)
			}
		case <-tickC:
			current.CheckPoint++
			report(current)
		case <-deadline:
			ticker.Stop()
			log.Printf("ERROR: the server did not stop within %s; the service stops anyway", limit)
			return 1
		}
	}
}
