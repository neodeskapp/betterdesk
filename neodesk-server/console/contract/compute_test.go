package contract

import (
	"strings"
	"testing"
	"time"
)

// TestTOTPCodes checks the codes against what the console computes: the
// vectors are taken from console/authn/testdata/vectors/totp.json, which
// the Node console's own code wrote (at is in milliseconds).
func TestTOTPCodes(t *testing.T) {
	for _, v := range []struct {
		secret string
		at     int64
		code   string
	}{
		{"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", 0, "755224"},
		{"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", 30000, "287082"},
		{"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", 1687003859999, "951937"},
		{"CQKGOBVGHG7EVLBA64EO2UDL7FIGSLJI", 0, "589822"},
		{"DIXCHCKLUMIBYPE4FXFAJG4IDEP35AZS", 29999, "714917"},
		{"DIXCHCKLUMIBYPE4FXFAJG4IDEP35AZS", 1985950799999, "523139"},
		{"4O3ASX5RDVYMUII5KCI5JG2APRRTU67M", 0, "021685"},
		{"FVIJKDP5QRJGXQVQBMTF5BL745W3SFRM", 1743123060001, "204573"},
		{"AO7ZLCXV3RZNLKNXSIB3YCUYKAF5HBQR", 2007733019999, "401908"},
		{"NO3HKXHPM37AP5ZGAXDPWQJX7SM22LVV", 1900621350000, "481569"},
		// Ten bytes: the key as it is
		{"AAAAAAAAAAAAAAAA", 1654659420001, "052819"},
		// Five bytes: repeated to the key the console makes
		{"GEZDGNBV", 1, "861883"},
		// Lower case and padding read as the console reads them
		{"gezdgnbvgy3tqojqgezdgnbvgy3tqojq", 30000, "287082"},
		{"GEZDGNBV========", 1, "861883"},
	} {
		got, err := totpCode(v.secret, totpStepAt(time.UnixMilli(v.at)))
		if err != nil || got != v.code {
			t.Errorf("totpCode(%s, at %d) = %q, %v; the console computes %q", v.secret, v.at, got, err, v.code)
		}
	}
	for _, bad := range []string{"", "not base32!", "1234"} {
		if _, err := totpCode(bad, 1); err == nil {
			t.Errorf("totpCode(%q) gave a code", bad)
		}
	}
}

// fakeClock is a clock a test moves: Sleep moves it on.
type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
}

const totpTestSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func clockAt(step int64, into time.Duration) *fakeClock {
	return &fakeClock{now: stepStart(step).Add(into)}
}

func codeAt(t *testing.T, step int64) string {
	t.Helper()
	code, err := totpCode(totpTestSecret, step)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestTOTPWaitsWhenTheStepIsAboutToEnd(t *testing.T) {
	const step = 58000000
	for _, c := range []struct {
		into     time.Duration
		wantStep int64
		waits    bool
	}{
		{10 * time.Second, step, false},
		{26*time.Second + 900*time.Millisecond, step, false},
		{27*time.Second + 100*time.Millisecond, step + 1, true},
		{29*time.Second + 999*time.Millisecond, step + 1, true},
	} {
		clock := clockAt(step, c.into)
		tn := newTurn(nil)
		tn.clock = clock
		tn.secrets["totp"] = totpTestSecret
		code, err := totpNow(tn, computeArg{name: "totp", secret: true})
		if err != nil {
			t.Fatal(err)
		}
		if code != codeAt(t, c.wantStep) || (len(clock.slept) > 0) != c.waits {
			t.Errorf("%s into the step: code of step %+d, slept %v; want the code of step %+d, waiting %v",
				c.into, stepOf(t, code, step), clock.slept, c.wantStep-step, c.waits)
		}
		if c.waits && !clock.now.After(stepStart(c.wantStep)) {
			t.Errorf("%s into the step: the wait ended at %v, before step %d began", c.into, clock.now, c.wantStep)
		}
	}
}

// stepOf finds which step near base a code belongs to, for a message.
func stepOf(t *testing.T, code string, base int64) int64 {
	for d := int64(-2); d <= 3; d++ {
		if codeAt(t, base+d) == code {
			return d
		}
	}
	return 99
}

func TestTOTPNextGivesACodeOfALaterStep(t *testing.T) {
	const step = 58000000
	clock := clockAt(step, 5*time.Second)
	tn := newTurn(nil)
	tn.clock = clock
	tn.vars["plain"] = totpTestSecret
	arg := computeArg{name: "plain"}

	first, _ := totpNow(tn, arg)
	second, _ := totpNext(tn, arg)
	third, _ := totpNext(tn, arg)
	if first != codeAt(t, step) || second != codeAt(t, step+1) || third != codeAt(t, step+2) || len(clock.slept) != 2 {
		t.Errorf("codes of steps %+d, %+d, %+d after %v; want 0, +1, +2 with two waits",
			stepOf(t, first, step), stepOf(t, second, step), stepOf(t, third, step), clock.slept)
	}

	// Long after the last code there is nothing to wait for
	clock.now = stepStart(step + 10).Add(4 * time.Second)
	clock.slept = nil
	if code, _ := totpNext(tn, arg); code != codeAt(t, step+10) || len(clock.slept) != 0 {
		t.Errorf("code of step %+d, slept %v; want the current step without a wait", stepOf(t, code, step+10)+10, clock.slept)
	}
	// Without an earlier code: the next step
	fresh := newTurn(nil)
	fresh.clock = clockAt(step, 5*time.Second)
	fresh.vars["plain"] = totpTestSecret
	if code, _ := totpNext(fresh, arg); code != codeAt(t, step+1) {
		t.Errorf("a first totp_next gave the code of step %+d, want +1", stepOf(t, code, step))
	}

	missing := newTurn(nil)
	if _, err := totpNow(missing, computeArg{name: "nothing"}); err == nil {
		t.Error("totp of a variable nothing captured gave a code")
	}
	missing.vars["n"] = int64(5)
	if _, err := totpNow(missing, computeArg{name: "n"}); err == nil {
		t.Error("totp of a number gave a code")
	}
	missing.secrets["bad"] = "not base32 at all"
	if _, err := totpNow(missing, computeArg{name: "bad", secret: true}); err == nil || strings.Contains(err.Error(), "not base32 at all") {
		t.Errorf("totp of a secret that is no base32: %v; want an error without the value", err)
	}
}

func TestComputeInACase(t *testing.T) {
	tn := newTurn(nil)
	tn.clock = clockAt(58000000, 5*time.Second)
	tn.secrets["totp"] = totpTestSecret
	c := Case{
		Name: "x", Method: "POST", Path: "/api/auth/totp/enable",
		Compute: map[string]string{"code": "totp(secret:totp)"},
		Body:    map[string]any{"code": "{code}"},
	}
	if err := c.compute(tn); err != nil {
		t.Fatal(err)
	}
	r, err := c.request(tn)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"code":"` + codeAt(t, 58000000) + `"}`; string(r.Body) != want {
		t.Errorf("body = %s, want %s: the code goes out as text", r.Body, want)
	}
	if notes := c.info().Notes; len(notes) != 1 || notes[0] != "computed: code = totp(secret:totp)" {
		t.Errorf("notes = %q", notes)
	}
}

func TestComputeIsCheckedWhenTheGroupIsRegistered(t *testing.T) {
	bad := map[string]map[string]string{
		"unknown function":     {"code": "hotp(secret:x)"},
		"is not function":      {"code": "totp(secret:x"},
		"functions that wait":  {"a": "totp_next(x)", "b": "totp_next(y)"},
		"computed by the same": {"a": "totp(x)", "x": "totp(secret:y)"},
		"a variable name":      {"a-b": "totp(x)"},
	}
	for want, compute := range bad {
		withGroups(func() {
			text := panicText(func() { Group("a", Case{Name: "x", Path: "/x", Compute: compute}) })
			if !strings.Contains(text, want) {
				t.Errorf("%v: panic %q, want one that says %q", compute, text, want)
			}
		})
	}
	list, err := parseCompute(map[string]string{"b": "totp(secret:x)", "z": "totp_next(x)", "a": "totp(y)"})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].target != "z" || list[1].target != "a" || list[2].target != "b" {
		t.Errorf("order %v: the function that waits runs first, then the others by name", list)
	}
}
