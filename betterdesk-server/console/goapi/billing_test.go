package goapi

import (
	"context"
	"testing"
)

// TestGetBillingSession reads one session with its device, gives nil and
// no error for a session the API server does not know, and an error for
// any other failure.
func TestGetBillingSession(t *testing.T) {
	ctx := context.Background()
	api := &fakeAPI{answer: answerJSON(200, `{"session":{"id":"bs 1","device_id":"PEER1"},"device":null}`)}
	got, err := New(api).GetBillingSession(ctx, "bs 1")
	if err != nil || got == nil || got.DeviceID() != "PEER1" || got.Device != nil {
		t.Fatalf("GetBillingSession = %+v, %v; want the session of PEER1 without a device", got, err)
	}
	if len(api.requests) != 1 || api.requests[0] != "GET /api/billing/sessions/bs%201" {
		t.Errorf("requests = %q", api.requests)
	}

	got, err = New(&fakeAPI{answer: answerJSON(404, `{"error":"billing session not found"}`)}).GetBillingSession(ctx, "nosuch")
	if got != nil || err != nil {
		t.Errorf("unknown session = %+v, %v; want nil, nil", got, err)
	}
	for name, api := range map[string]*fakeAPI{
		"500":        {answer: answerJSON(500, `{"error":"boom"}`)},
		"no answer":  {answer: noAnswer},
		"no session": {answer: answerJSON(200, `{"device":null}`)},
	} {
		if got, err := New(api).GetBillingSession(ctx, "bs1"); got != nil || err == nil {
			t.Errorf("%s: %+v, %v; want an error", name, got, err)
		}
	}
}
