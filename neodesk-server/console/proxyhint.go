package console

import (
	"net/http"
	"sync/atomic"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// untrustedProxyHint logs once when requests carry X-Forwarded-For or
// X-Forwarded-Proto from a hop TRUST_PROXY does not trust: the console is
// then behind a proxy it ignores, so its cookies are not Secure and the
// activity log shows the proxy's address.
type untrustedProxyHint struct{ logged atomic.Bool }

func (h *untrustedProxyHint) note(r *http.Request, trust httpx.TrustProxy) {
	if h.logged.Load() {
		return
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" && r.Header.Get("X-Forwarded-For") == "" {
		return
	}
	if trust.TrustsSocket(r) || !h.logged.CompareAndSwap(false, true) {
		return
	}
	what := "X-Forwarded-For"
	if proto != "" {
		what = "X-Forwarded-Proto: " + proto
	}
	logging.Logf("console", "WARN: requests from %s carry %s, but TRUST_PROXY does not trust that address: the console's cookies are not Secure and the activity log shows the proxy's address. Behind a reverse proxy, set TRUST_PROXY (for example TRUST_PROXY=1, or the proxy's address)", httpx.SocketIP(r), what)
}
