package contract

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A case can compute values from earlier ones (Case.Compute): a variable
// gets the result of a function of a variable or a secret, right before
// the request is sent. The functions are a closed list:
//
//	totp(x)       The two-factor code of the base32 secret x for the
//	              current time step, as console/authn computes it (RFC
//	              6238: HMAC-SHA1, 6 digits, steps of 30 seconds; a secret
//	              of fewer than 10 bytes is repeated to 20 bytes). When the
//	              step has less than three seconds left, it waits for the
//	              next one, so the code is still good when the console
//	              checks it.
//	totp_next(x)  The code of x for the first step after the step of the
//	              last code this turn computed for x, waiting for that step
//	              when it has not begun (without an earlier code: the first
//	              step after now). For a case that must not send a code the
//	              console saw before. A case has at most one of it.
//
// A code is text of six digits, with its zeros in front. It is a plain
// variable: a code is good for 30 seconds and tells nothing of the secret.

// computeFunc is one function of the list.
type computeFunc struct {
	// waits says the function may wait for the clock. A case has at most
	// one such function, and it runs before the others, so no value goes
	// stale while it waits.
	waits bool
	run   func(tn *turn, arg computeArg) (string, error)
}

var computeFunctions = map[string]computeFunc{
	"totp":      {run: totpNow},
	"totp_next": {waits: true, run: totpNext},
}

// computeArg is the argument of a function: a variable, or a secret when
// secret is set.
type computeArg struct {
	name   string
	secret bool
}

func (a computeArg) String() string {
	if a.secret {
		return "secret:" + a.name
	}
	return a.name
}

// expression is one entry of Case.Compute.
type expression struct {
	target, function string
	arg              computeArg
}

var expressionSyntax = regexp.MustCompile(`^([a-z_]+)\((secret:)?([A-Za-z0-9_]+)\)$`)

// parseCompute reads Case.Compute in the order the functions run: the one
// that waits first, the others by the name of their variable.
func parseCompute(entries map[string]string) ([]expression, error) {
	var list []expression
	waiting := 0
	for target, text := range entries {
		if !variableName.MatchString(target) {
			return nil, fmt.Errorf("compute %q: a variable name has letters, digits and _", target)
		}
		m := expressionSyntax.FindStringSubmatch(text)
		if m == nil {
			return nil, fmt.Errorf("compute %s: %q is not function(variable) or function(secret:name)", target, text)
		}
		fn, known := computeFunctions[m[1]]
		if !known {
			return nil, fmt.Errorf("compute %s: unknown function %q (there are %s)", target, m[1], strings.Join(computeNames(), ", "))
		}
		if fn.waits {
			waiting++
		}
		list = append(list, expression{target: target, function: m[1], arg: computeArg{name: m[3], secret: m[2] != ""}})
	}
	if waiting > 1 {
		return nil, fmt.Errorf("compute: %d functions that wait in one case; a value would go stale while the next one waits", waiting)
	}
	for _, e := range list {
		if _, computed := entries[e.arg.name]; computed && !e.arg.secret {
			return nil, fmt.Errorf("compute %s: %s is computed by the same case; use it in a later case", e.target, e.arg.name)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		wi, wj := computeFunctions[list[i].function].waits, computeFunctions[list[j].function].waits
		if wi != wj {
			return wi
		}
		return list[i].target < list[j].target
	})
	return list, nil
}

func computeNames() []string {
	names := make([]string, 0, len(computeFunctions))
	for name := range computeFunctions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// compute sets the variables of the case's Compute.
func (c *Case) compute(tn *turn) error {
	list, err := parseCompute(c.Compute)
	if err != nil {
		return err
	}
	for _, e := range list {
		value, err := computeFunctions[e.function].run(tn, e.arg)
		if err != nil {
			return fmt.Errorf("compute %s = %s(%s): %w", e.target, e.function, e.arg, err)
		}
		tn.vars[e.target] = value
	}
	return nil
}

// argText is the value of an argument as text.
func (tn *turn) argText(arg computeArg) (string, error) {
	if arg.secret {
		return tn.secret(arg.name)
	}
	value, ok := tn.vars[arg.name]
	if !ok {
		return "", fmt.Errorf("variable %s has no value: no case of this turn captured it", arg.name)
	}
	text, isText := value.(string)
	if !isText {
		return "", fmt.Errorf("variable %s is a %s, not text", arg.name, jsonType(value))
	}
	return text, nil
}

const (
	// totpStep is the length of a time step.
	totpStep = 30 * time.Second
	// totpMargin is how much of a step must be left for its code: a code
	// taken later could reach the console in the next step, where it no
	// longer counts.
	totpMargin = 3 * time.Second
	// totpSlack is how long after the start of a step a wait ends, so the
	// console is in the step too.
	totpSlack = 200 * time.Millisecond
)

// totpStepAt is the number of the step that holds t.
func totpStepAt(t time.Time) int64 { return t.UnixMilli() / totpStep.Milliseconds() }

// stepStart is when a step begins.
func stepStart(step int64) time.Time { return time.UnixMilli(step * totpStep.Milliseconds()) }

func totpNow(tn *turn, arg computeArg) (string, error) {
	secret, err := tn.argText(arg)
	if err != nil {
		return "", err
	}
	step := totpStepAt(tn.clock.Now())
	if stepStart(step+1).Sub(tn.clock.Now()) < totpMargin {
		step = tn.waitFor(step + 1)
	}
	return tn.totpFor(arg, secret, step)
}

func totpNext(tn *turn, arg computeArg) (string, error) {
	secret, err := tn.argText(arg)
	if err != nil {
		return "", err
	}
	now := totpStepAt(tn.clock.Now())
	next, used := tn.steps[arg.String()]
	if !used {
		next = now
	}
	step := max(next+1, now)
	if step == now && stepStart(step+1).Sub(tn.clock.Now()) < totpMargin {
		step++
	}
	if step > now {
		step = tn.waitFor(step)
	}
	return tn.totpFor(arg, secret, step)
}

// waitFor waits until the step has begun and returns the step it is then.
func (tn *turn) waitFor(step int64) int64 {
	if wait := stepStart(step).Add(totpSlack).Sub(tn.clock.Now()); wait > 0 {
		tn.clock.Sleep(wait)
	}
	return max(step, totpStepAt(tn.clock.Now()))
}

// totpFor is the code of a secret for a step, noted as the last code of
// the argument.
func (tn *turn) totpFor(arg computeArg, secret string, step int64) (string, error) {
	code, err := totpCode(secret, step)
	if err != nil {
		return "", fmt.Errorf("the value of %s is not a base32 secret", arg)
	}
	tn.steps[arg.String()] = step
	return code, nil
}

// totpCode is the six-digit code of a base32 secret for a time step.
func totpCode(secret string, step int64) (string, error) {
	key, err := totpKey(secret)
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// totpKeyLength is the length of the key the console makes from a short
// secret.
const totpKeyLength = 20

// totpKey decodes a base32 secret (either case, with or without padding)
// into the HMAC key. A secret of fewer than 10 bytes is repeated, as the
// console does it, and cut to 20 bytes.
func totpKey(secret string) ([]byte, error) {
	text := strings.TrimRight(strings.ToUpper(secret), "=")
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(text)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("not base32")
	}
	if 2*len(raw) >= totpKeyLength {
		return raw, nil
	}
	key := make([]byte, 0, totpKeyLength+len(raw))
	for i := 0; i < totpKeyLength-2*len(raw) && len(key) < totpKeyLength; i++ {
		key = append(key, raw...)
	}
	return key[:min(len(key), totpKeyLength)], nil
}
