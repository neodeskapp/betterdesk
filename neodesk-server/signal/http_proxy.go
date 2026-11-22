package signal

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/codec"
	pb "github.com/unitronix/betterdesk-server/proto"
)

// A client whose API server setting is empty sends its API calls through
// the signal connection as HttpProxyRequest (RustDesk's TCP proxy). The
// signal server answers those for this server's own API only: a path
// under /api/ goes to the handler of the API port in this process, with
// the client's address as RemoteAddr, so its rate limits and checks apply.
// Nothing is sent to another host.

const (
	// httpProxyTimeout bounds one proxied request.
	httpProxyTimeout = 30 * time.Second
	// httpProxyMaxBody is the largest answer body that fits one signal
	// frame together with its status and headers.
	httpProxyMaxBody = codec.MaxFrameSize - 4*1024
)

// httpProxyMethods are the methods a proxied request may use.
var httpProxyMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// httpProxyDroppedHeaders are request headers the client does not set:
// the connection's own and those that would name another address.
var httpProxyDroppedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Keep-Alive": true, "Proxy-Connection": true,
	"Forwarded": true, "X-Forwarded-For": true, "X-Forwarded-Host": true,
	"X-Forwarded-Proto": true, "X-Forwarded-Port": true, "X-Real-Ip": true,
}

// SetAPIProxy sets the handler that answers HttpProxyRequest: the API
// port's handler in this process. nil answers every request with "not
// supported".
func (s *Server) SetAPIProxy(h http.Handler) {
	if h == nil {
		s.apiProxy.Store(nil)
		return
	}
	s.apiProxy.Store(&h)
}

// handleHTTPProxyRequest answers one HttpProxyRequest from raddr.
func (s *Server) handleHTTPProxyRequest(req *pb.HttpProxyRequest, raddr net.Addr) *pb.RendezvousMessage {
	answer := func(r *pb.HttpProxyResponse) *pb.RendezvousMessage {
		return &pb.RendezvousMessage{Union: &pb.RendezvousMessage_HttpProxyResponse{HttpProxyResponse: r}}
	}
	h := s.apiProxy.Load()
	if h == nil {
		return answer(&pb.HttpProxyResponse{Error: "not supported"})
	}
	target, ok := httpProxyTarget(req.GetPath())
	if !ok || !httpProxyMethods[req.GetMethod()] || raddr == nil {
		return answer(&pb.HttpProxyResponse{Error: "only this server's /api/ is served"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpProxyTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, req.GetMethod(), "http://localhost"+target, bytes.NewReader(req.GetBody()))
	if err != nil {
		return answer(&pb.HttpProxyResponse{Error: "invalid request"})
	}
	r.RequestURI = target
	r.RemoteAddr = raddr.String()
	for _, e := range req.GetHeaders() {
		name := http.CanonicalHeaderKey(e.GetName())
		if name == "" || httpProxyDroppedHeaders[name] {
			continue
		}
		r.Header.Add(name, e.GetValue())
	}
	w := &proxyRecorder{header: http.Header{}}
	(*h).ServeHTTP(w, r)
	if w.tooLarge {
		return answer(&pb.HttpProxyResponse{Error: "answer too large"})
	}
	out := &pb.HttpProxyResponse{Status: int32(w.statusCode()), Body: w.body.Bytes()}
	for name, values := range w.header {
		for _, v := range values {
			out.Headers = append(out.Headers, &pb.HeaderEntry{Name: name, Value: v})
		}
	}
	return answer(out)
}

// httpProxyTarget is the request target of a proxied path when it names
// a path under /api/ of this server: no scheme or host, no fragment, no
// escaped slash or backslash, and already clean, so no ".." leaves /api/.
func httpProxyTarget(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "/api/") || strings.ContainsAny(raw, "#\\") {
		return "", false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawPath != "" {
		return "", false
	}
	clean := path.Clean(u.Path)
	if strings.HasSuffix(u.Path, "/") {
		clean += "/"
	}
	if clean != u.Path || !strings.HasPrefix(clean, "/api/") {
		return "", false
	}
	return u.RequestURI(), true
}

// proxyRecorder keeps the answer of a proxied request, up to
// httpProxyMaxBody bytes of body.
type proxyRecorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	tooLarge bool
}

func (w *proxyRecorder) Header() http.Header { return w.header }

func (w *proxyRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *proxyRecorder) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.body.Len()+len(b) > httpProxyMaxBody {
		w.tooLarge = true
		return 0, http.ErrContentLength
	}
	return w.body.Write(b)
}

func (w *proxyRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
