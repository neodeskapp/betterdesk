package envvar

import "testing"

func TestSwitchTakesEveryOnOffValueInAnyCase(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "True", "yes", "YES", "Yes", "y", "Y", "on", "ON", "On", " y ", "\ttrue\n"} {
		if on, ok := Switch(value); !on || !ok {
			t.Errorf("Switch(%q) = %v, %v; want on", value, on, ok)
		}
	}
	for _, value := range []string{"0", "false", "FALSE", "False", "no", "NO", "No", "n", "N", "off", "OFF", "Off", " n ", "\toff\n"} {
		if on, ok := Switch(value); on || !ok {
			t.Errorf("Switch(%q) = %v, %v; want off", value, on, ok)
		}
	}
	for _, value := range []string{"", " ", "2", "-1", "t", "f", "enabled", "yess", "o"} {
		if on, ok := Switch(value); on || ok {
			t.Errorf("Switch(%q) = %v, %v; want no on/off value", value, on, ok)
		}
	}
}

func TestSwitchOrFallsBackOnlyForOtherValues(t *testing.T) {
	cases := []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"", true, true},
		{"", false, false},
		{"maybe", true, true},
		{"maybe", false, false},
		{"off", true, false},
		{"ON", false, true},
	}
	for _, c := range cases {
		if got := SwitchOr(c.value, c.fallback); got != c.want {
			t.Errorf("SwitchOr(%q, %v) = %v, want %v", c.value, c.fallback, got, c.want)
		}
	}
}
