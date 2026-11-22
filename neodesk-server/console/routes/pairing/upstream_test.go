package pairing

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

func TestUpstreamErrorWithoutAMessage(t *testing.T) {
	answer := func(result *goapi.Object) string {
		rec := httptest.NewRecorder()
		upstream(rec, httptest.NewRequest(http.MethodGet, "/api/panel/pairing/codes", nil), http.StatusBadGateway, result)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("status %d", rec.Code)
		}
		return rec.Body.String()
	}
	for _, result := range []*goapi.Object{
		goapi.NewObject("success", false),
		goapi.NewObject("success", false, "error", ""),
		goapi.NewObject("success", false, "error", nil),
	} {
		if got := answer(result); got != `{"success":false,"error":"upstream_error"}` {
			t.Errorf("no message: %s", got)
		}
	}
	// A message that is not text is handed on as it is
	if got := answer(goapi.NewObject("success", false, "error", goapi.NewObject("code", float64(7)))); got != `{"success":false,"error":{"code":7}}` {
		t.Errorf("a message object: %s", got)
	}
}
