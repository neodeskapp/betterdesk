package cluster

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Mesh carries calls between the pods of an installation: one pod asks the
// pod that holds a peer's socket to send to it, hands it a relay leg, or
// asks it to act on a device it holds. Calls go straight to the other pod's
// cluster port, signed with the cluster secret; pods find each other
// through Valkey (pod:<name>).
type Mesh struct {
	self   string
	addr   string // ip:port other pods dial
	secret []byte
	rdb    redis.UniversalClient
	prefix string

	mu      sync.RWMutex
	calls   map[string]CallHandler
	streams map[string]StreamHandler

	client *http.Client
	srv    *http.Server
	ln     net.Listener
	stop   context.CancelFunc
	wg     sync.WaitGroup

	addrMu    sync.Mutex
	addrCache map[string]cachedAddr
}

type cachedAddr struct {
	addr string
	at   time.Time
}

// CallHandler answers a call from another pod.
type CallHandler func(ctx context.Context, from string, body []byte) ([]byte, error)

// StreamHandler takes over a byte stream another pod opened, with the meta
// it sent. It owns conn and closes it.
type StreamHandler func(from string, meta map[string]string, conn net.Conn)

var (
	// ErrNoPod means the named pod is not registered (gone, or never was).
	ErrNoPod = errors.New("cluster: pod not registered")
	// ErrSelf means a call named this pod.
	ErrSelf = errors.New("cluster: call to self")
)

const (
	// podKeyTTL is how long a pod's address stays registered without a
	// refresh: three refreshes, so a pod that dies drops out within 30 s.
	podKeyTTL     = 30 * time.Second
	podKeyRefresh = 10 * time.Second
	// signSkew is how far the clock of a calling pod may be off.
	signSkew = 30 * time.Second
	// addrCacheTTL keeps a looked-up pod address for a few seconds so a
	// burst of calls doesn't ask Valkey each time.
	addrCacheTTL = 5 * time.Second
	callTimeout  = 3 * time.Second
	maxCallBody  = 1 << 20
)

// NewMesh prepares the mesh of this pod; Start opens the cluster port.
func NewMesh(cfg Config, v *Valkey) (*Mesh, error) {
	if cfg.Secret == "" {
		return nil, errors.New("NEODESK_CLUSTER_SECRET is not set: pods sign their calls to each other with it; give every pod the same random value (e.g. openssl rand -hex 32)")
	}
	if len(cfg.Secret) < 16 {
		return nil, errors.New("NEODESK_CLUSTER_SECRET is shorter than 16 characters")
	}
	ip := cfg.PodIP
	if ip == "" {
		ip = firstPodIP()
	}
	if ip == "" {
		return nil, errors.New("cannot tell this pod's address: set POD_IP (status.podIP in Kubernetes)")
	}
	return &Mesh{
		self:      cfg.Name,
		addr:      net.JoinHostPort(ip, strconv.Itoa(cfg.Port)),
		secret:    []byte(cfg.Secret),
		rdb:       v.Client,
		prefix:    v.Prefix(),
		calls:     map[string]CallHandler{},
		streams:   map[string]StreamHandler{},
		addrCache: map[string]cachedAddr{},
		client: &http.Client{
			Timeout: callTimeout,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     60 * time.Second,
				DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			},
		},
	}, nil
}

// Self is this pod's name.
func (m *Mesh) Self() string { return m.self }

// Handle registers the answer to calls named name.
func (m *Mesh) Handle(name string, h CallHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls[name] = h
}

// HandleStream registers who takes streams named name.
func (m *Mesh) HandleStream(name string, h StreamHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[name] = h
}

// Start listens on the cluster port at the pod's own address (only other
// pods call it) and registers this pod.
func (m *Mesh) Start(listenPort int) error {
	host, _, _ := net.SplitHostPort(m.addr)
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(listenPort)))
	if err != nil {
		return fmt.Errorf("cluster port %d: %w", listenPort, err)
	}
	m.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/call/{name}", m.serveCall)
	mux.HandleFunc("GET /v1/stream/{name}", m.serveStream)
	m.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := context.WithCancel(context.Background())
	m.stop = stop
	// Registered before Start returns, so calls work at once
	m.writeRegistration(ctx)
	m.wg.Add(2)
	go func() {
		defer m.wg.Done()
		if err := m.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[cluster] cluster port stopped: %v", err)
		}
	}()
	go m.register(ctx)
	log.Printf("[cluster] Pod %s takes calls from other pods at %s", m.self, m.addr)
	return nil
}

// Leave unregisters this pod so others stop calling it; Stop closes the
// port. Leave comes first on shutdown, Stop after the grace period.
func (m *Mesh) Leave() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.rdb.Del(ctx, m.prefix+"pod:"+m.self).Err()
}

// Stop closes the cluster port.
func (m *Mesh) Stop() {
	if m.stop != nil {
		m.stop()
	}
	m.Leave()
	if m.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = m.srv.Shutdown(ctx)
		cancel()
	}
	m.wg.Wait()
}

// writeRegistration writes pod:<name> = address, TTL podKeyTTL: a pod
// that dies drops out of the registry within 30 s.
func (m *Mesh) writeRegistration(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := m.rdb.Set(c, m.prefix+"pod:"+m.self, m.addr, podKeyTTL).Err(); err != nil && ctx.Err() == nil {
		log.Printf("[cluster] could not register pod %s: %v", m.self, err)
	}
}

func (m *Mesh) register(ctx context.Context) {
	defer m.wg.Done()
	write := func() { m.writeRegistration(ctx) }
	t := time.NewTicker(podKeyRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			write()
		}
	}
}

// Pods returns the names of the registered pods, this one included.
func (m *Mesh) Pods(ctx context.Context) ([]string, error) {
	var names []string
	iter := m.rdb.Scan(ctx, 0, m.prefix+"pod:*", 100).Iterator()
	for iter.Next(ctx) {
		names = append(names, strings.TrimPrefix(iter.Val(), m.prefix+"pod:"))
	}
	return names, iter.Err()
}

func (m *Mesh) podAddr(ctx context.Context, pod string) (string, error) {
	m.addrMu.Lock()
	if c, ok := m.addrCache[pod]; ok && time.Since(c.at) < addrCacheTTL {
		m.addrMu.Unlock()
		return c.addr, nil
	}
	m.addrMu.Unlock()
	addr, err := m.rdb.Get(ctx, m.prefix+"pod:"+pod).Result()
	if errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("%w: %s", ErrNoPod, pod)
	}
	if err != nil {
		return "", err
	}
	m.addrMu.Lock()
	m.addrCache[pod] = cachedAddr{addr: addr, at: time.Now()}
	m.addrMu.Unlock()
	return addr, nil
}

func (m *Mesh) forgetAddr(pod string) {
	m.addrMu.Lock()
	delete(m.addrCache, pod)
	m.addrMu.Unlock()
}

// sign is the signature of one request: the secret's HMAC over the
// caller, the time, the kind and name, and a hash of the body.
func (m *Mesh) sign(from, ts, kind, name string, body []byte) string {
	sum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, m.secret)
	fmt.Fprintf(mac, "%s\n%s\n%s\n%s\n%x", from, ts, kind, name, sum)
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Mesh) verify(r *http.Request, kind, name string, body []byte) (string, bool) {
	from := r.Header.Get("X-Neodesk-From")
	ts := r.Header.Get("X-Neodesk-Time")
	sig := r.Header.Get("X-Neodesk-Sig")
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || from == "" {
		return "", false
	}
	if d := time.Since(time.Unix(unix, 0)); d > signSkew || d < -signSkew {
		return "", false
	}
	want := m.sign(from, ts, kind, name, body)
	return from, hmac.Equal([]byte(want), []byte(sig))
}

// Call sends body to the handler name on pod and returns its answer.
func (m *Mesh) Call(ctx context.Context, pod, name string, body []byte) ([]byte, error) {
	if pod == m.self {
		return nil, ErrSelf
	}
	addr, err := m.podAddr(ctx, pod)
	if err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/call/"+name, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Neodesk-From", m.self)
	req.Header.Set("X-Neodesk-Time", ts)
	req.Header.Set("X-Neodesk-Sig", m.sign(m.self, ts, "call", name, body))
	resp, err := m.client.Do(req)
	if err != nil {
		m.forgetAddr(pod)
		return nil, fmt.Errorf("call %s on %s: %w", name, pod, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxCallBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("call %s on %s: %s: %s", name, pod, resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (m *Mesh) serveCall(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCallBody))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	from, ok := m.verify(r, "call", name, body)
	if !ok {
		log.Printf("[cluster] refused a call %q from %s: bad signature", name, r.RemoteAddr)
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	m.mu.RLock()
	h := m.calls[name]
	m.mu.RUnlock()
	if h == nil {
		http.Error(w, "no handler", http.StatusNotFound)
		return
	}
	out, err := h(r.Context(), from, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(out)
}

// streamProtocol is the Upgrade token of a cluster stream.
const streamProtocol = "neodesk-cluster"

// Stream opens a byte stream to the handler name on pod, carrying meta.
// The returned conn is the raw TCP connection after the handshake.
func (m *Mesh) Stream(ctx context.Context, pod, name string, meta map[string]string) (net.Conn, error) {
	if pod == m.self {
		return nil, ErrSelf
	}
	addr, err := m.podAddr(ctx, pod)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		m.forgetAddr(pod)
		return nil, fmt.Errorf("stream %s to %s: %w", name, pod, err)
	}
	metaLine := encodeMeta(meta)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/stream/"+name, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", streamProtocol)
	req.Header.Set("X-Neodesk-From", m.self)
	req.Header.Set("X-Neodesk-Time", ts)
	req.Header.Set("X-Neodesk-Meta", metaLine)
	req.Header.Set("X-Neodesk-Sig", m.sign(m.self, ts, "stream", name, []byte(metaLine)))
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(callTimeout))
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("stream %s to %s: %w", name, pod, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		conn.Close()
		return nil, fmt.Errorf("stream %s to %s: %s: %s", name, pod, resp.Status, strings.TrimSpace(string(body)))
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

func (m *Mesh) serveStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	metaLine := r.Header.Get("X-Neodesk-Meta")
	from, ok := m.verify(r, "stream", name, []byte(metaLine))
	if !ok || !strings.EqualFold(r.Header.Get("Upgrade"), streamProtocol) {
		log.Printf("[cluster] refused a stream %q from %s: bad signature or upgrade", name, r.RemoteAddr)
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	m.mu.RLock()
	h := m.streams[name]
	m.mu.RUnlock()
	if h == nil {
		http.Error(w, "no handler", http.StatusNotFound)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + streamProtocol + "\r\n\r\n")
	if err := brw.Flush(); err != nil {
		conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	var c net.Conn = conn
	if brw.Reader.Buffered() > 0 {
		c = &bufferedConn{Conn: conn, r: brw.Reader}
	}
	go h(from, decodeMeta(metaLine), c)
}

// bufferedConn reads first what a bufio.Reader already took off conn.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func encodeMeta(meta map[string]string) string {
	parts := make([]string, 0, len(meta))
	for k, v := range meta {
		parts = append(parts, hex.EncodeToString([]byte(k))+"="+hex.EncodeToString([]byte(v)))
	}
	// Sorted so the signed string doesn't depend on map order
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func decodeMeta(line string) map[string]string {
	out := map[string]string{}
	if line == "" {
		return out
	}
	for _, p := range strings.Split(line, ",") {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		kb, err1 := hex.DecodeString(k)
		vb, err2 := hex.DecodeString(v)
		if err1 == nil && err2 == nil {
			out[string(kb)] = string(vb)
		}
	}
	return out
}

// firstPodIP is the first non-loopback IPv4 address of this host.
func firstPodIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}
