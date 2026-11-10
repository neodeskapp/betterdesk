package avatars

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

// The limits of a picture fetch.
const (
	// FetchTimeout is the time for the whole fetch, redirects included.
	FetchTimeout = 5 * time.Second
	// MaxRedirects is the number of redirects followed.
	MaxRedirects = 3
)

// FetchOptions are the limits and the network access of a picture fetch.
// The zero value is what the console uses.
type FetchOptions struct {
	// MaxBytes is the size limit, MaxAvatarBytes when zero.
	MaxBytes int
	// Timeout is the time for the whole fetch, FetchTimeout when zero.
	Timeout time.Duration
	// MaxRedirects is the number of redirects followed. Nil means
	// MaxRedirects; a pointer to zero follows none.
	MaxRedirects *int
	// Lookup resolves a host name. Nil means the system's resolver.
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// Dial opens the connection to a checked address. Nil means a plain
	// net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Transport sends the requests in place of the console's own
	// transport, which checks every address before it connects. Tests set
	// it; with it Lookup and Dial are not used.
	Transport http.RoundTripper
}

func (o FetchOptions) maxBytes() int {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return MaxAvatarBytes
}

// guardedDial connects to a host for a picture fetch. A name is resolved
// first, and the fetch is refused when any of its addresses is a refused
// one; the connection then goes to the addresses that were checked, so a
// name that changes its address between check and connect cannot reach a
// refused address.
func (o FetchOptions) guardedDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{literal}
	} else {
		lookup := o.Lookup
		if lookup == nil {
			lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
				return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			}
		}
		if addrs, err = lookup(ctx, host); err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, refuse("dns_failed", "No address for "+host)
		}
	}
	for _, addr := range addrs {
		if refusedAddr(addr) {
			return nil, refuse("address_refused", host+" resolves to refused address "+addr.String())
		}
	}
	dial := o.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// transport is the HTTP transport of one fetch: a new connection for
// every request, no proxy, no compression asked for, every address
// checked by guardedDial.
func (o FetchOptions) transport() http.RoundTripper {
	if o.Transport != nil {
		return o.Transport
	}
	return &http.Transport{
		DialContext:        o.guardedDial,
		DisableKeepAlives:  true,
		DisableCompression: true,
	}
}

// FetchURL fetches a picture from an http or https address:
//
//   - no credentials in the address, no refused host or address, also
//     after a redirect and after the name was resolved
//   - 5 seconds for everything, at most 3 redirects, at most 512 KB
//   - the answer must be a 200 whose Content-Type is PNG, JPEG, WebP or
//     GIF and whose first bytes are of that type
func FetchURL(ctx context.Context, address string, opts FetchOptions) (Image, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = FetchTimeout
	}
	maxRedirects := MaxRedirects
	if opts.MaxRedirects != nil {
		maxRedirects = *opts.MaxRedirects
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := opts.transport()
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}

	current, err := checkURL(address)
	if err != nil {
		return Image{}, err
	}
	for redirects := 0; ; redirects++ {
		img, location, err := requestOnce(ctx, transport, current, opts.maxBytes())
		if err != nil {
			return Image{}, err
		}
		if location == "" {
			return img, nil
		}
		if redirects >= maxRedirects {
			return Image{}, refuse("too_many_redirects", "Too many redirects")
		}
		if current, err = checkURL(current.resolve(location)); err != nil {
			return Image{}, err
		}
	}
}

// requestOnce sends one GET. It returns the picture, or the Location of a
// redirect.
func requestOnce(ctx context.Context, transport http.RoundTripper, t target, maxBytes int) (Image, string, error) {
	fail := func(err error) (Image, string, error) {
		var refusal *Error
		switch {
		case errors.As(err, &refusal):
			return Image{}, "", refusal
		case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
			return Image{}, "", refuse("timeout", "Picture fetch timed out")
		}
		return Image{}, "", refuse("fetch_failed", err.Error())
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.scheme+"://"+t.url().Host+"/", nil)
	if err != nil {
		return fail(err)
	}
	req.URL = t.url()
	req.Header.Set("Accept", "image/png,image/jpeg,image/webp,image/gif;q=0.9")
	req.Header.Set("User-Agent", "BetterDesk-Console/avatar")
	res, err := transport.RoundTrip(req)
	if err != nil {
		return fail(err)
	}
	defer res.Body.Close()

	if location := res.Header.Get("Location"); res.StatusCode >= 300 && res.StatusCode < 400 && location != "" {
		return Image{}, location, nil
	}
	if res.StatusCode != http.StatusOK {
		return Image{}, "", refuse("http_status", "Picture URL answered "+strconv.Itoa(res.StatusCode))
	}
	declared := normalizeMime(res.Header.Get("Content-Type"))
	if !allowedType(declared) {
		if declared == "" {
			declared = "none"
		}
		return Image{}, "", refuse("not_an_image", "Content type "+declared+" is not an allowed image type")
	}
	if res.ContentLength > int64(maxBytes) {
		return Image{}, "", refuse("too_large", "Picture is too large")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, int64(maxBytes)+1))
	if len(data) > maxBytes {
		return Image{}, "", refuse("too_large", "Picture is too large")
	}
	if err != nil {
		return fail(err)
	}
	sniffed := SniffImageType(data)
	if sniffed == "" {
		return Image{}, "", refuse("not_an_image", "The response is not a PNG, JPEG, WebP or GIF image")
	}
	if sniffed != declared {
		return Image{}, "", refuse("type_mismatch", "Declared "+declared+" but the data is "+sniffed)
	}
	return Image{MimeType: sniffed, Data: data}, "", nil
}

// Picture is a picture ready to be stored.
type Picture struct {
	Image
	// Version is ImageVersion of the bytes.
	Version string
	// SourceURL is the address the picture was fetched from, cut to 2048
	// characters, and empty for a data URL.
	SourceURL string
}

// LoadPicture turns a picture value, a data:image URL or an http(s)
// address, into checked bytes.
func LoadPicture(ctx context.Context, picture string, opts FetchOptions) (Picture, error) {
	value := jsTrim(picture)
	if value == "" {
		return Picture{}, refuse("empty", "No picture")
	}
	var (
		img       Image
		sourceURL string
		err       error
	)
	switch {
	case hasPrefixFold(value, "data:"):
		img, err = ParseDataURL(value, opts.MaxBytes)
	case hasPrefixFold(value, "http://"), hasPrefixFold(value, "https://"):
		img, err = FetchURL(ctx, value, opts)
		sourceURL = sliceUnits(value, 2048)
	default:
		err = invalidURL("Picture is neither an http(s) URL nor a data:image URL")
	}
	if err != nil {
		return Picture{}, err
	}
	return Picture{Image: img, Version: ImageVersion(img.Data), SourceURL: sourceURL}, nil
}

// hasPrefixFold reports whether s starts with prefix, which is in lower
// case, in any case of the letters A to Z.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && asciiLower(s[:len(prefix)]) == prefix
}

// errorCode is the code of a failure for an answer: the Code of an Error,
// and "error" for anything else.
func errorCode(err error) string {
	var refusal *Error
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return "error"
}
