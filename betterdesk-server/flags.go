package main

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/unitronix/betterdesk-server/internal/envvar"
)

// switchFlag is an on/off flag. The flag alone turns it on; -name=value
// takes the values the variables take (envvar.Switch) and t and f, which
// Go's own flags took.
type switchFlag struct{ value *bool }

func (f switchFlag) String() string {
	if f.value == nil {
		return "false"
	}
	return strconv.FormatBool(*f.value)
}

func (f switchFlag) Set(s string) error {
	if on, ok := envvar.Switch(s); ok {
		*f.value = on
		return nil
	}
	on, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("not an on/off value: use %s", envvar.SwitchValues)
	}
	*f.value = on
	return nil
}

// IsBoolFlag lets the flag stand alone, as -name.
func (f switchFlag) IsBoolFlag() bool { return true }

// switchVar defines an on/off flag whose default is what *value holds.
func switchVar(fs *flag.FlagSet, value *bool, name, usage string) {
	fs.Var(switchFlag{value}, name, usage)
}
