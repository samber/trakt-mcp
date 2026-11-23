package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/samber/google-contacts-mcp/internal/store"
)

const (
	// defaultFetchTimeout keeps a cold fetch well under the 10 s budget MCP
	// clients give the whole discovery-and-authorize round trip.
	defaultFetchTimeout = 5 * time.Second
	// defaultMaxBytes bounds a metadata document; real ones are under 2 KiB.
	defaultMaxBytes int64 = 64 << 10
	// defaultMaxCache caps Cache-Control so a client's metadata changes are
	// seen within a day.
	defaultMaxCache = 24 * time.Hour
	// defaultCacheTTL applies when the server sends no usable max-age: an hour
	// avoids a refetch on every token refresh without pinning stale data long.
	defaultCacheTTL = time.Hour
	// maxRedirects bounds hops; each hop is re-checked by the SSRF guard.
	maxRedirects = 3
	// maxResponseHeaderBytes bounds headers, which MaxBytes does not cover.
	maxResponseHeaderBytes = 16 << 10
	// schemeHTTPS is the only scheme a metadata document is fetched over.
	schemeHTTPS = "https"
	// mediaTypeJSON is the only accepted document Content-Type.
	mediaTypeJSON = "application/json"
	// authMethodNone is the only token endpoint auth method: clients are public.
	authMethodNone = "none"
	// cacheControlMaxAge is the Cache-Control directive honored for expiry.
	cacheControlMaxAge = "max-age"
)

// ErrForbiddenAddress reports a metadata URL or redirect that resolves to a
// non-public address (SSRF guard).
var ErrForbiddenAddress = errors.New("oauth: client metadata host resolves to a non-public address")

// fetchStatusError reports a metadata document answered with a non-200 status.
type fetchStatusError struct{ status int }

func (e *fetchStatusError) Error() string {
	return fmt.Sprintf("oauth: fetch client metadata: status %d", e.status)
}

// definitive reports a status meaning the document is gone or the request is
// wrong, not a passing outage: retrying later will not bring it back.
func (e *fetchStatusError) definitive() bool {
	return e.status == http.StatusNotFound || e.status == http.StatusGone || e.status == http.StatusBadRequest
}

// nonPublicPrefixes lists ranges netip's Is* predicates do not cover.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT shared address space
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 can map to private IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2002::/16"),       // 6to4 can embed private IPv4
	netip.MustParsePrefix("2001::/32"),       // Teredo can embed private IPv4
	netip.MustParsePrefix("::ffff:0:0:0/96"), // IPv4-translated
}

// CIMDOptions configures a CIMDFetcher. Zero values take the defaults.
type CIMDOptions struct {
	// Timeout bounds the whole fetch, redirects included (default 5 s).
	Timeout time.Duration
	// MaxBytes bounds the document body (default 64 KiB).
	MaxBytes int64
	// MaxCache caps the Cache-Control max-age honored (default 24 h).
	MaxCache time.Duration
	// AllowPrivate disables the SSRF address guard. Tests only.
	AllowPrivate bool
}

// CIMDFetcher fetches and validates Client ID Metadata Documents.
type CIMDFetcher struct {
	opts   CIMDOptions
	client *http.Client
	// allowAddr decides which resolved addresses may be dialed; replaced in tests.
	allowAddr func(netip.Addr) bool
	// now is replaced in tests.
	now func() time.Time
}

// clientMetadataDocument holds the CIMD fields this server reads.
type clientMetadataDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// NewCIMDFetcher returns a fetcher with an SSRF-guarded HTTP client.
func NewCIMDFetcher(opts CIMDOptions) *CIMDFetcher {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultFetchTimeout
	}

	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}

	if opts.MaxCache <= 0 {
		opts.MaxCache = defaultMaxCache
	}

	f := &CIMDFetcher{opts: opts, allowAddr: isPublicAddr, now: time.Now}
	if opts.AllowPrivate {
		f.allowAddr = func(netip.Addr) bool { return true }
	}

	dialer := &net.Dialer{Timeout: opts.Timeout, Control: f.dialControl}
	f.client = &http.Client{
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			// No proxy: a proxy would dial on our behalf and bypass the guard.
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			ForceAttemptHTTP2:      true,
			TLSHandshakeTimeout:    opts.Timeout,
			ResponseHeaderTimeout:  opts.Timeout,
			MaxResponseHeaderBytes: maxResponseHeaderBytes,
		},
		CheckRedirect: f.checkRedirect,
	}

	return f
}

// dialControl runs after DNS resolution, on the exact address about to be
// dialed, so DNS rebinding cannot slip a private address past a name check.
func (f *CIMDFetcher) dialControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparsable address %q", ErrForbiddenAddress, address)
	}

	if !f.allowAddr(ap.Addr().Unmap()) {
		return fmt.Errorf("%w: %s", ErrForbiddenAddress, ap.Addr())
	}

	return nil
}

func (f *CIMDFetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("oauth: client metadata: more than %d redirects", maxRedirects)
	}

	if req.URL.Scheme != schemeHTTPS {
		return fmt.Errorf("oauth: client metadata: redirect to non-https scheme %q", req.URL.Scheme)
	}

	return f.checkLiteralHost(req.URL)
}

// checkLiteralHost rejects IP-literal hosts early; names are checked at dial.
func (f *CIMDFetcher) checkLiteralHost(u *url.URL) error {
	// A parse error means a DNS name, which dialControl checks once resolved.
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && !f.allowAddr(addr.Unmap()) {
		return fmt.Errorf("%w: %s", ErrForbiddenAddress, addr)
	}

	return nil
}

// Fetch downloads and validates the metadata document at clientID. It returns
// the client and when its cached copy expires.
func (f *CIMDFetcher) Fetch(ctx context.Context, clientID string) (*store.OAuthClient, time.Time, error) {
	if err := validateClientIDURL(clientID); err != nil {
		return nil, time.Time{}, err
	}

	u, err := url.Parse(clientID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("oauth: client_id: %w", err)
	}

	if err := f.checkLiteralHost(u); err != nil {
		return nil, time.Time{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("oauth: client metadata: %w", err)
	}

	req.Header.Set("Accept", mediaTypeJSON)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("oauth: fetch client metadata: %w", err)
	}

	defer func() { _ = resp.Body.Close() }() // read-only body; a close error changes nothing

	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, &fetchStatusError{status: resp.StatusCode}
	}

	body, err := readMetadataBody(resp, f.opts.MaxBytes)
	if err != nil {
		return nil, time.Time{}, err
	}

	var doc clientMetadataDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, time.Time{}, fmt.Errorf("oauth: decode client metadata: %w", err)
	}

	if err := validateDocument(clientID, &doc); err != nil {
		return nil, time.Time{}, err
	}

	name := strings.TrimSpace(doc.ClientName)
	if name == "" {
		name = u.Hostname()
	}

	expires := f.now().Add(cacheTTL(resp.Header.Get("Cache-Control"), f.opts.MaxCache))

	return &store.OAuthClient{
		ID:           clientID,
		Kind:         store.ClientKindCIMD,
		Name:         name,
		RedirectURIs: doc.RedirectURIs,
		ExpiresAt:    expires,
	}, expires, nil
}

// readMetadataBody checks that resp carries a JSON document no larger than
// maxBytes and returns its body.
func readMetadataBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != mediaTypeJSON {
		return nil, fmt.Errorf("oauth: client metadata: content-type %q is not %s",
			resp.Header.Get("Content-Type"), mediaTypeJSON)
	}

	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("oauth: client metadata: larger than %d bytes", maxBytes)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("oauth: read client metadata: %w", err)
	}

	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("oauth: client metadata: larger than %d bytes", maxBytes)
	}

	return body, nil
}

// validateClientIDURL enforces the CIMD client_id URL rules: https, a host,
// a path other than "/", no dot segments, no fragment and no userinfo.
func validateClientIDURL(id string) error {
	u, err := url.Parse(id)
	if err != nil {
		return fmt.Errorf("oauth: client_id is not a URL: %w", err)
	}

	switch {
	case u.Scheme != schemeHTTPS:
		return errors.New("oauth: client_id URL must use https")
	case u.Host == "" || u.Hostname() == "":
		return errors.New("oauth: client_id URL has no host")
	case u.User != nil:
		return errors.New("oauth: client_id URL must not contain userinfo")
	case u.Fragment != "" || strings.Contains(id, "#"):
		return errors.New("oauth: client_id URL must not contain a fragment")
	case u.Path == "" || u.Path == "/":
		return errors.New("oauth: client_id URL must have a path")
	}

	for seg := range strings.SplitSeq(u.Path, "/") {
		if seg == "." || seg == ".." {
			return errors.New("oauth: client_id URL must not contain dot segments")
		}
	}

	return nil
}

func validateDocument(clientID string, doc *clientMetadataDocument) error {
	if doc.ClientID != clientID {
		return fmt.Errorf("oauth: client metadata client_id %q does not match its URL", doc.ClientID)
	}

	if doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != authMethodNone {
		return fmt.Errorf("oauth: client metadata: token_endpoint_auth_method %q not supported, only %q",
			doc.TokenEndpointAuthMethod, authMethodNone)
	}

	if utf8.RuneCountInString(strings.TrimSpace(doc.ClientName)) > maxClientNameRunes {
		return fmt.Errorf("oauth: client metadata: client_name longer than %d characters", maxClientNameRunes)
	}

	if len(doc.RedirectURIs) == 0 {
		return errors.New("oauth: client metadata: redirect_uris is empty")
	}

	if len(doc.RedirectURIs) > maxRedirectURIs {
		return fmt.Errorf("oauth: client metadata: more than %d redirect_uris", maxRedirectURIs)
	}

	for _, r := range doc.RedirectURIs {
		if len(r) > maxRedirectURIBytes {
			return fmt.Errorf("oauth: client metadata: a redirect_uri is longer than %d bytes", maxRedirectURIBytes)
		}

		u, err := url.Parse(r)
		if err != nil || !u.IsAbs() || u.Fragment != "" {
			return fmt.Errorf("oauth: client metadata: redirect_uri %q is not an absolute URI without fragment", r)
		}
	}

	return nil
}

// cacheTTL reads max-age from a Cache-Control header, capped at maxCache.
// A missing, zero, negative or unparsable max-age yields defaultCacheTTL,
// itself capped at maxCache.
func cacheTTL(header string, maxCache time.Duration) time.Duration {
	fallback := min(defaultCacheTTL, maxCache)

	for directive := range strings.SplitSeq(header, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(directive), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), cacheControlMaxAge) {
			continue
		}

		secs, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(val), `"`), 10, 64)
		if errors.Is(err, strconv.ErrRange) && secs > 0 {
			return maxCache
		}

		if err != nil || secs <= 0 {
			return fallback
		}

		if secs >= int64(maxCache/time.Second) {
			return maxCache
		}

		return time.Duration(secs) * time.Second
	}

	return fallback
}

// isPublicAddr reports whether addr is a globally routable unicast address.
func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return false
	}

	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return false
		}
	}

	return true
}
