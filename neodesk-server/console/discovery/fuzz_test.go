package discovery

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// referenceProbe decides with encoding/json, which agrees with JSON.parse
// on the grammar up to its nesting limit of 10,000.
func referenceProbe(msg []byte) (answer, usable bool) {
	text := []byte(decodeUTF8(msg))
	if bytes.Count(text, []byte("["))+bytes.Count(text, []byte("{")) >= 9000 {
		return false, false
	}
	if !json.Valid(text) {
		return false, true
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(text, &top); err != nil {
		return false, true
	}
	if top == nil {
		return false, true
	}
	raw, ok := top["type"]
	if !ok {
		return false, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, true
	}
	return value == upstreamProbe, true
}

// FuzzIsProbe checks the probe parser against encoding/json on any input.
func FuzzIsProbe(f *testing.F) {
	for i, c := range loadLAN(f).Probes {
		if i%7 == 0 {
			f.Add(decode64(f, c.Probe))
		}
	}
	f.Fuzz(func(t *testing.T, msg []byte) {
		got := isProbeOf(msg, upstreamProbe)
		if want, usable := referenceProbe(msg); usable && got != want {
			t.Errorf("isProbeOf(%q) = %v, encoding/json says %v", msg, got, want)
		}
	})
}

// FuzzDecodeMessage feeds any datagram to the mDNS reader and responder:
// neither may panic, a decoded name is valid text, and every answer the
// responder gives decodes again as a response.
func FuzzDecodeMessage(f *testing.F) {
	for _, a := range loadMDNS(f).Answers {
		for i, p := range a.Packets {
			if i%5 == 0 {
				f.Add(decode64(f, p.Packet))
			}
		}
	}
	settings := Settings{
		Hostname: func() (string, error) { return "fuzz-host", nil },
		Interfaces: func() ([]Interface, error) {
			return []Interface{{Name: "eth0", Entries: []Address{{Family: "IPv4", Address: "10.0.0.1", MAC: "02:00:00:00:00:01"}}}}, nil
		},
		Host:    "10.0.0.1",
		Port:    5000,
		Version: "1.0",
	}
	m := newRecordedMDNS(settings)
	m.logf = func(string, ...any) {}
	svc, ok := m.prepare()
	if !ok {
		f.Fatal("no service")
	}
	ifaces, _ := settings.interfaces()
	reg := newRegistry()
	reg.register(svc.records(ifaces))
	f.Fuzz(func(t *testing.T, datagram []byte) {
		if msg, err := decodeMessage(datagram); err == nil {
			for _, q := range msg.Questions {
				if !utf8.ValidString(q.Name) {
					t.Errorf("question name %q is no valid text", q.Name)
				}
			}
		}
		for _, answer := range reg.respond(datagram) {
			back, err := decodeMessage(answer)
			if err != nil || !back.Response {
				t.Errorf("answer %x to %x does not decode as a response: %v", answer, datagram, err)
			}
		}
	})
}
