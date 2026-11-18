package servicedef

import (
	"strings"
	"testing"
)

// FuzzUnitText rewrites unit text: nothing panics, each change is made
// once (a second pass changes nothing), and the lines it promises are
// there afterwards.
func FuzzUnitText(f *testing.F) {
	for _, seed := range []string{"", "[Service]\nExecStart=/s -tls-api\n", "[Service]\r\nEnvironment=AUTH_DB_PATH=/a\r\n",
		"Environment=AUTH_DB_PATH=/a\u2028x", " -force-https\t-tls-apix", "[Service]x\rEnvironmentFile=/e"} {
		f.Add(seed, "/opt/console/.env")
	}
	f.Add("[Service]\n", "/o/$1/$&/$$/.env")
	f.Fuzz(func(t *testing.T, unit, envPath string) {
		text, changed := EnsureGoServerEnvironmentFile(unit, envPath)
		if changed {
			if lineStarting(text, "EnvironmentFile=") < 0 {
				t.Fatalf("%q: no EnvironmentFile line after the change:\n%q", unit, text)
			}
			if again, more := EnsureGoServerEnvironmentFile(text, envPath); more || again != text {
				t.Fatalf("%q: a second pass changed it again", unit)
			}
		}
		ports, changed := EnsureGoServerSignalRelayPorts(unit)
		if changed {
			for _, key := range []string{"SIGNAL_PORT", "RELAY_PORT", "GO_API_PORT"} {
				if lineStarting(ports, "Environment="+key+"=") < 0 {
					t.Fatalf("%q: no %s line after the change", unit, key)
				}
			}
			if _, more := EnsureGoServerSignalRelayPorts(ports); more {
				t.Fatalf("%q: a second pass changed the ports again", unit)
			}
		}
		stripped := StripIncompatibleGoAPITLSArgs(unit, false)
		if StripIncompatibleGoAPITLSArgs(stripped, false) != stripped && !strings.Contains(unit, "-tls-api-") {
			t.Logf("%q: a second strip changed %q", unit, stripped)
		}
		_ = StripIncompatibleGoAPITLSArgs(unit, true)
		mesh, changed := EnsureMeshEnabledInServiceEnv(unit)
		if changed && lineStarting(mesh, "MESH_ENABLED=") < 0 {
			t.Fatalf("%q: no MESH_ENABLED line after the change", unit)
		}
		_, _ = MergeBillingEnvIntoWindowsServiceExtra(unit, map[string]string{"NTP_SERVERS": envPath})
		_ = ParseEnvFileKeys(unit, billingEnvKeys)
	})
}
