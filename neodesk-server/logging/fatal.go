package logging

import (
	"fmt"
	"log"
	"os"
)

// Fatalf logs the message as a FATAL line, which the level filter keeps at
// every LOG_LEVEL, and ends the process with status 1, as log.Fatalf does.
func Fatalf(format string, args ...any) {
	_ = log.Output(2, "FATAL: "+fmt.Sprintf(format, args...))
	os.Exit(1)
}

// Fatal is Fatalf with the arguments of log.Fatal.
func Fatal(args ...any) {
	_ = log.Output(2, "FATAL: "+fmt.Sprint(args...))
	os.Exit(1)
}
