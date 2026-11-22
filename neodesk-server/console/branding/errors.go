package branding

import (
	"errors"
	"fmt"
)

// Error is a failure the console's code produced itself: a rule it checks
// ("Profile name is required") or a value JavaScript cannot work with
// ("Cannot convert object to primitive value"). Message is the text the
// error had, which some routes put into their answer.
type Error struct {
	// Name is the kind of JavaScript error: "Error", "TypeError",
	// "SyntaxError", "RangeError" or "URIError".
	Name    string
	Message string
	// Err is the failure underneath, when there is one.
	Err error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// Message returns the text a route would have read from err.message: the
// Message of an *Error anywhere in the chain, and the error's own text
// otherwise.
func Message(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

func syntaxError(msg string) *Error { return &Error{Name: "SyntaxError", Message: msg} }

// throw raises an Error the way JavaScript throws; catch turns it into the
// returned error at the edge of the package.
func throw(msg string)     { panic(&Error{Name: "Error", Message: msg}) }
func throwType(msg string) { panic(&Error{Name: "TypeError", Message: msg}) }

// catch recovers an Error raised with throw and stores it in *err. Any
// other panic goes on.
func catch(err *error) {
	if r := recover(); r != nil {
		e, ok := r.(*Error)
		if !ok {
			panic(r)
		}
		*err = e
	}
}

// try runs fn and returns the Error it threw, or nil.
func try(fn func()) (err error) {
	defer catch(&err)
	fn()
	return nil
}

// wrap adds what was being done to an error, keeping the message a route
// would send.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}
